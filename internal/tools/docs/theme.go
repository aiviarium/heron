package docs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aiviarium/heron/web/tokens"
)

// ThemeManifest is the file a theme package keeps at its root,
// heron-theme.json. Paths are relative to the package's folder.
type ThemeManifest struct {
	// Tokens is a token file in web/tokens' format, holding the tokens the
	// theme changes: colours with their dark and light values, and any
	// other group's values.
	Tokens string `json:"tokens,omitempty"`
	// Fonts are the font files the theme ships, declared as @font-face.
	Fonts []ThemeFont `json:"fonts,omitempty"`
	// Logo replaces the mark in the top bar.
	Logo string `json:"logo,omitempty"`
	// CSS are stylesheets, in order. Their url()s resolve against the
	// package's folder.
	CSS []string `json:"css,omitempty"`
}

// ThemeFont is one font face of a theme.
type ThemeFont struct {
	Family string `json:"family"`
	Src    string `json:"src"`
	Weight string `json:"weight,omitempty"`
	Style  string `json:"style,omitempty"`
}

// ThemeFile is the name of a theme package's manifest.
const ThemeFile = "heron-theme.json"

// Theme is a loaded theme package: its stylesheet, in the `theme` layer
// after the built-in theme, and the files the stylesheet and the top bar
// use, served under the assets prefix's theme/ folder.
type Theme struct {
	Spec    string
	Dir     string
	Name    string
	Version string
	CSS     []byte
	// Logo is the logo's path under the assets prefix, or "".
	Logo  string
	Files []Asset
}

// LoadTheme finds a theme package and reads it. spec is "npm:<name>@<major>",
// found in a node_modules folder at or above dir, or a folder relative to
// dir.
func LoadTheme(spec, dir string) (*Theme, error) {
	pkgDir, err := findTheme(spec, dir)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(pkgDir, ThemeFile))
	if err != nil {
		return nil, fmt.Errorf("theme %s: %s has no %s", spec, pkgDir, ThemeFile)
	}
	var m ThemeManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("theme %s: %s: %v", spec, ThemeFile, err)
	}
	th := &Theme{Spec: spec, Dir: pkgDir}
	var pj struct{ Name, Version string }
	if b, err := os.ReadFile(filepath.Join(pkgDir, "package.json")); err == nil {
		_ = json.Unmarshal(b, &pj)
	}
	th.Name, th.Version = pj.Name, pj.Version
	file := func(rel string) (string, []byte, error) {
		clean := filepath.ToSlash(filepath.Clean(rel))
		if filepath.IsAbs(rel) || clean == ".." || strings.HasPrefix(clean, "../") {
			return "", nil, fmt.Errorf("theme %s: %s is outside the package", spec, rel)
		}
		b, err := os.ReadFile(filepath.Join(pkgDir, clean))
		if err != nil {
			return "", nil, fmt.Errorf("theme %s: %v", spec, err)
		}
		return clean, b, nil
	}
	var css strings.Builder
	name := th.Name
	if name == "" {
		name = spec
	}
	fmt.Fprintf(&css, "/* The theme %s %s, in the theme layer after the built-in theme. */\n@layer heron, theme;\n@layer theme {\n", name, th.Version)
	for _, f := range m.Fonts {
		p, b, err := file(f.Src)
		if err != nil {
			return nil, err
		}
		th.Files = append(th.Files, Asset{Path: "theme/" + p, Content: b, Type: mimeOf(p)})
		w, st := f.Weight, f.Style
		if w == "" {
			w = "400"
		}
		if st == "" {
			st = "normal"
		}
		fmt.Fprintf(&css, "@font-face { font-family: %q; src: url(%q) format(%q); font-weight: %s; font-style: %s; font-display: swap; }\n", f.Family, p, fontFormat(p), w, st)
	}
	if m.Tokens != "" {
		_, b, err := file(m.Tokens)
		if err != nil {
			return nil, err
		}
		set, err := tokens.Parse(b)
		if err != nil {
			return nil, fmt.Errorf("theme %s: %s: %v", spec, m.Tokens, err)
		}
		css.WriteString(themeTokensCSS(set))
	}
	for _, c := range m.CSS {
		_, b, err := file(c)
		if err != nil {
			return nil, err
		}
		css.Write(b)
		css.WriteString("\n")
	}
	css.WriteString("}\n")
	th.CSS = []byte(css.String())
	if m.Logo != "" {
		p, b, err := file(m.Logo)
		if err != nil {
			return nil, err
		}
		th.Files = append(th.Files, Asset{Path: "theme/" + p, Content: b, Type: mimeOf(p)})
		th.Logo = "theme/" + p
	}
	th.Files = append([]Asset{{Path: "theme/theme.css", Content: th.CSS, Type: mimeOf("theme.css")}}, th.Files...)
	return th, nil
}

// themeTokensCSS writes the tokens a theme sets: colours only where the
// theme gives a value for that theme, so a colour set for dark alone keeps
// the built-in light value.
func themeTokensCSS(s *tokens.Set) string {
	block := func(sel string, light, values bool) string {
		var lines []string
		for _, c := range s.Colors {
			v := c.Dark
			if light {
				v = c.Light
			}
			if c.Alias != "" {
				v = "var(" + tokens.Var("color", c.Alias) + ")"
			}
			if v != "" {
				lines = append(lines, "  "+tokens.Var("color", c.Name)+": "+v+";")
			}
		}
		if values {
			for _, v := range s.Values {
				lines = append(lines, "  "+tokens.Var(v.Group, v.Name)+": "+v.Value+";")
			}
		}
		if len(lines) == 0 {
			return ""
		}
		return sel + " {\n" + strings.Join(lines, "\n") + "\n}\n"
	}
	out := block(":root,\n[data-theme=\"dark\"]", false, true) + block(`[data-theme="light"]`, true, false)
	if l := block(`:root:not([data-theme="dark"])`, true, false); l != "" {
		out += "@media (prefers-color-scheme: light) {\n" + l + "}\n"
	}
	return out
}

func fontFormat(p string) string {
	switch filepath.Ext(p) {
	case ".woff2":
		return "woff2"
	case ".woff":
		return "woff"
	case ".otf":
		return "opentype"
	}
	return "truetype"
}

// findTheme returns a theme package's folder.
func findTheme(spec, dir string) (string, error) {
	if rest, ok := strings.CutPrefix(spec, "npm:"); ok {
		name, major := rest, ""
		if i := strings.LastIndex(rest, "@"); i > 0 {
			name, major = rest[:i], rest[i+1:]
		}
		for d := dir; ; d = filepath.Dir(d) {
			p := filepath.Join(d, "node_modules", filepath.FromSlash(name))
			if b, err := os.ReadFile(filepath.Join(p, "package.json")); err == nil {
				var pj struct{ Version string }
				_ = json.Unmarshal(b, &pj)
				if major != "" && strings.SplitN(pj.Version, ".", 2)[0] != major {
					return "", fmt.Errorf("theme %s: node_modules has %s@%s", spec, name, pj.Version)
				}
				return p, nil
			}
			if d == filepath.Dir(d) {
				break
			}
		}
		return "", fmt.Errorf("theme %s: %s isn't installed in node_modules above %s", spec, name, dir)
	}
	p := spec
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		return "", fmt.Errorf("theme %s: no folder %s", spec, p)
	}
	return p, nil
}
