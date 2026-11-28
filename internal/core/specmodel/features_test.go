package specmodel_test

import (
	"testing"

	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/specmodel"
)

// A feature references the goals, qualities and actors it's for; a rule
// joins a feature it references; features nest; and a feature's prose
// linking its own rules is no cycle.
func TestFeatures(t *testing.T) {
	res := checkSrc(t, head+`
shop: goal {{ Shop. }}

fast: quality(measure = "1 s") {{ Fast. }}

customer: actor {{ Buys. }}

desk: part {{ The console. }}

security: feature<security> {{ What keeps payments safe. }}

refunds: feature(shop, fast, customer) {
  | Refunds, as [late] says.

  late: rule(desk, security) {
    if {{ a refund is late }}
    must {{ reject it }}
  }

  gifts: feature {
    gift_limit: rule {
      must {{ refund at most the payment }}
    }
  }
}

bad_ref: feature(desk) {{ A feature for a part. }}
`)
	wantAll(t, res,
		"reference: desk is a part, and a feature references only goal, quality, actor or assumption")
	wantNone(t, res, "cycle:", "late is a rule", "security is a feature")
	c := res.Checker
	late := res.Tree.Find("refunds.late")
	fs := project.Features(c, late)
	if len(fs) != 2 || fs[0].Name != "refunds" || fs[1].Name != "security" {
		t.Errorf("late belongs to %v, want refunds and security", fs)
	}
	if fs := project.Features(c, res.Tree.Find("refunds.gifts.gift_limit")); len(fs) != 1 || fs[0].Name != "gifts" {
		t.Errorf("gift_limit belongs to %v, want the nested gifts", fs)
	}
	if got := specmodel.KindLayer(c, c.KindOf(res.Tree.Find("refunds"))); got != specmodel.Specification {
		t.Errorf("a feature's layer is %d", got)
	}
}

// Every error has a @status, its one required projection.
func TestErrorStatus(t *testing.T) {
	res := checkSrc(t, head+`
found: error(404, id = "x") {
  title = "found"
}

missing: error(id = "y") {
  title = "missing"
}

inherited: error(found) {}
`)
	wantAll(t, res, "missing-argument: kind error needs the argument status")
	if n := len(res.Diags); n != 1 {
		t.Errorf("an error that extends another inherits its status: %v", messages(res))
	}
}

// A command's parameters are its inputs, and its usage line is derived
// from them: options first, then arguments, by their display names.
func TestCommandUsage(t *testing.T) {
	res := checkSrc(t, head+`
refund(
  payment: String,
  @option(short = "-n", env = "SHOP_DRY_RUN") dry_run?: Bool,
  @option(value = "file") config: String = "shop.toml",
  @option limit?: Int,
  @display("--tag value") tags<variadic>?: String,
): command("shop refund") {{ Refunds a payment. }}

plain: command("shop version") {{ Prints the version. }}
`)
	if res.Diags.HasErrors() {
		t.Fatal(messages(res))
	}
	c := res.Checker
	n := res.Tree.Find("refund")
	if got, want := specmodel.CommandUsage(c, n), "shop refund [--dry-run] [--config file] [--limit limit] <payment> [--tag value…]"; got != want {
		t.Errorf("usage %q, want %q", got, want)
	}
	ps := specmodel.CommandParams(c, n)
	if len(ps) != 5 || ps[0].Option || !ps[1].Option || ps[1].Flag != "--dry-run" || ps[1].Short != "-n" || ps[1].Env != "SHOP_DRY_RUN" || ps[2].Default != `"shop.toml"` || !ps[4].Variadic {
		t.Errorf("params %+v", ps)
	}
	if got := specmodel.CommandUsage(c, res.Tree.Find("plain")); got != "shop version" {
		t.Errorf("usage %q", got)
	}
}
