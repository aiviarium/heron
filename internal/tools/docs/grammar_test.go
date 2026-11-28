package docs

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/resolve"
)

// ungram reads grammar/heron.ungram: its token kinds, and each
// production's expression with its layout collapsed.
func ungram(t *testing.T, path string) (tokens map[string]bool, prods map[string]string) {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tokens, prods = map[string]bool{}, map[string]string{}
	head := regexp.MustCompile(`^([A-Z][A-Za-z0-9]*)\s*=\s*(.*)$`)
	last := ""
	for _, line := range strings.Split(string(src), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		switch {
		case strings.TrimSpace(line) == "":
			last = ""
		case strings.HasPrefix(line, "token "):
			for _, f := range strings.Fields(line)[1:] {
				tokens[f] = true
			}
			last = ""
		case head.MatchString(line):
			m := head.FindStringSubmatch(line)
			last = m[1]
			prods[last] = m[2]
		case last != "" && (line[0] == ' ' || line[0] == '\t'):
			prods[last] += " " + strings.TrimSpace(line)
		default:
			t.Fatalf("%s: can't read %q", path, line)
		}
	}
	for k, v := range prods {
		prods[k] = strings.Join(strings.Fields(v), " ")
	}
	return tokens, prods
}

// The tree productions and tokens of spec/language/grammar stay equal to
// grammar/heron.ungram, as rules ungram_is_the_spec and grammar_per_concern
// state.
func TestSpecGrammarIsUngram(t *testing.T) {
	t.Parallel() // a session and site of its own; the slow tests run at once
	const source = "grammar/heron.ungram"
	s := site(t, entries["spec"])
	wantTokens, wantProds := ungram(t, "../../../"+source)
	gotTokens, gotProds := map[string]bool{}, map[string]string{}
	grammars := 0
	s.Tree.Walk(func(n *resolve.Node) {
		if s.kindPath(n) != "grammar.grammar" {
			return
		}
		if src, _ := argString(kindArgs(n), -1, "source"); src != source {
			return
		}
		grammars++
		for _, ch := range n.Children {
			switch s.kindPath(ch) {
			case "grammar.token":
				gotTokens[ch.Name] = true
			case "grammar.production":
				if _, dup := gotProds[ch.Name]; dup {
					t.Errorf("production %s is in the spec twice", ch.Name)
				}
				gotProds[ch.Name] = strings.Join(strings.Fields(verbatim(ch)), " ")
			}
		}
	})
	if grammars == 0 {
		t.Fatalf("no grammar in spec/ names %s as its source", source)
	}
	for _, name := range sortedKeys(wantTokens) {
		if !gotTokens[name] {
			t.Errorf("token %s is in %s but not in the spec", name, source)
		}
	}
	for _, name := range sortedKeys(gotTokens) {
		if !wantTokens[name] {
			t.Errorf("token %s is in the spec but not in %s", name, source)
		}
	}
	for _, name := range sortedKeys(wantProds) {
		got, ok := gotProds[name]
		switch {
		case !ok:
			t.Errorf("production %s is in %s but not in the spec", name, source)
		case got != wantProds[name]:
			t.Errorf("production %s differs:\n spec %s\n file %s", name, got, wantProds[name])
		}
	}
	for _, name := range sortedKeys(gotProds) {
		if _, ok := wantProds[name]; !ok {
			t.Errorf("production %s is in the spec but not in %s", name, source)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// A diagnostic renders as a card with its code, severity and messages, and
// the diagnostics index lists it by code with a link to its section.
func TestDiagnosticsRender(t *testing.T) {
	t.Parallel() // a session and site of its own; the slow tests run at once
	s := site(t, entries["spec"])
	n := s.Tree.Find("heron.language.core_format.conflicts.errors.declared_twice")
	if n == nil {
		t.Fatal("no declared_twice diagnostic")
	}
	f, err := s.RenderNode(n)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<code class="diag-code">conflict</code>`, `sev-error`, `<span class="ph">‹name›</span> is declared twice`, `first declared here`, `checker`} {
		if !strings.Contains(f.HTML, want) {
			t.Fatalf("diagnostic card lacks %q:\n%s", want, f.HTML)
		}
	}
	p := s.PageByFile("_diagnostics.html")
	if p == nil {
		t.Fatal("no diagnostics index")
	}
	f, err = s.PageFragment(p)
	if err != nil || !strings.Contains(f.HTML, `id="code-conflict"`) || !strings.Contains(f.HTML, "language.core_format.conflicts.html") {
		t.Fatalf("diagnostics index: %v\n%.2000s", err, f.HTML)
	}
}

// In a table, `\|` is a pipe in its cell, inside a code span too, and
// never splits the row.
func TestTableEscapedPipe(t *testing.T) {
	s := site(t, entries["kinds"])
	f, err := s.RenderNode(s.Tree.Find("tables"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.HTML, `\|`) || !strings.Contains(f.HTML, `<code>T | null</code></td><td>the value or null</td>`) || !strings.Contains(f.HTML, `<td>a | b</td>`) {
		t.Fatalf("escaped pipes:\n%s", f.HTML)
	}
}
