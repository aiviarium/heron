// Package tokens holds Heron's one set of design tokens, declared once in
// tokens.json, and the CSS and TS generated from them. The app, the editors
// and the docs theme all read the same variables, so they look like one
// product. Go embeds the generated CSS through CSS.
//
// Naming contract: every token is the CSS variable --h-<group>-<name>
// (colours drop the group: --h-bg-page, --h-text-faint, --h-accent), and
// the TS export `tokens` holds the same names without the prefix.
//
//go:generate go run ./gen
package tokens

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

//go:embed tokens.json
var source []byte

//go:embed tokens.css
var css string

// CSS returns the generated stylesheet: the variables for both themes.
func CSS() string { return css }

// Source returns tokens.json.
func Source() []byte { return source }

// Color is a colour token with a value per theme. Role says how the
// contrast test checks it: "surface" is a background, "text" must reach
// 4.5:1 on every surface, "mark" (project, status and kind colours shown
// as text) must reach 4.5:1 on the page, raised and soft surfaces, and
// "none" isn't read as text.
type Color struct {
	Name        string
	Dark, Light string
	Role        string
	Alias       string // another colour this one is defined as
}

// Value is a theme-independent token.
type Value struct {
	Group, Name, Value string
}

// Set is the parsed token file, in declaration order.
type Set struct {
	Colors []Color
	Values []Value
}

// Var returns the CSS variable of a colour or a group's token.
func Var(group, name string) string {
	if group == "color" {
		return "--h-" + name
	}
	return "--h-" + group + "-" + name
}

// Load parses tokens.json.
func Load() (*Set, error) { return Parse(source) }

// Parse reads a token file, keeping declaration order.
func Parse(data []byte) (*Set, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	s := &Set{}
	if err := expect(dec, json.Delim('{')); err != nil {
		return nil, err
	}
	for dec.More() {
		group, err := key(dec)
		if err != nil {
			return nil, err
		}
		if strings.HasPrefix(group, "$") {
			var skip any
			if err := dec.Decode(&skip); err != nil {
				return nil, err
			}
			continue
		}
		if err := expect(dec, json.Delim('{')); err != nil {
			return nil, err
		}
		for dec.More() {
			name, err := key(dec)
			if err != nil {
				return nil, err
			}
			if group == "color" {
				var c struct{ Dark, Light, Role, Alias string }
				if err := dec.Decode(&c); err != nil {
					return nil, fmt.Errorf("color %s: %w", name, err)
				}
				s.Colors = append(s.Colors, Color{Name: name, Dark: c.Dark, Light: c.Light, Role: c.Role, Alias: c.Alias})
				continue
			}
			var v string
			if err := dec.Decode(&v); err != nil {
				return nil, fmt.Errorf("%s %s: %w", group, name, err)
			}
			s.Values = append(s.Values, Value{Group: group, Name: name, Value: v})
		}
		if err := expect(dec, json.Delim('}')); err != nil {
			return nil, err
		}
	}
	// Resolve aliases.
	byName := map[string]*Color{}
	for i := range s.Colors {
		byName[s.Colors[i].Name] = &s.Colors[i]
	}
	for i := range s.Colors {
		c := &s.Colors[i]
		if c.Alias == "" {
			continue
		}
		t := byName[c.Alias]
		if t == nil || t.Alias != "" {
			return nil, fmt.Errorf("color %s: alias %q is not a base colour", c.Name, c.Alias)
		}
		c.Dark, c.Light, c.Role = t.Dark, t.Light, t.Role
	}
	return s, nil
}

func expect(dec *json.Decoder, want json.Token) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if t != want {
		return fmt.Errorf("tokens: expected %v, found %v", want, t)
	}
	return nil
}

func key(dec *json.Decoder) (string, error) {
	t, err := dec.Token()
	if err != nil {
		return "", err
	}
	k, ok := t.(string)
	if !ok {
		return "", fmt.Errorf("tokens: expected a key, found %v", t)
	}
	return k, nil
}

const header = "Generated from tokens.json by `go generate ./web/tokens`. Do not edit."

// GenerateCSS writes the stylesheet: dark on :root (dark first), light
// under [data-theme=light] and, without an explicit theme, when the system
// prefers light.
func (s *Set) GenerateCSS() string {
	var b strings.Builder
	fmt.Fprintf(&b, "/* %s */\n\n", header)
	colors := func(w io.Writer, indent string, light bool) {
		for _, c := range s.Colors {
			v := c.Dark
			if light {
				v = c.Light
			}
			if c.Alias != "" {
				v = "var(" + Var("color", c.Alias) + ")"
			}
			fmt.Fprintf(w, "%s%s: %s;\n", indent, Var("color", c.Name), v)
		}
	}
	b.WriteString(":root,\n[data-theme=\"dark\"] {\n  color-scheme: dark;\n")
	colors(&b, "  ", false)
	for _, v := range s.Values {
		fmt.Fprintf(&b, "  %s: %s;\n", Var(v.Group, v.Name), v.Value)
	}
	b.WriteString("}\n\n[data-theme=\"light\"] {\n  color-scheme: light;\n")
	colors(&b, "  ", true)
	b.WriteString("}\n\n@media (prefers-color-scheme: light) {\n  :root:not([data-theme=\"dark\"]) {\n    color-scheme: light;\n")
	colors(&b, "    ", true)
	b.WriteString("  }\n}\n")
	return b.String()
}

// GenerateTS writes the TS module: the values per theme, the variable of
// every token, and the token names as types.
func (s *Set) GenerateTS() string {
	var b strings.Builder
	fmt.Fprintf(&b, "// %s\n\n", header)
	q := func(v string) string { j, _ := json.Marshal(v); return string(j) }
	b.WriteString("export const colors = {\n")
	for _, c := range s.Colors {
		fmt.Fprintf(&b, "  %s: { dark: %s, light: %s },\n", q(c.Name), q(c.Dark), q(c.Light))
	}
	b.WriteString("} as const;\n\n")
	groups := map[string][]Value{}
	var order []string
	for _, v := range s.Values {
		if groups[v.Group] == nil {
			order = append(order, v.Group)
		}
		groups[v.Group] = append(groups[v.Group], v)
	}
	b.WriteString("export const values = {\n")
	for _, g := range order {
		fmt.Fprintf(&b, "  %s: {\n", q(g))
		for _, v := range groups[g] {
			fmt.Fprintf(&b, "    %s: %s,\n", q(v.Name), q(v.Value))
		}
		b.WriteString("  },\n")
	}
	b.WriteString("} as const;\n\n")
	b.WriteString("export type ColorToken = keyof typeof colors;\nexport type Theme = \"dark\" | \"light\";\n\n")
	b.WriteString("/** The CSS variable of a colour, such as cssVar(\"accent\") → \"var(--h-accent)\". */\n")
	b.WriteString("export function cssVar(name: ColorToken): string {\n  return `var(--h-${name})`;\n}\n\n")
	b.WriteString("/** The CSS variable of a non-colour token, such as valueVar(\"radius\", \"card\"). */\n")
	b.WriteString("export function valueVar<G extends keyof typeof values>(group: G, name: keyof (typeof values)[G] & string): string {\n  return `var(--h-${group}-${name})`;\n}\n")
	return b.String()
}
