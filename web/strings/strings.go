// Package uistrings is the catalogue of every string Heron's tools show
// that doesn't come from a spec: labels, headings, buttons and the words
// the renderer joins requirements with. The docs renderer, the app and the
// editors read the same strings.json, so the interface text is checked in
// one place (the prose lints, the Vale style for interface text, and the
// docs test that fails on visible text from anywhere else).
package uistrings

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

//go:embed strings.json
var source []byte

var catalogue = func() map[string]string {
	var raw map[string]any
	if err := json.Unmarshal(source, &raw); err != nil {
		panic(fmt.Sprintf("uistrings: strings.json: %v", err))
	}
	out := map[string]string{}
	var flat func(prefix string, v any)
	flat = func(prefix string, v any) {
		switch v := v.(type) {
		case string:
			out[prefix] = v
		case map[string]any:
			for k, x := range v {
				if strings.HasPrefix(k, "$") {
					continue
				}
				p := k
				if prefix != "" {
					p = prefix + "." + k
				}
				flat(p, x)
			}
		}
	}
	flat("", raw)
	return out
}()

// Get returns the string of a key, such as "docs.nav.glossary". A missing
// key panics: every string the tools show is in the catalogue.
func Get(key string) string {
	s, ok := catalogue[key]
	if !ok {
		panic("uistrings: no string for " + key)
	}
	return s
}

// Format returns a string with {name} placeholders filled.
func Format(key string, args ...string) string {
	s := Get(key)
	for i := 0; i+1 < len(args); i += 2 {
		s = strings.ReplaceAll(s, "{"+args[i]+"}", args[i+1])
	}
	return s
}

// Has reports whether a key exists.
func Has(key string) bool { _, ok := catalogue[key]; return ok }

// All returns every key and string, for the tests that check visible text.
func All() map[string]string {
	out := make(map[string]string, len(catalogue))
	for k, v := range catalogue {
		out[k] = v
	}
	return out
}

// Keys returns every key, sorted.
func Keys() []string {
	out := make([]string, 0, len(catalogue))
	for k := range catalogue {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Source returns strings.json, as the TS side reads it.
func Source() []byte { return source }
