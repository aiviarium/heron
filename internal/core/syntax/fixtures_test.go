package syntax_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/fixtures"
	"github.com/aiviarium/heron/internal/core/syntax"
)

func loadFixtures(t testing.TB) []fixtures.Block {
	t.Helper()
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

func fileName(b fixtures.Block) string { return b.Name() }

func diagString(src string, ds []syntax.Diagnostic) string {
	var sb strings.Builder
	for _, d := range ds {
		l, c := syntax.LineCol(src, d.Start)
		fmt.Fprintf(&sb, "  %d:%d: %s\n", l, c, d.Message)
	}
	return sb.String()
}

// TestFixturesParse: every Heron fixture parses losslessly, and fixtures in
// heron and heron-md mode parse without errors.
func TestFixturesParse(t *testing.T) {
	clean := 0
	bs := loadFixtures(t)
	for _, b := range bs {
		tr := syntax.Parse(fileName(b), b.Content)
		if got := tr.Root.Text(); got != b.Content {
			t.Errorf("%s: not lossless", b.File)
			continue
		}
		if b.Mode == fixtures.ModeHeron || b.Mode == fixtures.ModeProse {
			if len(tr.Diags) > 0 {
				t.Errorf("%s: %d errors:\n%s", b.File, len(tr.Diags), diagString(b.Content, tr.Diags))
				continue
			}
		}
		clean++
	}
	t.Logf("%d/%d Heron fixtures parse", clean, len(bs))
}
