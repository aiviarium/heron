// Package fixtures extracts the code blocks of the design tabs into test
// fixtures and describes them with a manifest. The fixtures directory is
// generated: Generate reproduces it from docs/design, and a test fails when
// the checked-in copy drifts from the docs.
package fixtures

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Modes a fixture can be in. Mode decides which checks apply.
const (
	ModeHeron  = "heron"        // a Heron file: parses clean, formats to itself, checks clean
	ModeError  = "heron-error"  // Heron that shows an error on purpose: parses losslessly
	ModeSketch = "heron-sketch" // Heron with elided parts (…): parses losslessly with recovery
	ModeProse  = "heron-md"     // a prose file, .heron.md
	ModeOther  = "other"        // not Heron: a file tree, a grammar, TS, a commit message
)

// Block is one fenced code block of a design tab.
type Block struct {
	Tab     string `json:"tab"`   // the tab's slug, from its file name without the number
	Index   int    `json:"index"` // 1-based position of the block in its tab
	Lang    string `json:"lang"`  // the fence's info word, or ""
	Mode    string `json:"mode"`
	File    string `json:"file"` // path relative to the fixtures directory
	SHA256  string `json:"sha256"`
	Content string `json:"-"`
}

var fenceRE = regexp.MustCompile("^([ \t]*)(`{3,}|~{3,})[ \t]*([^`\\s]*)")

// Extract returns the fenced code blocks of one Markdown document, in order.
// Fences may be indented, as inside list items; the opening fence's
// indentation is removed from the content lines.
func Extract(tab, src string) []Block {
	var blocks []Block
	lines := strings.SplitAfter(src, "\n")
	for i := 0; i < len(lines); i++ {
		m := fenceRE.FindStringSubmatch(strings.TrimRight(lines[i], "\r\n"))
		if m == nil {
			continue
		}
		indent, fence, lang := m[1], m[2], m[3]
		var body strings.Builder
		j := i + 1
		for ; j < len(lines); j++ {
			line := strings.TrimRight(lines[j], "\r\n")
			t := strings.TrimLeft(line, " \t")
			if strings.HasPrefix(t, fence) && strings.Trim(t, fence[:1]) == "" && len(t) >= len(fence) {
				break
			}
			body.WriteString(strings.TrimPrefix(lines[j], indent))
		}
		blocks = append(blocks, Block{Tab: tab, Index: len(blocks) + 1, Lang: lang, Content: body.String()})
		i = j
	}
	return blocks
}

// TabSlug turns "01-core-format.md" into "core-format".
func TabSlug(file string) string {
	base := strings.TrimSuffix(filepath.Base(file), ".md")
	if i := strings.IndexByte(base, '-'); i > 0 {
		return base[i+1:]
	}
	return base
}

// ReadModes parses a modes file: one "tab/NN mode" per line, with # comments.
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
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("%s:%d: want \"tab/NN mode\"", path, n)
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

// Key is the block's key in the modes file, "tab/NN".
func (b Block) Key() string { return fmt.Sprintf("%s/%02d", b.Tab, b.Index) }

func defaultMode(lang string) string {
	switch lang {
	case "", "text", "heron":
		return ModeHeron
	case "heron.md":
		return ModeProse
	}
	return ModeOther
}

func extFor(b Block) string {
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

// Generate reads every docs/design/*.md and the modes file and returns the
// fixture files to write, keyed by path relative to the fixtures directory,
// including manifest.json. Modes naming no block are an error.
func Generate(designDir, modesPath string) (map[string][]byte, []Block, error) {
	modes, err := ReadModes(modesPath)
	if err != nil {
		return nil, nil, err
	}
	docs, err := filepath.Glob(filepath.Join(designDir, "*.md"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(docs)
	files := map[string][]byte{}
	var all []Block
	used := map[string]bool{}
	for _, doc := range docs {
		src, err := os.ReadFile(doc)
		if err != nil {
			return nil, nil, err
		}
		for _, b := range Extract(TabSlug(doc), string(src)) {
			b.Mode = defaultMode(b.Lang)
			if m, ok := modes[b.Key()]; ok {
				b.Mode = m
				used[b.Key()] = true
			}
			b.File = fmt.Sprintf("%s/%02d%s", b.Tab, b.Index, extFor(b))
			sum := sha256.Sum256([]byte(b.Content))
			b.SHA256 = hex.EncodeToString(sum[:])
			files[b.File] = []byte(b.Content)
			all = append(all, b)
		}
	}
	for k := range modes {
		if !used[k] {
			return nil, nil, fmt.Errorf("%s: %s names no code block", modesPath, k)
		}
	}
	manifest, err := json.MarshalIndent(all, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	files["manifest.json"] = append(manifest, '\n')
	return files, all, nil
}

// Load reads the manifest and the fixture contents from a fixtures directory.
func Load(dir string) ([]Block, error) {
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil, err
	}
	var blocks []Block
	if err := json.Unmarshal(data, &blocks); err != nil {
		return nil, err
	}
	for i := range blocks {
		c, err := os.ReadFile(filepath.Join(dir, blocks[i].File))
		if err != nil {
			return nil, err
		}
		blocks[i].Content = string(c)
	}
	return blocks, nil
}

// IsHeron reports whether the fixture is Heron source of any mode.
func (b Block) IsHeron() bool {
	return b.Mode == ModeHeron || b.Mode == ModeError || b.Mode == ModeSketch || b.Mode == ModeProse
}
