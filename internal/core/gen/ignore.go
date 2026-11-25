package gen

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The managed ignore block: one block of .gitignore between two marker
// lines, which heron rewrites and everything outside of which it keeps.
// Inside, each writer owns a section under a `# from <owner>` line, so
// `heron run setup` and `heron generate` keep their entries side by side
// without overwriting each other's.
const (
	BlockBegin    = "# heron:begin (managed by heron; edits inside are replaced)"
	BlockEnd      = "# heron:end"
	sectionPrefix = "# from "
)

// Owners of the block's sections.
const (
	OwnerGenerate = "heron generate"
	OwnerSetup    = "heron run setup"
)

// IgnoreSection writes one owner's section of the managed block. It
// implements setup's IgnoreWriter, which `heron run setup` writes through.
type IgnoreSection struct{ Owner string }

// WriteIgnore sets the owner's section of root/.gitignore to entries and
// reports whether the file changed.
func (s IgnoreSection) WriteIgnore(root string, entries []string) (bool, error) {
	path := filepath.Join(root, ".gitignore")
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	text, err := IgnoreBlock(string(old), s.Owner, entries)
	if err != nil {
		return false, err
	}
	return writeIfChanged(path, []byte(text))
}

// IgnoreBlock returns a file's text with owner's section of the managed
// block set to entries; the block is appended when the file has none.
func IgnoreBlock(text, owner string, entries []string) (string, error) {
	sections := map[string][]string{}
	before, after := text, ""
	if i := strings.Index(text, BlockBegin); i >= 0 {
		j := strings.Index(text[i:], BlockEnd)
		if j < 0 {
			return "", errors.New(".gitignore has the start of heron's block but not its end; remove the start line or add " + BlockEnd)
		}
		inner := text[i+len(BlockBegin) : i+j]
		before, after = text[:i], strings.TrimPrefix(text[i+j+len(BlockEnd):], "\n")
		cur := OwnerGenerate
		for _, l := range strings.Split(inner, "\n") {
			l = strings.TrimSpace(l)
			switch {
			case l == "":
			case strings.HasPrefix(l, sectionPrefix):
				cur = strings.TrimPrefix(l, sectionPrefix)
			default:
				sections[cur] = append(sections[cur], l)
			}
		}
	} else {
		if before != "" && !strings.HasSuffix(before, "\n") {
			before += "\n"
		}
		if before != "" {
			before += "\n"
		}
	}
	sections[owner] = uniqSorted(entries)
	owners := make([]string, 0, len(sections))
	for o, es := range sections {
		if len(es) > 0 {
			owners = append(owners, o)
		}
	}
	sort.Strings(owners)
	var b strings.Builder
	b.WriteString(BlockBegin + "\n")
	for _, o := range owners {
		b.WriteString(sectionPrefix + o + "\n")
		for _, e := range sections[o] {
			b.WriteString(e + "\n")
		}
	}
	b.WriteString(BlockEnd + "\n")
	return before + b.String() + after, nil
}

func uniqSorted(entries []string) []string {
	es := append([]string(nil), entries...)
	sort.Strings(es)
	var out []string
	for i, e := range es {
		if i == 0 || e != es[i-1] {
			out = append(out, e)
		}
	}
	return out
}
