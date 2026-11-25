// Package fixtures reads the parser and checker fixtures from the spec
// package's example nodes. The spec, imported from aiviarium/heron-specs,
// is their only source: every fenced code block in the `|` lines of a
// `name: example { … }` node is one fixture, keyed by the node's path, so
// changing an example changes what the implementation is tested against.
// Nothing is copied into this repository.
package fixtures

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
)

// Modes a fixture can be in. Mode decides which checks apply.
const (
	ModeHeron  = "heron"        // a Heron file: parses clean, formats to itself, checks clean
	ModeError  = "heron-error"  // Heron that shows an error on purpose: parses losslessly
	ModeSketch = "heron-sketch" // Heron with elided parts (…): parses losslessly with recovery
	ModeProse  = "heron-md"     // a prose file, .heron.md
	ModeOther  = "other"        // not Heron: a file tree, a grammar, TS, a commit message
)

// Block is one fenced code block of an example node of the spec package.
type Block struct {
	// Path is the example node's path inside the spec package, as
	// "language.core_format.tour.format_tour".
	Path string `json:"path"`
	// Index is the block's 1-based position among its node's blocks.
	Index int    `json:"index"`
	Lang  string `json:"lang"` // the fence's info word, or ""
	Mode  string `json:"mode"`
	// File names the block in messages: the spec file holding it,
	// relative to the package's folder, and the block's first line, as
	// "language/core_format/tour.heron:21".
	File string `json:"file"`
	// Source is the spec file's absolute path.
	Source  string `json:"-"`
	Content string `json:"-"`
}

// Key is the block's key in modes.txt and pending.txt: the example node's
// path, with "#2", "#3" … for the further blocks of one node.
func (b Block) Key() string {
	if b.Index > 1 {
		return fmt.Sprintf("%s#%d", b.Path, b.Index)
	}
	return b.Path
}

// Ext is the extension of a file that holds the block.
func (b Block) Ext() string {
	switch b.Mode {
	case ModeHeron, ModeError, ModeSketch:
		return ".heron"
	case ModeProse:
		return ".heron.md"
	}
	switch b.Lang {
	case "", "text":
		return ".txt"
	}
	return "." + b.Lang
}

// Name is a file name for the block, unique in the package, as
// "language.core_format.tour.format_tour.heron".
func (b Block) Name() string {
	name := b.Path
	if b.Index > 1 {
		name += fmt.Sprintf(".%d", b.Index)
	}
	return name + b.Ext()
}

// IsHeron reports whether the fixture is Heron source of any mode.
func (b Block) IsHeron() bool {
	return b.Mode == ModeHeron || b.Mode == ModeError || b.Mode == ModeSketch || b.Mode == ModeProse
}

// SpecPackage is the name the spec package is installed under in
// .heron/packages/, after its git import
// "git+https://github.com/aiviarium/heron-specs//heron@v0".
const SpecPackage = "github.com/aiviarium/heron-specs/heron"

// SpecDirEnv names the variable that points at a checkout of the spec
// package, such as ../heron-specs/heron, in place of the installed one.
const SpecDirEnv = "HERON_SPECS_DIR"

// SpecDir is the folder of the spec package for the repository at root:
// $HERON_SPECS_DIR when it's set, otherwise the package `heron run setup`
// installed (or scripts/spec-setup.sh linked) under .heron/packages/.
func SpecDir(root string) (string, error) {
	d := os.Getenv(SpecDirEnv)
	if d == "" {
		d = filepath.Join(root, filepath.FromSlash(resolve.PackagesDir), filepath.FromSlash(SpecPackage))
	}
	if _, err := os.Stat(filepath.Join(d, "index.heron")); err != nil {
		return "", fmt.Errorf("the spec package isn't at %s: run scripts/spec-setup.sh, which runs heron run setup or links $%s, a checkout such as ../heron-specs/heron", d, SpecDirEnv)
	}
	abs, err := filepath.Abs(d)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}

// SpecEntry is the spec package's index.heron, which its tree checks from.
func SpecEntry(root string) (string, error) {
	d, err := SpecDir(root)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "index.heron"), nil
}

// Load reads the fixtures of the repository at root: the code blocks of
// the spec package's example nodes, by key, each in the mode
// fixtures/modes.txt gives it or else its fence's default. A modes entry
// that names no block is an error.
func Load(root string) ([]Block, error) {
	dir, err := SpecDir(root)
	if err != nil {
		return nil, err
	}
	modesPath := filepath.Join(root, "fixtures", "modes.txt")
	modes, err := ReadModes(modesPath)
	if err != nil {
		return nil, err
	}
	blocks, err := FromPackage(dir)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for i := range blocks {
		b := &blocks[i]
		b.Mode = defaultMode(b.Lang)
		if m, ok := modes[b.Key()]; ok {
			b.Mode = m
			used[b.Key()] = true
		}
	}
	for k := range modes {
		if !used[k] {
			return nil, fmt.Errorf("%s: %s names no code block of an example", modesPath, k)
		}
	}
	return blocks, nil
}

// FromPackage returns the code blocks of the example nodes of the spec
// package in dir, with their node paths, sorted by key. Mode is left
// empty.
func FromPackage(dir string) ([]Block, error) {
	entry, err := resolve.Canonical(filepath.Join(dir, "index.heron"))
	if err != nil {
		return nil, err
	}
	tree := resolve.New(resolve.NewLoader()).Resolve(entry)
	// An example node is matched to its code by its file and the line
	// its name is on.
	type at struct {
		file string
		line int
	}
	paths := map[at]string{}
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range n.Children {
			if ch.Shape == resolve.ShapeMounted {
				continue
			}
			if l := nameLine(ch); l > 0 {
				paths[at{string(ch.Decl.File.ID), l}] = relPath(ch)
			}
			walk(ch)
		}
	}
	walk(tree.Root)
	files, err := SpecFiles(dir)
	if err != nil {
		return nil, err
	}
	var out []Block
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		id, _ := resolve.Canonical(f)
		rel, _ := filepath.Rel(dir, f)
		rel = filepath.ToSlash(rel)
		count := map[string]int{}
		for _, ex := range SpecExamples(rel, string(src)) {
			p, ok := paths[at{string(id), ex.NameLine}]
			if !ok {
				return nil, fmt.Errorf("%s:%d: example %s isn't a node of the spec package's tree", rel, ex.NameLine, ex.Name)
			}
			count[p]++
			out = append(out, Block{
				Path: p, Index: count[p], Lang: ex.Lang,
				File: fmt.Sprintf("%s:%d", rel, ex.Line), Source: f, Content: ex.Content,
			})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out, nil
}

// nameLine is the 1-based line a declared node's name is on, after any
// annotations and doc lines its declaration starts with, or 0.
func nameLine(n *resolve.Node) int {
	if n.Decl.N == nil || n.Decl.File == nil || n.Name == "" {
		return 0
	}
	src := n.Decl.File.Src
	start, end := n.Decl.Span()
	for off := start; off < end; {
		eol := strings.IndexByte(src[off:end], '\n')
		line := src[off:end]
		if eol >= 0 {
			line = src[off : off+eol]
		}
		if strings.HasPrefix(strings.TrimSpace(line), n.Name+":") {
			l, _ := syntax.LineCol(src, off)
			return l
		}
		if eol < 0 {
			break
		}
		off += eol + 1
	}
	return 0
}

// relPath is a node's path inside its package: its path in the tree,
// without the package's own `name: package(…)` node at the top.
func relPath(n *resolve.Node) string {
	var parts []string
	x := n
	for ; x.Parent != nil && x.Parent.Parent != nil; x = x.Parent {
		parts = append([]string{x.Name}, parts...)
	}
	if !isPackageNode(x) {
		parts = append([]string{x.Name}, parts...)
	}
	return strings.Join(parts, ".")
}

// isPackageNode reports whether a node is declared as `name: package(…)`.
func isPackageNode(n *resolve.Node) bool {
	l := nameLine(n)
	if l == 0 {
		return false
	}
	start, end := n.Decl.Span()
	text := n.Decl.File.Src[start:end]
	i := strings.Index(text, n.Name+":")
	return i >= 0 && strings.HasPrefix(strings.TrimSpace(text[i+len(n.Name)+1:]), "package(")
}

// ReadModes parses a modes file: one "key mode" per line, with # comments.
func ReadModes(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer f.Close()
	modes := map[string]string{}
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		fields := Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want \"example.path mode\"", path, n)
		}
		switch fields[1] {
		case ModeHeron, ModeError, ModeSketch, ModeProse, ModeOther:
		default:
			return nil, fmt.Errorf("%s:%d: unknown mode %q", path, n, fields[1])
		}
		modes[fields[0]] = fields[1]
	}
	return modes, sc.Err()
}

// Fields splits a line of a fixtures list into its words, without its #
// comment.
func Fields(line string) []string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		line = line[:i]
	}
	return strings.Fields(line)
}

func defaultMode(lang string) string {
	switch lang {
	case "", "text", "heron":
		return ModeHeron
	case "heron.md":
		return ModeProse
	}
	return ModeOther
}
