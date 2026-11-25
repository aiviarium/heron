package syntax_test

import (
	"testing"

	"github.com/helightdev/specs-v0/internal/core/syntax"
)

// TestFixturePrefixes: every prefix of every Heron fixture re-parses
// losslessly, which exercises error recovery on half-written files.
func TestFixturePrefixes(t *testing.T) {
	n := 0
	for _, b := range loadFixtures(t) {
		src := b.Content
		for i := 0; i <= len(src); i++ {
			pre := src[:i]
			tr := syntax.Parse(b.File, pre)
			if got := tr.Root.Text(); got != pre {
				t.Fatalf("%s: prefix of %d bytes is not lossless:\n got %q\nwant %q", b.File, i, got, pre)
			}
			checkSpans(t, tr)
			n++
		}
	}
	t.Logf("%d prefixes", n)
}

// checkSpans verifies that tokens are contiguous and nodes span them.
func checkSpans(t *testing.T, tr *syntax.Tree) {
	t.Helper()
	pos := 0
	tr.Root.Tokens(func(tok *syntax.Token) {
		if tok.Start != pos {
			t.Fatalf("%s: token %s at %d, want %d", tr.Name, tok.Kind, tok.Start, pos)
		}
		if tr.Src[tok.Start:tok.End()] != tok.Text {
			t.Fatalf("%s: token text differs from source at %d", tr.Name, pos)
		}
		pos = tok.End()
	})
	if s, e := tr.Root.Span(); s != 0 || e != len(tr.Src) {
		t.Fatalf("%s: root spans %d..%d of %d", tr.Name, s, e, len(tr.Src))
	}
}

func FuzzParse(f *testing.F) {
	for _, b := range loadFixtures(f) {
		f.Add(b.File, b.Content)
	}
	f.Add("x.heron", "a: b<c>(d = [1, {e = |f\n}]) {{ $g(h) <i j={k}>l</i> }}")
	f.Fuzz(func(t *testing.T, name, src string) {
		if len(name) > 0 && name[len(name)-1] == 'd' {
			name = "f.heron.md"
		} else {
			name = "f.heron"
		}
		tr := syntax.Parse(name, src)
		if got := tr.Root.Text(); got != src {
			t.Fatalf("not lossless:\n got %q\nwant %q", got, src)
		}
		checkSpans(t, tr)
	})
}
