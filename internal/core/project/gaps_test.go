package project

import (
	"reflect"
	"testing"
)

// A gap's entries group by the innermost feature each stands in, and an
// entry in no feature by its area, the first two names of its path, in the
// order the groups first appear.
func TestGapGroups(t *testing.T) {
	got := GapGroups([]string{"tools.lsp.a", "language.grammar.b", "tools.lsp.steps.c", "top", "language.grammar.recovery.d", "language.grammar.e"}, map[string]string{"language.grammar.recovery.d": "language.grammar.recovery"})
	want := []GapGroup{
		{"tools.lsp", []string{"tools.lsp.a", "tools.lsp.steps.c"}},
		{"language.grammar", []string{"language.grammar.b", "language.grammar.e"}},
		{"top", []string{"top"}},
		{"language.grammar.recovery", []string{"language.grammar.recovery.d"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
}
