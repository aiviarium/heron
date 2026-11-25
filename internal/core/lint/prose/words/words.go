// Package words is a sample prose lint provider: it flags configured words
// in the prose view and offers each word's replacement as a fix. It proves
// the prose provider extension point.
package words

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/helightdev/specs-v0/internal/core/diag"
	"github.com/helightdev/specs-v0/internal/core/lint/prose"
	"github.com/helightdev/specs-v0/internal/core/registry"
)

// Provider returns a provider named "Words" that flags each word of the
// map, ignoring case, and offers its value as a replacement; an empty
// value offers to remove the word. Findings use the rule "Flagged".
func Provider(words map[string]string) registry.ProseProvider {
	lower := map[string]string{}
	for w, r := range words {
		lower[strings.ToLower(w)] = r
	}
	return registry.ProseProvider{
		Name:    "Words",
		Summary: "flags configured words in prose",
		Check: func(regions []*registry.ProseRegion) []registry.ProseFinding {
			var out []registry.ProseFinding
			for i, r := range regions {
				if r.Block == "code" {
					continue
				}
				for _, w := range scan(r.Text) {
					repl, ok := lower[strings.ToLower(r.Text[w[0]:w[1]])]
					if !ok || prose.Masked(r, w[0], w[1]) {
						continue
					}
					f := registry.ProseFinding{Region: i, Start: w[0], End: w[1], Rule: "Flagged", Severity: diag.Warning,
						Message: fmt.Sprintf("avoid %q", r.Text[w[0]:w[1]])}
					if repl == "" {
						// Remove the word with the space before it, or after it
						// at the start of a text.
						start, end := w[0], w[1]
						if start > 0 && r.Text[start-1] == ' ' {
							start--
						} else if end < len(r.Text) && r.Text[end] == ' ' {
							end++
						}
						f.Fixes = []registry.ProseFix{{Title: "remove " + r.Text[w[0]:w[1]], Start: start, End: end}}
					} else {
						f.Fixes = []registry.ProseFix{{Title: "replace with " + repl, Start: w[0], End: w[1], New: repl}}
					}
					out = append(out, f)
				}
			}
			return out
		},
	}
}

// scan returns the byte ranges of the words of a text.
func scan(s string) [][2]int {
	var out [][2]int
	start := -1
	for i := 0; i <= len(s); {
		r, n := rune(0), 1
		if i < len(s) {
			r, n = utf8.DecodeRuneInString(s[i:])
		}
		word := i < len(s) && (unicode.IsLetter(r) || r == '\'' && start >= 0)
		if word && start < 0 {
			start = i
		} else if !word && start >= 0 {
			out = append(out, [2]int{start, i})
			start = -1
		}
		i += n
	}
	return out
}

// Parse reads a configuration such as "simply=,utilize=use": words
// separated by commas, each with an optional replacement after `=`.
func Parse(s string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		w, r, _ := strings.Cut(part, "=")
		out[strings.TrimSpace(w)] = strings.TrimSpace(r)
	}
	return out
}
