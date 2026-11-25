package docs

import (
	"testing"

	"github.com/helightdev/specs-v0/internal/core/resolve"
)

// TestSpecRequirementStatus keeps the spec's implementation status honest:
// a rule nothing verifies is marked <planned>, unless its subject is a
// person, whose behaviour no test can prove; and a rule marked <planned>
// has no verify link, since a test proving it would mean it's built. The
// same holds for a planned diagnostic or component.
//
// untested lists the rules that are built but that no test proves yet. It
// may only shrink: an entry that gains a verify link, turns <planned> or
// goes away fails the test until it's removed.
func TestSpecRequirementStatus(t *testing.T) {
	s := site(t, entries["spec"])
	n := 0
	waiting := map[string]bool{}
	for _, p := range untested {
		waiting[p] = true
	}
	var walk func(x *resolve.Node)
	walk = func(x *resolve.Node) {
		for _, ch := range x.Children {
			walk(ch)
		}
		if !own(x) {
			return
		}
		switch s.kindPath(x) {
		case "tech.diagnostic", "tech.component":
			if hasStatus(x, "planned") && len(s.childrenOfKind(x, "spec.verify")) > 0 {
				t.Errorf("%s is <planned> but has a verify link", x.Path())
			}
			return
		case "spec.rule":
		default:
			return
		}
		n++
		verified := len(s.childrenOfKind(x, "spec.verify")) > 0
		planned := hasStatus(x, "planned")
		if waiting[x.Path()] {
			delete(waiting, x.Path())
			if verified || planned {
				t.Errorf("%s is listed as untested but is verified or <planned>; take it off the list", x.Path())
			}
			return
		}
		switch {
		case planned && verified:
			t.Errorf("%s is <planned> but has a verify link", x.Path())
		case !planned && !verified && !s.personSubject(x):
			t.Errorf("%s has no verify link and isn't <planned>", x.Path())
		}
	}
	walk(s.Tree.Root)
	for p := range waiting {
		t.Errorf("%s is listed as untested but isn't a rule of the spec", p)
	}
	if n == 0 {
		t.Fatal("spec/ has no rules")
	}
}

func hasStatus(n *resolve.Node, flag string) bool {
	for _, f := range kindFlags(n) {
		if f == flag {
			return true
		}
	}
	return false
}

var untested = []string{
	"language.core_format.nodes.locals.locals_never_change",
	"language.core_format.amends.flag_as_boolean",
	"language.core_format.amends.lists_change_by_calls",
	"language.grammar.recovery.checking_goes_on",
	"language.schemas.kinds.refs.ref_kind_documents",
	"language.schemas.accept.verbatim_text",
	"language.schemas.extensions.limits.unsandboxed",
	"language.schemas.extensions.limits.hosts_not_filtered",
	"language.schemas.extensions.limits.trust_first_run",
	"language.schemas.extensions.limits.js_host_unlimited",
	"language.schemas.extensions.lifecycle.start_failure",
	"language.schemas.extensions.lifecycle.builtins_same_contract",
	"language.markup.prose_view.verbatim_docs",
	"language.markup.prose_view.region_context",
	"language.markup.prose_view.no_titles",
}
