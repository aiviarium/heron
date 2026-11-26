package setup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/resolve"
)

// Git runs git in a folder and returns what it wrote to stdout.
type Git func(ctx context.Context, dir string, args ...string) ([]byte, error)

// ExecGit runs the git on PATH.
func ExecGit(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errs.String())
		if i := strings.LastIndexByte(msg, '\n'); i >= 0 {
			msg = msg[i+1:]
		}
		return nil, fmt.Errorf("git %s: %v %s", strings.Join(args, " "), err, msg)
	}
	return out.Bytes(), nil
}

// InstallFromLock installs every package from git that heron.lock under
// root pins, at its pinned commit, into .heron/packages/, and checks each
// against its checksum. It reads nothing but the lock, so it runs before
// the tree that imports the packages can check. A package already
// installed with its pinned checksum is left alone.
func InstallFromLock(ctx context.Context, root string, git Git) (installed []string, err error) {
	lock, err := ReadLock(filepath.Join(root, LockFile))
	if err != nil {
		return nil, err
	}
	for _, name := range sortedKeys(lock.Packages) {
		pin := lock.Packages[name]
		if !pin.Git() {
			continue
		}
		changed, err := installPin(ctx, root, name, pin, git)
		if err != nil {
			return installed, err
		}
		if changed {
			installed = append(installed, name)
		}
	}
	return installed, nil
}

// PinGitImports pins the git imports of a tree that heron.lock doesn't pin
// yet, or pins for another url, folder or tag, and installs them; with
// update, imports of a major version move to its newest tag. It writes the
// lock when a pin changed and returns the names it pinned.
func PinGitImports(ctx context.Context, root string, imports []resolve.GitImport, update bool, git Git) ([]string, error) {
	file := filepath.Join(root, LockFile)
	lock, err := ReadLock(file)
	if err != nil {
		return nil, err
	}
	var pinned []string
	seen := map[string]bool{}
	for _, g := range imports {
		if seen[g.Name] {
			continue
		}
		seen[g.Name] = true
		pin, ok := lock.Packages[g.Name]
		// A pin at a version tag holds for the import's major version; one
		// at a branch, as before a package's first release, holds by its
		// commit.
		major, tagged := resolve.TagMajor(pin.Ref)
		holds := ok && pin.URL == g.URL && pin.Path == g.Path && pin.Commit != "" &&
			(pin.Ref == g.Ref || g.Ref == "" && (!tagged || major == g.Major))
		if holds && (!update || g.Ref != "") {
			continue
		}
		ref, commit, err := resolveTag(ctx, root, g, git)
		if err != nil {
			return pinned, err
		}
		if holds && pin.Commit == commit {
			continue
		}
		next := Pin{URL: g.URL, Path: g.Path, Ref: ref, Commit: commit}
		if _, err := fetch(ctx, root, g.Name, next, git); err != nil {
			return pinned, err
		}
		if next.Checksum, err = DirChecksum(installDir(root, g.Name)); err != nil {
			return pinned, err
		}
		lock.Packages[g.Name] = next
		pinned = append(pinned, g.Name)
	}
	if len(pinned) > 0 {
		if err := os.WriteFile(file, []byte(lock.Format()), 0o644); err != nil {
			return pinned, err
		}
	}
	return pinned, nil
}

func installDir(root, name string) string {
	return resolve.GitImport{Name: name}.InstallDir(root)
}

// installPin makes the install of a pinned package match its checksum,
// fetching it again when it doesn't.
func installPin(ctx context.Context, root, name string, pin Pin, git Git) (bool, error) {
	if sum, err := DirChecksum(installDir(root, name)); err == nil && sum == pin.Checksum {
		return false, nil
	}
	if pin.Commit == "" {
		return false, fmt.Errorf("heron.lock pins %s from %s but no commit", name, pin.URL)
	}
	if _, err := fetch(ctx, root, name, pin, git); err != nil {
		return false, err
	}
	sum, err := DirChecksum(installDir(root, name))
	if err != nil {
		return false, err
	}
	if sum != pin.Checksum {
		return false, fmt.Errorf("heron.lock pins %s at %s (%s), but that commit has %s; run setup with --update to move the pin",
			name, pin.Commit, pin.Checksum, sum)
	}
	return true, nil
}

// fetch installs the package's folder at the pin's commit, replacing what
// was installed.
func fetch(ctx context.Context, root, name string, pin Pin, git Git) (string, error) {
	tmp, err := os.MkdirTemp("", "heron-git-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if _, err := git(ctx, root, "clone", "-q", "--no-checkout", pin.URL, tmp); err != nil {
		return "", fmt.Errorf("can't clone %s for %s: %v", pin.URL, name, err)
	}
	if _, err := git(ctx, tmp, "checkout", "-q", "--detach", pin.Commit); err != nil {
		return "", fmt.Errorf("can't check out %s of %s for %s: %v", pin.Commit, pin.URL, name, err)
	}
	src := filepath.Join(tmp, filepath.FromSlash(pin.Path))
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return "", fmt.Errorf("%s has no folder %q at %s", pin.URL, pin.Path, pin.Commit)
	}
	dst := installDir(root, name)
	if err := os.RemoveAll(dst); err != nil {
		return "", err
	}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
	return dst, err
}

// DirChecksum hashes the files of an installed package by their paths
// inside it and their content, as a package's checksum is everywhere.
func DirChecksum(dir string) (string, error) {
	type f struct{ rel, src string }
	var files []f
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		files = append(files, f{filepath.ToSlash(rel), string(b)})
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].rel < files[j].rel })
	h := sha256.New()
	for _, x := range files {
		fmt.Fprintf(h, "%s\x00%d\x00%s", x.rel, len(x.src), x.src)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// resolveTag finds the tag an import names, or the newest tag of its major
// version, and the commit the tag points at.
func resolveTag(ctx context.Context, root string, g resolve.GitImport, git Git) (tag, commit string, err error) {
	out, err := git(ctx, root, "ls-remote", "--tags", g.URL)
	if err != nil {
		return "", "", fmt.Errorf("can't read the tags of %s: %v", g.URL, err)
	}
	commits := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || !strings.HasPrefix(f[1], "refs/tags/") {
			continue
		}
		name := strings.TrimPrefix(f[1], "refs/tags/")
		if peeled, ok := strings.CutSuffix(name, "^{}"); ok {
			commits[peeled] = f[0] // an annotated tag, peeled to its commit
		} else if _, ok := commits[name]; !ok {
			commits[name] = f[0]
		}
	}
	if g.Ref != "" {
		if c, ok := commits[g.Ref]; ok {
			return g.Ref, c, nil
		}
		return "", "", fmt.Errorf("%s has no tag %s", g.URL, g.Ref)
	}
	var best []int
	for name := range commits {
		if m, ok := resolve.TagMajor(name); !ok || m != g.Major {
			continue
		}
		v := versionNumbers(name)
		if best == nil || newer(v, best) || equal(v, best) && name > tag {
			best, tag = v, name
		}
	}
	if tag == "" {
		return "", "", fmt.Errorf("%s has no tag of version %d, as v%d.0.0", g.URL, g.Major, g.Major)
	}
	return tag, commits[tag], nil
}

func versionNumbers(tag string) []int {
	var out []int
	for _, p := range strings.Split(strings.TrimPrefix(tag, "v"), ".") {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

func newer(a, b []int) bool {
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := at(a, i), at(b, i)
		if x != y {
			return x > y
		}
	}
	return false
}

func equal(a, b []int) bool { return !newer(a, b) && !newer(b, a) }

func at(v []int, i int) int {
	if i < len(v) {
		return v[i]
	}
	return 0
}
