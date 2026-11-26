package setup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/resolve"
)

// A package pinned at a branch, as an unreleased package is, installs from
// its pinned commit and holds: setup asks the remote for no tag.
func TestBranchPinInstallsByCommit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	gitIn := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitIn(repo, "init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(repo, "spec"), 0o755)
	os.WriteFile(filepath.Join(repo, "spec", "index.heron"), []byte("spec: package(version = \"0.5.0\") {}\n"), 0o644)
	gitIn(repo, "add", "-A")
	gitIn(repo, "commit", "-qm", "spec")
	commit := gitIn(repo, "rev-parse", "HEAD")
	// A later commit on main, which the pin must not follow.
	os.WriteFile(filepath.Join(repo, "spec", "later.heron"), []byte("x: section {}\n"), 0o644)
	gitIn(repo, "add", "-A")
	gitIn(repo, "commit", "-qm", "later")

	root := t.TempDir()
	url := "file://" + repo
	name := "example.com/spec"
	lock := &Lock{Packages: map[string]Pin{name: {URL: url, Path: "spec", Ref: "main", Commit: commit}}}
	// The checksum of the pinned commit's folder.
	if _, err := fetch(context.Background(), root, name, lock.Packages[name], ExecGit); err != nil {
		t.Fatal(err)
	}
	sum, err := DirChecksum(installDir(root, name))
	if err != nil {
		t.Fatal(err)
	}
	p := lock.Packages[name]
	p.Checksum = sum
	lock.Packages[name] = p
	os.RemoveAll(filepath.Join(root, ".heron"))
	if err := os.WriteFile(filepath.Join(root, LockFile), []byte(lock.Format()), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []string
	git := func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		return ExecGit(ctx, dir, args...)
	}
	installed, err := InstallFromLock(context.Background(), root, git)
	if err != nil || len(installed) != 1 {
		t.Fatalf("install: %v %v", installed, err)
	}
	if _, err := os.Stat(filepath.Join(installDir(root, name), "later.heron")); !os.IsNotExist(err) {
		t.Error("the install followed main past the pinned commit")
	}
	pinned, err := PinGitImports(context.Background(), root, []resolve.GitImport{{Name: name, URL: url, Path: "spec", Major: 0}}, false, git)
	if err != nil || len(pinned) != 0 {
		t.Fatalf("pin: %v %v", pinned, err)
	}
	for _, c := range calls {
		if strings.HasPrefix(c, "ls-remote") {
			t.Errorf("setup asked for tags: %s", c)
		}
	}
	after, _ := ReadLock(filepath.Join(root, LockFile))
	if got := after.Packages[name]; got.Ref != "main" || got.Commit != commit || got.Checksum != sum {
		t.Errorf("the pin moved: %+v", got)
	}
}
