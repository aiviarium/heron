package syntax

import (
	"os"
	"testing"
)

// TestDump prints the tree of $HERON_DUMP, for debugging.
func TestDump(t *testing.T) {
	f := os.Getenv("HERON_DUMP")
	if f == "" {
		t.Skip("set HERON_DUMP to a file")
	}
	src, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := Parse(f, string(src))
	t.Log("\n" + Dump(tr.Root))
	for _, d := range tr.Diags {
		l, c := LineCol(tr.Src, d.Start)
		t.Logf("%d:%d: %s", l, c, d.Message)
	}
}
