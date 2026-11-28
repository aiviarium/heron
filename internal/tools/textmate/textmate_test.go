package textmate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const root = "../../.."

func built(t *testing.T) map[string][]byte {
	t.Helper()
	sc, err := Scopes(root)
	if err != nil {
		t.Fatal(err)
	}
	gs, err := Grammars(sc)
	if err != nil {
		t.Fatal(err)
	}
	return gs
}

// The published grammars are what the generator writes.
func TestPublishedGrammarsAreCurrent(t *testing.T) {
	for name, b := range built(t) {
		have, err := os.ReadFile(filepath.Join(root, Dir, name))
		if err != nil || !bytes.Equal(have, b) {
			t.Errorf("%s is stale: run go run ./internal/tools/textmate/write", name)
		}
	}
}

// Every token kind of the editor's static grammar has a scope, and every
// pattern compiles.
func TestScopesAndPatterns(t *testing.T) {
	sc, _ := Scopes(root)
	src, err := os.ReadFile(filepath.Join(root, "web/editor/src/monaco.ts"))
	if err != nil {
		t.Fatal(err)
	}
	grammar := string(src)
	grammar = grammar[strings.Index(grammar, "const grammar"):strings.Index(grammar, "function hex(")]
	grammar = regexp.MustCompile(`(?m)^\s*keywords:.*$`).ReplaceAllString(grammar, "")
	for _, m := range regexp.MustCompile(`(?:token: |, |\[)"([a-z][\w.]*)"`).FindAllStringSubmatch(grammar, -1) {
		if m[1] == "@pop" || m[1] == "@keywords" || m[1] == "@default" {
			continue
		}
		if _, ok := sc[m[1]]; !ok {
			t.Errorf("the token kind %q has no scope", m[1])
		}
	}
	for name, b := range built(t) {
		var g any
		if err := json.Unmarshal(b, &g); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var walk func(v any)
		walk = func(v any) {
			switch v := v.(type) {
			case map[string]any:
				for _, k := range []string{"match", "begin", "end"} {
					if re, ok := v[k].(string); ok {
						if _, err := regexp.Compile(re); err != nil {
							t.Errorf("%s: %s %q: %v", name, k, re, err)
						}
					}
				}
				for _, x := range v {
					walk(x)
				}
			case []any:
				for _, x := range v {
					walk(x)
				}
			}
		}
		walk(g)
	}
}

// A few lines scope as the editor colours them: a link in prose, a
// keyword, a type and an annotation in structure.
func TestScopedSamples(t *testing.T) {
	var g struct {
		Repository map[string]struct {
			Patterns []map[string]any
		}
	}
	json.Unmarshal(built(t)["heron.tmLanguage.json"], &g)
	find := func(repo, scope, sample string) {
		t.Helper()
		for _, p := range g.Repository[repo].Patterns {
			if p["name"] == scope {
				if re, ok := p["match"].(string); ok && regexp.MustCompile(re).FindString(sample) != "" {
					return
				}
			}
		}
		t.Errorf("no %s pattern of %s matches %q", scope, repo, sample)
	}
	find("prose", "markup.underline.link.heron", "[system.editor] underlines")
	find("prose", "variable.other.insert.heron", "$kite.cli.sync.name")
	find("structure", "keyword.control.heron", "import = [")
	find("structure", "entity.name.type.heron", "Order: type")
	find("structure", "entity.name.function.decorator.heron", "@touch(x)")
}
