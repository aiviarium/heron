package diag

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSortAndFormat(t *testing.T) {
	l := List{
		{File: "b.heron", Start: 1, Severity: Warning, Code: "x", Message: "two"},
		{File: "a.heron", Start: 9, Severity: Error, Code: "x", Message: "one"},
		{File: "a.heron", Start: 2, Severity: Hint, Code: "x", Message: "zero"},
		{File: "b.heron", Start: 1, Severity: Warning, Code: "x", Message: "two"},
	}
	s := l.Sort()
	if len(s) != 3 || s[0].Message != "zero" || s[1].Message != "one" || s[2].Message != "two" {
		t.Errorf("sorted %+v", s)
	}
	if !l.HasErrors() || s[2:].HasErrors() {
		t.Error("HasErrors")
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range []string{`"severity":"hint"`, `"severity":"error"`, `"severity":"warning"`} {
		if !strings.Contains(string(b), w) {
			t.Errorf("json has no %s: %s", w, b)
		}
	}
	d := Diagnostic{File: "a.heron", Start: 3, Severity: Error, Code: "conflict", Message: "set twice",
		Related: []Related{{File: "b.heron", Start: 5, Message: "set here"}}}
	pos := func(file string, off int) (int, int) { return 1, off + 1 }
	if got, want := Format(d, pos), "a.heron:1:4: error: set twice [conflict]\n\tb.heron:1:6: set here"; got != want {
		t.Errorf("Format = %q, want %q", got, want)
	}
}
