package format_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/fixtures"
	"github.com/aiviarium/heron/internal/core/format"
)

func heronFixtures(t *testing.T) []fixtures.Block {
	bs, err := fixtures.Load(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var out []fixtures.Block
	for _, b := range bs {
		if b.IsHeron() {
			out = append(out, b)
		}
	}
	return out
}

// TestFixturesFormatToThemselves: the design's examples are already in
// canonical form.
func TestFixturesFormatToThemselves(t *testing.T) {
	n := 0
	bs := heronFixtures(t)
	for _, b := range bs {
		got := format.Source(b.Name(), b.Content)
		if got != b.Content {
			t.Errorf("%s does not format to itself:\n%s", b.File, diff(b.Content, got))
			continue
		}
		n++
	}
	t.Logf("%d/%d fixtures format to themselves", n, len(bs))
}

// TestIdempotent: formatting a formatted file changes nothing, also for
// files whose layout was scrambled first.
func TestIdempotent(t *testing.T) {
	for _, b := range heronFixtures(t) {
		for _, src := range []string{b.Content, scramble(b.Content)} {
			once := format.Source(b.Name(), src)
			twice := format.Source(b.Name(), once)
			if once != twice {
				t.Errorf("%s: not idempotent:\n%s", b.File, diff(once, twice))
			}
		}
	}
}

// scramble changes every line's indentation and adds trailing spaces,
// which formatting must undo or at least settle.
func scramble(src string) string {
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		lines[i] = strings.Repeat(" ", i%5) + strings.TrimLeft(l, " ") + strings.Repeat(" ", i%2)
	}
	return strings.Join(lines, "\n")
}

func TestLayout(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a: b {\nc = 1   \n\n\n   d = 2\n}", "a: b {\n  c = 1\n\n  d = 2\n}\n"},
		{"x = f(\n1,\n    2,\n)\n", "x = f(\n  1,\n  2,\n)\n"},
		{"x =\n| one\n    | two\ny = 1", "x =\n  | one\n  | two\ny = 1\n"},
		{"s: section {{\n      Text\n        indented\n  }}", "s: section {{\n  Text\n    indented\n}}\n"},
		{"t: task do {\nif a {\nb()\n} else {\nc()\n}\n}", "t: task do {\n  if a {\n    b()\n  } else {\n    c()\n  }\n}\n"},
		{"\n\n/// Doc\n  a: b\n\n\n", "/// Doc\na: b\n"},
	}
	for _, c := range cases {
		if got := format.Source("t.heron", c.in); got != c.want {
			t.Errorf("%q\n got %q\nwant %q", c.in, got, c.want)
		}
	}
}

func diff(a, b string) string {
	al, bl := strings.Split(a, "\n"), strings.Split(b, "\n")
	var sb strings.Builder
	for i := 0; i < len(al) || i < len(bl); i++ {
		var x, y string
		if i < len(al) {
			x = al[i]
		}
		if i < len(bl) {
			y = bl[i]
		}
		if x != y {
			fmt.Fprintf(&sb, "  line %d:\n    - %q\n    + %q\n", i+1, x, y)
		}
	}
	return sb.String()
}
