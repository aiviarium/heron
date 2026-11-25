package prose

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/lint"
	coreprose "github.com/aiviarium/heron/internal/core/lint/prose"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// SpecName is the spec-aware lints' provider name.
const SpecName = "Spec"

// SpecProvider returns the spec-aware prose lints of std/lint. They run
// on trees that import std/lint and know the tree, which Vale can't: its
// terms, rules, metrics and node names.
func SpecProvider() registry.ProseProvider {
	return registry.ProseProvider{
		Name:    SpecName,
		Summary: "terms, rules and references in prose, checked against the tree",
		Prepare: prepareSpec,
	}
}

// VagueWords are flagged in a rule without a metric.
var VagueWords = []string{"fast", "quick", "quickly", "slow", "appropriate", "appropriately", "reasonable",
	"reasonably", "adequate", "sufficient", "efficient", "efficiently", "easy", "easily", "simple", "user-friendly",
	"robust", "timely", "soon", "promptly", "large", "small", "many", "few", "several", "flexible", "intuitive"}

var weakModals = []string{"should", "may", "might", "could", "ideally"}

type term struct {
	node    *resolve.Node
	path    string
	phrases []string // the name as words, lower case
	aliases []string
}

type specFacts struct {
	terms []*term
	// names maps a node name that reads as an identifier (snake_case or
	// camelCase) to the nodes declaring it.
	names map[string][]*resolve.Node
	// measured holds the rules a metric or quality covers.
	measured map[*resolve.Node]bool
	c        *types.Checker
}

func importsLint(t *resolve.Tree) bool {
	for _, p := range t.Imports {
		if p.Name == "std/lint" {
			return true
		}
	}
	return false
}

func prepareSpec(in any) (string, func([]*registry.ProseRegion) []registry.ProseFinding) {
	li, ok := in.(*lint.Input)
	if !ok || !importsLint(li.Tree) {
		return "", nil
	}
	f := collect(li.Tree, li.Checker)
	reportSenses(li, f)
	reportDeadLinks(li)
	return f.key(), func(regions []*registry.ProseRegion) []registry.ProseFinding {
		var out []registry.ProseFinding
		for i, r := range regions {
			out = append(out, f.region(i, r)...)
		}
		return out
	}
}

func collect(t *resolve.Tree, c *types.Checker) *specFacts {
	f := &specFacts{names: map[string][]*resolve.Node{}, measured: map[*resolve.Node]bool{}, c: c}
	var qualities []*resolve.Node
	t.Walk(func(n *resolve.Node) {
		if n.Shape == resolve.ShapeMounted || n.Decl.File == nil || n.Decl.File.ID.IsStd() {
			return
		}
		k := c.KindOf(n)
		switch {
		case types.IsStd(k, "doc", "doc.term") && n.Name != "":
			tm := &term{node: n, path: n.Path(), phrases: []string{strings.ToLower(strings.ReplaceAll(n.Name, "_", " "))}}
			tm.aliases = aliases(n)
			f.terms = append(f.terms, tm)
		case types.IsStd(k, "spec", "spec.quality"):
			qualities = append(qualities, n)
		case types.IsStd(k, "spec", "spec.metric"):
			for p := n.Parent; p != nil; p = p.Parent {
				f.measured[p] = true
			}
		}
		if n.Name != "" && len(n.Name) >= 4 && identLike(n.Name) {
			f.names[n.Name] = append(f.names[n.Name], n)
		}
	})
	// A quality covers the rules its targets name.
	for _, q := range qualities {
		for p := q.Parent; p != nil; p = p.Parent {
			f.measured[p] = true
		}
		if q.Decl.N == nil {
			continue
		}
		q.Decl.N.Walk(func(m *syntax.Node) bool {
			if m.Kind == syntax.Path {
				if x := c.Refs[m]; x != nil {
					f.measured[x] = true
				}
			}
			return true
		})
	}
	sort.Slice(f.terms, func(i, j int) bool { return f.terms[i].path < f.terms[j].path })
	return f
}

// identLike reports whether a name reads as an identifier rather than a
// word: it has an underscore or an inner capital.
func identLike(s string) bool {
	if strings.Contains(strings.Trim(s, "_"), "_") {
		return true
	}
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// aliases reads a term's `aliases = […]` argument.
func aliases(n *resolve.Node) []string {
	if n.Decl.N == nil || n.Decl.N.Kind != syntax.Declare {
		return nil
	}
	nt, ok := (ast.Declare{N: n.Decl.N}).Type().(ast.NamedType)
	if !ok {
		return nil
	}
	var out []string
	for _, a := range nt.Args().Args() {
		na, ok := a.(ast.NamedArg)
		if !ok || na.Ident() == nil || na.Ident().Text != "aliases" {
			continue
		}
		if arr, ok := na.Expr().(ast.ArrayExpr); ok {
			for _, x := range arr.Exprs() {
				if s, ok := resolve.StringValue(x.Syntax()); ok && strings.TrimSpace(s) != "" {
					out = append(out, strings.ToLower(s))
				}
			}
		}
	}
	return out
}

func (f *specFacts) key() string {
	h := sha256.New()
	for _, t := range f.terms {
		fmt.Fprintf(h, "t%s\x00%v\x00%v\x00", t.path, t.phrases, t.aliases)
	}
	var names []string
	for n, ns := range f.names {
		names = append(names, fmt.Sprintf("%s=%s", n, ns[0].Path()))
	}
	sort.Strings(names)
	fmt.Fprintf(h, "%v\x00", names)
	var measured []string
	for n := range f.measured {
		measured = append(measured, n.Path())
	}
	sort.Strings(measured)
	fmt.Fprintf(h, "%v", measured)
	return hex.EncodeToString(h.Sum(nil))
}

// reportSenses flags one word defined by two terms, at both terms.
func reportSenses(li *lint.Input, f *specFacts) {
	by := map[string][]*term{}
	for _, t := range f.terms {
		seen := map[string]bool{}
		for _, p := range append(append([]string(nil), t.phrases...), t.aliases...) {
			// A term whose alias repeats its own name defines the word once.
			if !seen[p] {
				seen[p] = true
				by[p] = append(by[p], t)
			}
		}
	}
	var words []string
	for w := range by {
		words = append(words, w)
	}
	sort.Strings(words)
	for _, w := range words {
		ts := by[w]
		if len(ts) < 2 {
			continue
		}
		for _, t := range ts {
			site := t.node.Decl
			s, e := site.Span()
			if p := childNode(site.N, syntax.Path); p != nil {
				s, e = p.Span()
			}
			var others []string
			for _, o := range ts {
				if o != t {
					others = append(others, o.path)
				}
			}
			li.As(SpecName+".TwoSenses").ReportFix(t.node, site.File, s, e, diag.Warning,
				fmt.Sprintf("%q is defined by two terms, here and %s, so it's used in two senses", w, strings.Join(others, ", ")), nil)
		}
	}
}

func childNode(n *syntax.Node, k syntax.Kind) *syntax.Node {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c, ok := c.(*syntax.Node); ok && c.Kind == k {
			return c
		}
	}
	return nil
}

type word struct {
	s, e int
	text string
}

func words(s string) []word {
	var out []word
	start := -1
	for i := 0; i <= len(s); {
		r, n := rune(0), 1
		if i < len(s) {
			r, n = utf8.DecodeRuneInString(s[i:])
		}
		in := i < len(s) && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' ||
			(r == '-' || r == '\'') && start >= 0 && i+1 < len(s) && isLetterAt(s, i+1))
		if in && start < 0 {
			start = i
		} else if !in && start >= 0 {
			out = append(out, word{start, i, s[start:i]})
			start = -1
		}
		i += n
	}
	return out
}

func isLetterAt(s string, i int) bool {
	r, _ := utf8.DecodeRuneInString(s[i:])
	return unicode.IsLetter(r)
}

// owners returns the node a region belongs to and its ancestors.
func ownerChain(r *registry.ProseRegion) []*resolve.Node {
	n, _ := r.Owner.(*resolve.Node)
	var out []*resolve.Node
	for ; n != nil; n = n.Parent {
		out = append(out, n)
	}
	return out
}

func (f *specFacts) region(i int, r *registry.ProseRegion) []registry.ProseFinding {
	if r.Block == "code" {
		return nil
	}
	var out []registry.ProseFinding
	chain := ownerChain(r)
	ws := words(r.Text)
	lower := strings.ToLower(r.Text)
	add := func(s, e int, rule, msg string, fix *registry.ProseFix) {
		fd := registry.ProseFinding{Region: i, Start: s, End: e, Rule: rule, Severity: diag.Warning, Message: msg}
		if fix != nil {
			fd.Fixes = []registry.ProseFix{*fix}
		}
		out = append(out, fd)
	}
	var spans [][2]int // ranges already reported
	taken := func(s, e int) bool {
		for _, sp := range spans {
			if s < sp[1] && sp[0] < e {
				return true
			}
		}
		return false
	}
	inTerm := func(t *term) bool {
		for _, n := range chain {
			if n == t.node {
				return true
			}
		}
		return false
	}
	// Terms and their ruled-out aliases.
	if r.Block != "heading" {
		for _, t := range f.terms {
			if inTerm(t) {
				continue
			}
			for _, ph := range t.phrases {
				for _, m := range findPhrase(lower, ws, ph) {
					if taken(m[0], m[1]) || shouting(r.Text[m[0]:m[1]]) || coreprose.Masked(r, m[0], m[1]) {
						continue
					}
					spans = append(spans, m)
					text := r.Text[m[0]:m[1]]
					add(m[0], m[1], "UnlinkedTerm", fmt.Sprintf("%q is the term %s; link it", text, t.path),
						&registry.ProseFix{Title: "Link to " + t.path, Start: m[0], End: m[1], New: fmt.Sprintf("[%s](#%s)", text, t.path)})
				}
			}
			for _, al := range t.aliases {
				for _, m := range findPhrase(lower, ws, al) {
					if taken(m[0], m[1]) || shouting(r.Text[m[0]:m[1]]) || coreprose.Masked(r, m[0], m[1]) {
						continue
					}
					spans = append(spans, m)
					name := strings.ReplaceAll(t.node.Name, "_", " ")
					add(m[0], m[1], "Synonym", fmt.Sprintf("%q is a synonym the term %s rules out; use the term", r.Text[m[0]:m[1]], t.path),
						&registry.ProseFix{Title: "Use the term " + name, Start: m[0], End: m[1], New: fmt.Sprintf("[%s](#%s)", name, t.path)})
				}
			}
		}
	}
	// Rules: the strength's wording, and vague words without a metric.
	var rule *resolve.Node
	strength := ""
	for _, n := range chain {
		k := f.c.KindOf(n)
		switch {
		case types.IsStd(k, "spec", "spec.must") && strength == "":
			strength = "must"
		case types.IsStd(k, "spec", "spec.mustNot") && strength == "":
			strength = "must not"
		case types.IsStd(k, "spec", "spec.rule") && rule == nil:
			rule = n
		}
	}
	if strength != "" {
		for _, w := range ws {
			lw := strings.ToLower(w.text)
			for _, m := range weakModals {
				if lw == m && !coreprose.Masked(r, w.s, w.e) && !quoted(r.Text, w.s, w.e) {
					add(w.s, w.e, "MustSaysShould", fmt.Sprintf("a %s requirement says %q; state it as %s", strength, w.text, strength), nil)
				}
			}
		}
	}
	if rule != nil && !f.measured[rule] {
		for _, w := range ws {
			lw := strings.ToLower(w.text)
			for _, v := range VagueWords {
				if lw == v && !coreprose.Masked(r, w.s, w.e) && !quoted(r.Text, w.s, w.e) {
					add(w.s, w.e, "VagueWord", fmt.Sprintf("%q is vague in a rule without a metric; measure it or say exactly", w.text), nil)
				}
			}
		}
	}
	// Node names written as words.
	for _, w := range ws {
		ns := f.names[w.text]
		if len(ns) == 0 || taken(w.s, w.e) || coreprose.Masked(r, w.s, w.e) {
			continue
		}
		own := false
		for _, n := range chain {
			for _, x := range ns {
				own = own || n == x
			}
		}
		if own {
			continue
		}
		var fix *registry.ProseFix
		if len(ns) == 1 {
			fix = &registry.ProseFix{Title: "Link to " + ns[0].Path(), Start: w.s, End: w.e, New: fmt.Sprintf("[%s](#%s)", w.text, ns[0].Path())}
		}
		add(w.s, w.e, "UnlinkedNode", fmt.Sprintf("%s names a node without linking it", w.text), fix)
	}
	return out
}

// reportDeadLinks flags links to relative files that don't exist. Links
// to nodes are checked by the type checker.
func reportDeadLinks(li *lint.Input) {
	in := li.As(SpecName + ".DeadLink")
	for _, f := range li.Tree.Files {
		if f.ID.IsStd() || !filepath.IsAbs(string(f.ID)) {
			continue
		}
		f.Tree.Root.Walk(func(n *syntax.Node) bool {
			if n.Kind != syntax.Link {
				return true
			}
			for _, c := range n.Children {
				t, ok := c.(*syntax.Token)
				if !ok || t.Kind != syntax.Url {
					continue
				}
				dest, _, _ := strings.Cut(t.Text, "#")
				if !strings.HasPrefix(dest, "./") && !strings.HasPrefix(dest, "../") {
					continue
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(string(f.ID)), filepath.FromSlash(dest))); err != nil {
					s, e := n.Span()
					in.ReportAt(nil, f, s, e, diag.Warning, fmt.Sprintf("the link's target %s doesn't exist", dest))
				}
			}
			return false
		})
	}
}

// findPhrase finds a lower-case phrase of words in a text, on word
// boundaries, also with a plural "s".
func findPhrase(lower string, ws []word, phrase string) [][2]int {
	parts := strings.Fields(phrase)
	if len(parts) == 0 {
		return nil
	}
	var out [][2]int
	for i := 0; i+len(parts) <= len(ws); i++ {
		ok := true
		for k, p := range parts {
			w := lower[ws[i+k].s:ws[i+k].e]
			// The words of a phrase are separated by spaces only, so
			// "type, kind" isn't the phrase "type kind".
			if k > 0 && strings.TrimSpace(lower[ws[i+k-1].e:ws[i+k].s]) != "" {
				ok = false
				break
			}
			if w != p && !(k == len(parts)-1 && (w == p+"s" || w == p+"es")) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, [2]int{ws[i].s, ws[i+len(parts)-1].e})
		}
	}
	return out
}

// quoted reports whether a word stands in double quotes, as in `such as
// "fast"`, so it's mentioned rather than used.
func quoted(text string, s, e int) bool {
	open := strings.HasSuffix(text[:s], `"`) || strings.HasSuffix(text[:s], "“")
	shut := strings.HasPrefix(text[e:], `"`) || strings.HasPrefix(text[e:], "”")
	return open && shut
}

// shouting reports whether a word is all capitals, as an enum variant is
// written, so it names a node rather than the term.
func shouting(s string) bool {
	letters := 0
	for _, r := range s {
		if unicode.IsLower(r) {
			return false
		}
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters > 1
}
