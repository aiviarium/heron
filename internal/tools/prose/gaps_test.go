package prose_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/lint"
)

const gapsIndex = `import = ["std/doc@1", "std/lint@1"]

customer: actor {{ Someone who paid. }}

desk: part {{ The support console. }}

refunds: section {
  self_service: goal {{ Customers fix a wrong charge themselves. }}

  late: rule(desk) {
    must {{ refund the amount and/or offer credit }}
  }

  never: rule(desk) {
    must_not {{ never refund twice }}
  }

  odd: rule(desk) {
    while {{ it's not the case that the payment is held }}
    must {{ refund it }}
  }

  stored: rule(desk) {
    must {{ keep the refund in the database for a sprint }}
  }

  click_flow: scenario(refunds.late) {
    given {{ a payment }}
    given {{ a customer }}
    given {{ a desk }}
    when {{ the customer clicks the button }}
    then {{ a refund }}
    then {{ a receipt }}
  }

  buy: usecase(actor = customer) {{
    1. [customer] asks for a refund.
    2. [desk] refunds it.

    - 2a. The window closed:
      [desk] says no. Fails.
  }}
}

quiet: section {
  lonely: goal {{ Nobody serves this. }}
  speedy: quality {{ Feels quick. }}
}

@status(422)
orphan: error("orphan") {
  title = "bad"
}
`

// The gap lints and the layer's prose lints, each once.
func TestGapAndLayerLints(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.heron"), gapsIndex)
	got, _ := lintDir(t, dir, lint.NewProseCache())
	var codes []string
	for _, f := range got {
		if strings.HasPrefix(f.code, "Spec.") {
			codes = append(codes, f.code+" "+strings.Fields(f.text + " x")[0])
		}
	}
	sort.Strings(codes)
	all := strings.Join(codes, "\n")
	for _, want := range []string{
		"Spec.CompoundRule and/or",
		"Spec.BareNegation never",
		"Spec.NegatedCondition not",
		"Spec.DesignWord database",
		"Spec.DeliveryWord sprint",
		"Spec.ScriptedScenario clicks",
		"Spec.LongScenario",
		"Spec.UnservedGoal",
		"Spec.MeasureMissing",
		"Spec.OrphanEntry",
		"Spec.UnhandledFailure",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("no %q in\n%s", want, all)
		}
	}
	// The rules under refunds serve the goal the section opens with.
	if strings.Contains(all, "Spec.Unmotivated") {
		t.Errorf("a rule under an area's goal is unmotivated:\n%s", all)
	}
}

// A requirement no goal reaches is unmotivated, unless it's a constraint.
func TestUnmotivated(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.heron"), `import = ["std/doc@1", "std/lint@1"]

desk: part {{ The console. }}

r: rule(desk) {
  must {{ refund }}
}

c: constraint {
  must {{ refund to the card }}
  rationale {{ The card network says so. }}
}

intent: section {
  refunds_served: goal {{ Customers get their money back. }}
}

refunds: feature(intent.refunds_served) {
  in_feature: rule(desk) {
    must {{ refund in time }}
  }
}

joins: rule(desk, refunds) {
  must {{ refund in the currency paid }}
}
`)
	got, _ := lintDir(t, dir, lint.NewProseCache())
	var un []string
	for _, f := range got {
		if f.code == "Spec.Unmotivated" {
			un = append(un, f.text)
		}
	}
	// The feature's goal motivates the rules it holds and those that
	// reference it.
	if len(un) != 1 || !strings.Contains(un[0], "r: rule") {
		t.Errorf("unmotivated: %q", un)
	}
	for _, f := range got {
		if f.code == "Spec.UnservedGoal" {
			t.Errorf("a feature's goal is served by its rules: %s", f.text)
		}
	}
}

// An entry's hard references, in its constructor or its @touch, keep it
// from being an orphan; a soft @see doesn't.
func TestOrphanCountsHardReferences(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "index.heron"), `import = ["std/doc@1", "std/lint@1", "std/types@1"]

desk: part {{ The support console. }}

goal_area: section {
  self_service: goal {{ Customers fix a wrong charge themselves. }}

  late: rule(desk) {
    must {{ refund the amount }}
  }

  refund_cmd: command("refund", goal_area.late)
}

shop: api {
  @touch(goal_area.late)
  touched(): op(Int)
  built(): op(Int, goal_area.late)
}

docs_api: api {
  @see(goal_area.late)
  seen(): op(Int)
}
`)
	got, _ := lintDir(t, dir, lint.NewProseCache())
	var orphans []string
	for _, f := range got {
		if f.code == "Spec.OrphanEntry" {
			orphans = append(orphans, f.text)
		}
	}
	all := strings.Join(orphans, "\n")
	if !strings.Contains(all, "seen()") {
		t.Errorf("an op with only @see isn't an orphan:\n%s", all)
	}
	for _, n := range []string{"refund_cmd", "touched()", "built()"} {
		if strings.Contains(all, n) {
			t.Errorf("%s is an orphan:\n%s", n, all)
		}
	}
}
