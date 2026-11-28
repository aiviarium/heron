package resolve

import (
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/std"
)

// FileID identifies a file canonically: one identity per file, whatever
// spelling reached it. A file on disk is its cleaned absolute path with
// symlinks resolved; an embedded standard file is "std:" and its path in the
// embedded tree, such as "std:doc/package.heron".
type FileID string

const stdPrefix = "std:"

// IsStd reports whether the file is embedded in the binary.
func (id FileID) IsStd() bool { return strings.HasPrefix(string(id), stdPrefix) }

// Dir returns the identity's directory, in the same namespace.
func (id FileID) Dir() string {
	if id.IsStd() {
		return stdPrefix + path.Dir(strings.TrimPrefix(string(id), stdPrefix))
	}
	return filepath.Dir(string(id))
}

// Canonical returns the identity of a path on disk: absolute, cleaned, and
// with every symlink resolved. A path that doesn't exist yet (an unsaved
// editor buffer) is resolved through its nearest existing parent, so it gets
// the identity it will have once written.
func Canonical(p string) (FileID, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return FileID(r), nil
	}
	dir, base := filepath.Split(abs)
	if dir == abs || dir == "" {
		return FileID(abs), nil
	}
	parent, err := Canonical(strings.TrimSuffix(dir, string(filepath.Separator)))
	if err != nil {
		return "", err
	}
	return FileID(filepath.Join(string(parent), base)), nil
}

// join resolves a relative reference against a directory in the same
// namespace, giving a canonical identity.
func join(dir, rel string) (FileID, error) {
	if strings.HasPrefix(dir, stdPrefix) {
		p := path.Clean(path.Join(strings.TrimPrefix(dir, stdPrefix), rel))
		if strings.HasPrefix(p, "..") {
			return "", errors.New("path leaves the standard packages")
		}
		return FileID(stdPrefix + p), nil
	}
	if filepath.IsAbs(rel) {
		return Canonical(rel)
	}
	return Canonical(filepath.Join(dir, rel))
}

// File is a parsed source file.
type File struct {
	ID   FileID
	Src  string
	Hash [32]byte
	Tree *syntax.Tree
	// Expansion is set on source a macro produced, which no file on disk
	// holds: it names the invocation the source stands for.
	Expansion *Expansion
}

// Loader reads and parses files. Parses are memoized per file by content
// hash, so a watch loop or the language server reparses only what changed.
type Loader struct {
	// Overlay holds unsaved contents by identity; it wins over the disk.
	Overlay map[FileID]string
	files   map[FileID]*File
	// Parses counts the files parsed, for tests of memoization.
	Parses int
}

// NewLoader returns a loader with an empty cache.
func NewLoader() *Loader {
	return &Loader{Overlay: map[FileID]string{}, files: map[FileID]*File{}}
}

func (l *Loader) read(id FileID) (string, error) {
	if s, ok := l.Overlay[id]; ok {
		return s, nil
	}
	if id.IsStd() {
		b, err := fs.ReadFile(std.FS, strings.TrimPrefix(string(id), stdPrefix))
		return string(b), err
	}
	b, err := os.ReadFile(string(id))
	return string(b), err
}

// Load returns the parsed file, reparsing only when its content changed.
func (l *Loader) Load(id FileID) (*File, error) {
	src, err := l.read(id)
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256([]byte(src))
	if f, ok := l.files[id]; ok && f.Hash == h {
		return f, nil
	}
	l.Parses++
	f := &File{ID: id, Src: src, Hash: h, Tree: syntax.Parse(string(id), src)}
	l.files[id] = f
	return f, nil
}

// Cached returns the file as last loaded, or nil.
func (l *Loader) Cached(id FileID) *File { return l.files[id] }

// isDir reports whether the identity names a directory.
func (l *Loader) isDir(id FileID) bool {
	if id.IsStd() {
		st, err := fs.Stat(std.FS, strings.TrimPrefix(string(id), stdPrefix))
		return err == nil && st.IsDir()
	}
	st, err := os.Stat(string(id))
	return err == nil && st.IsDir()
}

func (l *Loader) exists(id FileID) bool {
	if _, ok := l.Overlay[id]; ok {
		return true
	}
	if id.IsStd() {
		_, err := fs.Stat(std.FS, strings.TrimPrefix(string(id), stdPrefix))
		return err == nil
	}
	_, err := os.Stat(string(id))
	return err == nil
}

// glob expands a pattern relative to dir, with `*` within a segment and `**`
// across segments, and returns the matches in path order.
func (l *Loader) glob(dir, pattern string) ([]FileID, error) {
	pattern = path.Clean(filepath.ToSlash(pattern))
	segs := strings.Split(pattern, "/")
	// The literal prefix before the first segment with a meta character.
	i := 0
	for i < len(segs) && !strings.ContainsAny(segs[i], "*?[") {
		i++
	}
	base, err := join(dir, strings.Join(segs[:i], "/"))
	if err != nil {
		return nil, err
	}
	rest := segs[i:]
	var out []FileID
	walk := func(rel string) {
		if matchSegs(rest, strings.Split(rel, "/")) {
			id, err := join(string(base), rel)
			if err == nil {
				out = append(out, id)
			}
		}
	}
	if base.IsStd() {
		root := strings.TrimPrefix(string(base), stdPrefix)
		_ = fs.WalkDir(std.FS, root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				walk(strings.TrimPrefix(strings.TrimPrefix(p, root), "/"))
			}
			return nil
		})
	} else {
		root := string(base)
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(root, p)
				walk(filepath.ToSlash(rel))
			}
			return nil
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func matchSegs(pat, name []string) bool {
	if len(pat) == 0 {
		return len(name) == 0
	}
	if pat[0] == "**" {
		for k := 0; k <= len(name); k++ {
			if matchSegs(pat[1:], name[k:]) {
				return true
			}
		}
		return false
	}
	if len(name) == 0 {
		return false
	}
	ok, err := path.Match(pat[0], name[0])
	return err == nil && ok && matchSegs(pat[1:], name[1:])
}
