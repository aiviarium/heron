// Package prose is unit 2's prose linting: Vale run as a process over the
// prose view, with its alerts mapped back to exact source ranges, its fixes
// and vocabulary actions, and the spec-aware prose lints of std/lint. Both
// register as prose providers, unit 1's extension point.
package prose

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/lint"
	coreprose "github.com/aiviarium/heron/internal/core/lint/prose"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// ValeName is the Vale provider's name; its findings are coded
// "Vale.<check>", such as "Vale.Vale.Spelling".
const ValeName = "Vale"

// Register adds the Vale provider and the spec-aware prose lints.
func Register(r *registry.Registry) {
	r.MustAddProseProvider(registry.ProseProvider{
		Name:    ValeName,
		Summary: "Vale's styles and vocabularies over the prose view",
		Prepare: prepareVale,
	})
	r.MustAddProseProvider(SpecProvider())
	r.MustAddSetupStep(SetupStep())
}

// ValeConfig is the `lint { vale { … } }` configuration of a tree, or the
// .vale.ini found above its entry point.
type ValeConfig struct {
	Config   string   // the .vale.ini, absolute
	MinLevel string   // "suggestion", "warning" or "error"
	Skip     []string // scopes of the projected document left alone
	// StylesPath and Vocab come from the .vale.ini.
	StylesPath string
	Vocab      []string
}

// Binary finds the Vale binary: HERON_VALE, then PATH, then the Go bin
// directory `go install` puts it in.
func Binary() string {
	if p := os.Getenv("HERON_VALE"); p != "" {
		return p
	}
	if p, err := exec.LookPath("vale"); err == nil {
		return p
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		if home, err := os.UserHomeDir(); err == nil {
			gopath = filepath.Join(home, "go")
		}
	}
	if gopath != "" {
		p := filepath.Join(gopath, "bin", "vale")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// configs remembers the configuration last used for each file, for the
// vocabulary actions.
var configs sync.Map // file → *ValeConfig

// ConfigFor reads a tree's Vale configuration: the `lint.vale` node, or
// the nearest .vale.ini above the entry point. It returns nil when there
// is none.
func ConfigFor(t *resolve.Tree) *ValeConfig {
	cfg := &ValeConfig{MinLevel: "suggestion", Skip: []string{"code"}}
	if v := t.Find("lint.vale"); v != nil {
		if c := v.Child("config"); c != nil && c.Value != nil && c.ValueSite.File != nil {
			if s, ok := resolve.StringValue(c.Value.Syntax()); ok {
				p := s
				if !filepath.IsAbs(p) {
					p = filepath.Join(c.ValueSite.File.ID.Dir(), p)
				}
				cfg.Config = p
			}
		}
		if c := v.Child("min_level"); c != nil && c.Value != nil {
			if s, ok := resolve.StringValue(c.Value.Syntax()); ok {
				cfg.MinLevel = s
			}
		}
		if c := v.Child("skip"); c != nil && c.Value != nil {
			cfg.Skip = []string{}
			if arr, ok := c.Value.(ast.ArrayExpr); ok {
				for _, x := range arr.Exprs() {
					if s, ok := resolve.StringValue(x.Syntax()); ok {
						cfg.Skip = append(cfg.Skip, s)
					}
				}
			}
		}
	}
	if cfg.Config == "" && t.Entry != nil && !t.Entry.ID.IsStd() {
		for dir := filepath.Dir(string(t.Entry.ID)); ; dir = filepath.Dir(dir) {
			p := filepath.Join(dir, ".vale.ini")
			if _, err := os.Stat(p); err == nil {
				cfg.Config = p
				break
			}
			if filepath.Dir(dir) == dir {
				break
			}
		}
	}
	if cfg.Config == "" {
		return nil
	}
	if _, err := os.Stat(cfg.Config); err != nil {
		return nil
	}
	cfg.readIni()
	return cfg
}

// readIni reads StylesPath and Vocab from the .vale.ini's global section.
func (c *ValeConfig) readIni() {
	b, err := os.ReadFile(c.Config)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			break
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "StylesPath":
			if !filepath.IsAbs(v) {
				v = filepath.Join(filepath.Dir(c.Config), v)
			}
			c.StylesPath = v
		case "Vocab":
			for _, x := range strings.Split(v, ",") {
				if x = strings.TrimSpace(x); x != "" {
					c.Vocab = append(c.Vocab, x)
				}
			}
		}
	}
}

// key hashes everything Vale's results depend on besides the text: the
// binary, the configuration, and every style and vocabulary file, so
// accepting a word re-lints.
func (c *ValeConfig) key(bin string) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00%s\x00%v\x00", bin, c.Config, c.MinLevel, c.Skip)
	if b, err := os.ReadFile(c.Config); err == nil {
		h.Write(b)
	}
	if c.StylesPath != "" {
		_ = filepath.WalkDir(c.StylesPath, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if b, err := os.ReadFile(p); err == nil {
				fmt.Fprintf(h, "%s\x00", p)
				h.Write(b)
			}
			return nil
		})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func prepareVale(in any) (string, func([]*registry.ProseRegion) []registry.ProseFinding) {
	li, ok := in.(*lint.Input)
	if !ok {
		return "", nil
	}
	cfg := ConfigFor(li.Tree)
	bin := Binary()
	if cfg == nil || bin == "" {
		return "", nil
	}
	return cfg.key(bin), func(regions []*registry.ProseRegion) []registry.ProseFinding {
		return runVale(bin, cfg, regions)
	}
}

// valeAlert is one alert of `vale --output=JSON`.
type valeAlert struct {
	Action struct {
		Name   string   `json:"Name"`
		Params []string `json:"Params"`
	} `json:"Action"`
	Span     [2]int `json:"Span"` // 1-based columns in runes, inclusive
	Check    string `json:"Check"`
	Message  string `json:"Message"`
	Severity string `json:"Severity"`
	Match    string `json:"Match"`
	Line     int    `json:"Line"`
}

var levels = map[string]int{"suggestion": 0, "warning": 1, "error": 2}

// skipBlock maps Vale's scope names to the prose view's blocks.
var skipBlock = map[string]string{
	"code": "code", "pre": "code", "heading": "heading", "h1": "heading", "h2": "heading", "h3": "heading",
	"list": "item", "li": "item", "blockquote": "quote", "quote": "quote", "paragraph": "paragraph", "p": "paragraph",
}

// runVale lints regions: each file's regions as one HTML document, one
// Vale call per file, the files in parallel.
func runVale(bin string, cfg *ValeConfig, regions []*registry.ProseRegion) []registry.ProseFinding {
	byFile := map[string][]int{}
	var files []string
	for i, r := range regions {
		if _, ok := byFile[r.File]; !ok {
			files = append(files, r.File)
		}
		byFile[r.File] = append(byFile[r.File], i)
		configs.Store(r.File, cfg)
	}
	sort.Strings(files)
	skipped := map[string]bool{}
	for _, s := range cfg.Skip {
		if b, ok := skipBlock[s]; ok {
			skipped[b] = true
		}
	}
	results := make([][]registry.ProseFinding, len(files))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for fi, file := range files {
		var group []*registry.ProseRegion
		var index []int
		for _, i := range byFile[file] {
			if !skipped[regions[i].Block] {
				group = append(group, regions[i])
				index = append(index, i)
			}
		}
		if len(group) == 0 {
			continue
		}
		wg.Add(1)
		go func(fi int, group []*registry.ProseRegion, index []int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[fi] = valeFile(bin, cfg, group, index)
		}(fi, group, index)
	}
	wg.Wait()
	var out []registry.ProseFinding
	for _, r := range results {
		out = append(out, r...)
	}
	return out
}

func valeFile(bin string, cfg *ValeConfig, group []*registry.ProseRegion, index []int) []registry.ProseFinding {
	doc := coreprose.RenderHTML(group)
	alerts, err := vale(bin, cfg, doc.Text)
	if err != nil {
		return []registry.ProseFinding{{Region: index[0], Start: 0, End: 0, Rule: "Error", Severity: diag.Warning,
			Message: "vale failed: " + err.Error()}}
	}
	lineStarts := []int{0}
	for i := 0; i < len(doc.Text); i++ {
		if doc.Text[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	// byteAt converts a 1-based line and rune column to a byte offset.
	byteAt := func(line, col int) int {
		if line < 1 || line > len(lineStarts) {
			return -1
		}
		i := lineStarts[line-1]
		for k := 1; k < col && i < len(doc.Text) && doc.Text[i] != '\n'; k++ {
			_, n := utf8.DecodeRuneInString(doc.Text[i:])
			i += n
		}
		return i
	}
	min := levels[cfg.MinLevel]
	var out []registry.ProseFinding
	fixes := 0
	for _, a := range alerts {
		if levels[a.Severity] < min {
			continue
		}
		s := byteAt(a.Line, a.Span[0])
		e := byteAt(a.Line, a.Span[1]+1)
		if s < 0 || e <= s {
			continue
		}
		ri, ps, ok := doc.Locate(s)
		rj, pe, ok2 := doc.Locate(e - 1)
		if !ok || !ok2 || ri != rj {
			continue
		}
		r := group[ri]
		_, n := utf8.DecodeRuneInString(r.Text[pe:])
		pe += n
		if pe > len(r.Text) {
			pe = len(r.Text)
		}
		// Never inside code, inserts, links or tags.
		if coreprose.Masked(r, ps, pe) {
			continue
		}
		f := registry.ProseFinding{Region: index[ri], Start: ps, End: pe, Rule: a.Check,
			Severity: severity(a.Severity), Message: a.Message}
		var repl []string
		// Only a replace with one replacement, and a remove, are
		// deterministic; Vale's spelling suggestions and a replace with
		// several choices are guesses, offered only as code actions.
		guess := true
		switch a.Action.Name {
		case "replace":
			repl = a.Action.Params
			guess = len(repl) != 1
		case "remove":
			start, end := ps, pe
			if start > 0 && r.Text[start-1] == ' ' {
				start--
			}
			f.Fixes = append(f.Fixes, registry.ProseFix{Title: "Remove " + r.Text[ps:pe], Start: start, End: end})
		case "":
		default:
			if fixes < 25 {
				fixes++
				repl = valeFix(bin, cfg, a)
			}
		}
		for i, x := range repl {
			if i == 5 {
				break
			}
			f.Fixes = append(f.Fixes, registry.ProseFix{Title: "Replace with " + x, Start: ps, End: pe, New: x, Suggestion: guess})
		}
		out = append(out, f)
	}
	return out
}

func severity(s string) diag.Severity {
	switch s {
	case "error":
		return diag.Error
	case "warning":
		return diag.Warning
	}
	return diag.Hint
}

// vale runs `vale --output=JSON` on an HTML document given on stdin.
func vale(bin string, cfg *ValeConfig, text string) ([]valeAlert, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--output=JSON", "--ext=.html", "--no-exit", "--config="+cfg.Config)
	cmd.Dir = filepath.Dir(cfg.Config)
	cmd.Stdin = strings.NewReader(text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		return nil, fmt.Errorf("%v: %s", err, firstLine(msg))
	}
	var res map[string][]valeAlert
	if b := bytes.TrimSpace(stdout.Bytes()); len(b) > 0 {
		if err := json.Unmarshal(b, &res); err != nil {
			return nil, fmt.Errorf("reading its JSON: %v", err)
		}
	}
	var out []valeAlert
	for _, as := range res {
		out = append(out, as...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Span[0] < out[j].Span[0]
	})
	return out, nil
}

// valeFix asks `vale fix` for an alert's suggestions, as vale-ls does.
func valeFix(bin string, cfg *ValeConfig, a valeAlert) []string {
	b, err := json.Marshal(a)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "fix", string(b), "--config="+cfg.Config)
	cmd.Dir = filepath.Dir(cfg.Config)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var res struct {
		Suggestions []string `json:"suggestions"`
	}
	if json.Unmarshal(out, &res) != nil {
		return nil
	}
	return res.Suggestions
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}
