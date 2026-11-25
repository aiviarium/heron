package tokens

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestGeneratedFilesAreCurrent(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := s.GenerateCSS(); got != CSS() {
		t.Error("tokens.css is stale: run go generate ./web/tokens")
	}
	ts, err := os.ReadFile("src/index.ts")
	if err != nil {
		t.Fatal(err)
	}
	if string(ts) != s.GenerateTS() {
		t.Error("src/index.ts is stale: run go generate ./web/tokens")
	}
}

// Every readable text level reaches 4.5:1 on every surface, and project,
// status and kind colours, shown only as text or a 1 px line, reach it on
// the page, raised and soft surfaces, in both themes.
func TestContrast(t *testing.T) {
	s, _ := Load()
	for _, theme := range []string{"dark", "light"} {
		var surfaces []Color
		for _, c := range s.Colors {
			if c.Role == "surface" && c.Alias == "" {
				surfaces = append(surfaces, c)
			}
		}
		if len(surfaces) != 4 {
			t.Fatalf("want four surfaces (page, raised, soft, highlight), have %d", len(surfaces))
		}
		for _, c := range s.Colors {
			if c.Role != "text" && c.Role != "mark" {
				continue
			}
			for _, bg := range surfaces {
				if c.Role == "mark" && bg.Name == "bg-highlight" {
					continue
				}
				fg, bgv := pick(c, theme), pick(bg, theme)
				r := ratio(t, fg, bgv)
				if r < 4.5 {
					t.Errorf("%s: %s %s on %s %s is %.2f:1, below 4.5:1", theme, c.Name, fg, bg.Name, bgv, r)
				}
			}
		}
	}
}

// Surfaces are neutral: no tint in either theme.
func TestSurfacesUntinted(t *testing.T) {
	s, _ := Load()
	for _, c := range s.Colors {
		if c.Role != "surface" && c.Role != "text" && !strings.HasPrefix(c.Name, "border") && !strings.HasPrefix(c.Name, "bg-") {
			continue
		}
		for _, v := range []string{c.Dark, c.Light} {
			r, g, b, ok := parse(v)
			if ok && (r != g || g != b) {
				t.Errorf("%s %s is tinted", c.Name, v)
			}
		}
	}
}

func TestScales(t *testing.T) {
	s, _ := Load()
	want := map[string]string{
		"size.control-sm": "24px", "size.control": "28px", "size.control-lg": "32px", "size.row": "28px",
		"radius.mark": "4px", "radius.control": "6px", "radius.card": "8px", "radius.overlay": "10px",
		"font.size-ui": "13px", "font.size-prose": "15px", "font.size-code": "12.5px", "font.size-label": "11px",
	}
	have := map[string]string{}
	for _, v := range s.Values {
		have[v.Group+"."+v.Name] = v.Value
		switch v.Group {
		case "space":
			if px(t, v.Value)%4 != 0 {
				t.Errorf("space %s = %s is off the 4 px grid", v.Name, v.Value)
			}
		case "motion":
			if strings.HasSuffix(v.Value, "ms") {
				ms, _ := strconv.Atoi(strings.TrimSuffix(v.Value, "ms"))
				if ms < 120 || ms > 200 {
					t.Errorf("motion %s = %s is outside 120–200 ms", v.Name, v.Value)
				}
			}
		}
	}
	for k, v := range want {
		if have[k] != v {
			t.Errorf("%s = %q, want %q", k, have[k], v)
		}
	}
	if tb := px(t, have["size.topbar"]); tb < 38 || tb > 48 {
		t.Errorf("top bar %d px is outside 38–48", tb)
	}
}

func px(t *testing.T, v string) int {
	n, err := strconv.Atoi(strings.TrimSuffix(v, "px"))
	if err != nil {
		t.Fatalf("%q is not a px value", v)
	}
	return n
}

func pick(c Color, theme string) string {
	if theme == "light" {
		return c.Light
	}
	return c.Dark
}

func parse(v string) (r, g, b int, ok bool) {
	if len(v) != 7 || v[0] != '#' {
		return 0, 0, 0, false
	}
	n, err := strconv.ParseUint(v[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(n >> 16 & 255), int(n >> 8 & 255), int(n & 255), true
}

func luminance(t *testing.T, v string) float64 {
	r, g, b, ok := parse(v)
	if !ok {
		t.Fatalf("%q is not #rrggbb", v)
	}
	lin := func(c int) float64 {
		x := float64(c) / 255
		if x <= 0.04045 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

func ratio(t *testing.T, a, b string) float64 {
	la, lb := luminance(t, a), luminance(t, b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
