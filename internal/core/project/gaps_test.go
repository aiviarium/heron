package project

import (
	"reflect"
	"testing"
)

// A gap's entries group by their area, the first two names of their
// path, in the order the areas first appear.
func TestGapGroups(t *testing.T) {
	got := GapGroups([]string{"tools.lsp.a", "language.grammar.b", "tools.lsp.steps.c", "top", "language.grammar.d"})
	want := []GapGroup{
		{"tools.lsp", []string{"tools.lsp.a", "tools.lsp.steps.c"}},
		{"language.grammar", []string{"language.grammar.b", "language.grammar.d"}},
		{"top", []string{"top"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}
