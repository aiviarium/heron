// Package textmate builds the published TextMate grammars of Heron files
// and prose files from the editor's static grammar's token kinds, which
// grammar/textmate/scopes.json maps to TextMate's standard scope names. The
// files are written by `go run ./internal/tools/textmate/write`, and a test
// keeps them equal to what this package builds.
package textmate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Dir is where the grammars are published, from the repository root.
const Dir = "grammar/textmate"

// Grammars builds the two grammars, by file name, from the scopes of the
// token kinds.
func Grammars(scopes map[string]string) (map[string][]byte, error) {
	sc := func(token string) string {
		s, ok := scopes[token]
		if !ok {
			panic(fmt.Sprintf("textmate: no scope for the token kind %q", token))
		}
		return s
	}
	m := func(re, token string) map[string]any { return map[string]any{"match": re, "name": sc(token)} }
	inc := func(r string) map[string]any { return map[string]any{"include": r} }

	proseInline := []any{
		m(`\\[$<\[|\]`+"`"+`\\*_#]`, "string.escape"),
		m(`\[`+"`"+`?[A-Za-z_]\w*(\.[A-Za-z_]\w*)*`+"`"+`?\](\([^)]*\))?`, "link"),
		m(`\[[^\]]*\]\([^)]*\)`, "link"),
		m("`+[^`]*`+", "string.code"),
		map[string]any{"begin": `\$\{`, "end": `\}`, "name": "meta.interpolation.heron",
			"beginCaptures": map[string]any{"0": map[string]any{"name": sc("delimiter.interp")}},
			"endCaptures":   map[string]any{"0": map[string]any{"name": sc("delimiter.interp")}},
			"patterns":      []any{inc("#structure")}},
		m(`\$[A-Za-z_][\w.]*`, "insert"),
		map[string]any{"begin": `(</?)([A-Za-z_][\w.]*)`, "end": `(/?>)`,
			"beginCaptures": map[string]any{"1": map[string]any{"name": sc("delimiter.tag")}, "2": map[string]any{"name": sc("tag")}},
			"endCaptures":   map[string]any{"1": map[string]any{"name": sc("delimiter.tag")}},
			"patterns": []any{
				m(`@[A-Za-z_]\w*`, "annotation"),
				m(`[A-Za-z_][\w-]*`, "attribute.name"),
				m(`"[^"]*"`, "attribute.value"),
			}},
		m(`\*\*|~~|\*`, "delimiter.markdown"),
	}
	structure := []any{
		m(`^\s*///.*$`, "comment.doc"),
		m(`//.*$`, "comment"),
		map[string]any{"begin": `^\s*(\|)(\s*([-*+]|\d+[.)])\s)?|(=\|)`, "end": `$`, "contentName": sc("string.prose"),
			"beginCaptures": map[string]any{"1": map[string]any{"name": sc("delimiter.prose")}, "3": map[string]any{"name": sc("delimiter.markdown")}, "4": map[string]any{"name": sc("delimiter.prose")}},
			"patterns":      []any{inc("#prose")}},
		map[string]any{"begin": `\{\{`, "end": `\}\}`, "contentName": sc("string.prose"),
			"beginCaptures": map[string]any{"0": map[string]any{"name": sc("delimiter.prose")}},
			"endCaptures":   map[string]any{"0": map[string]any{"name": sc("delimiter.prose")}},
			"patterns":      []any{inc("#prose")}},
		m(`@[A-Za-z_][\w.]*`, "annotation"),
		map[string]any{"begin": `"`, "end": `"`, "name": sc("string"),
			"beginCaptures": map[string]any{"0": map[string]any{"name": sc("string.quote")}},
			"endCaptures":   map[string]any{"0": map[string]any{"name": sc("string.quote")}},
			"patterns": []any{
				m(`\\.`, "string.escape"),
				map[string]any{"begin": `\$\{`, "end": `\}`, "name": "meta.interpolation.heron",
					"beginCaptures": map[string]any{"0": map[string]any{"name": sc("delimiter.interp")}},
					"endCaptures":   map[string]any{"0": map[string]any{"name": sc("delimiter.interp")}},
					"patterns":      []any{inc("#structure")}},
			}},
		m("`[^`]*`", "string.code"),
		m(`\b\d+(\.\d+)?([a-z]+)?\b`, "number"),
		m(`\b(import|include|do|let|if|else|for|in|return|true|false|null|and|or|not|match)\b`, "keyword"),
		m(`\b[A-Z]\w*`, "type"),
		m(`\b[a-z_]\w*`, "identifier"),
		m(`[{}()\[\]<>]`, "delimiter.bracket"),
		m(`[=:,.?|$#]`, "delimiter"),
	}
	heron := map[string]any{
		"$schema":   "https://raw.githubusercontent.com/martinring/tmlanguage/master/tmlanguage.json",
		"name":      "Heron",
		"scopeName": "source.heron",
		"fileTypes": []string{"heron"},
		"patterns":  []any{inc("#structure")},
		"repository": map[string]any{
			"structure": map[string]any{"patterns": structure},
			"prose":     map[string]any{"patterns": proseInline},
		},
	}
	fence := func(lang, scope string) map[string]any {
		r := map[string]any{"begin": `^\s*(` + "```+|~~~+" + `)\s*` + lang + `\s*$`, "end": `^\s*(` + "```+|~~~+" + `)\s*$`,
			"beginCaptures": map[string]any{"0": map[string]any{"name": sc("string.code")}},
			"endCaptures":   map[string]any{"0": map[string]any{"name": sc("string.code")}}}
		if scope != "" {
			r["patterns"] = []any{inc(scope)}
		} else {
			r["contentName"] = sc("string.code")
		}
		return r
	}
	md := map[string]any{
		"$schema":   "https://raw.githubusercontent.com/martinring/tmlanguage/master/tmlanguage.json",
		"name":      "Heron prose",
		"scopeName": "text.heron.markdown",
		"fileTypes": []string{"heron.md"},
		"patterns": []any{
			fence("heron", "source.heron"),
			fence(`[\w.+-]*`, ""),
			map[string]any{"match": `^(#{1,6})(\s.*?)(\{#[A-Za-z_]\w*\})?\s*$`, "captures": map[string]any{
				"1": map[string]any{"name": sc("delimiter.markdown")},
				"3": map[string]any{"name": sc("annotation")}}},
			m(`^\s*>`, "delimiter.markdown"),
			m(`^\s*([-*+]|\d+[.)])\s`, "delimiter.markdown"),
			inc("#prose"),
		},
		"repository": map[string]any{
			"prose":     map[string]any{"patterns": proseInline},
			"structure": map[string]any{"patterns": []any{inc("source.heron#structure")}},
		},
	}
	out := map[string][]byte{}
	for name, g := range map[string]any{"heron.tmLanguage.json": heron, "heron-md.tmLanguage.json": md} {
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(g); err != nil {
			return nil, err
		}
		out[name] = b.Bytes()
	}
	return out, nil
}

// Scopes reads the mapping of token kinds to scopes under root.
func Scopes(root string) (map[string]string, error) {
	b, err := os.ReadFile(filepath.Join(root, Dir, "scopes.json"))
	if err != nil {
		return nil, err
	}
	var sc map[string]string
	return sc, json.Unmarshal(b, &sc)
}
