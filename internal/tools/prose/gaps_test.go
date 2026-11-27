package prose_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/lint"
)

const gapsIndex = `import = ["std/doc@1", "std/spec@1", "std/lint@1"]

customer: actor {{ Someone who paid. }}

desk: part {{ The support console. }}

refunds: section {
  self_service: goal {{ Customers fix a wrong charge themselves. }}

  @by(desk)
  late: rule {
    must {{ refund the amount and/or offer credit }}
  }

  @by(desk)
  never: rule {
    mustNot {{ never refund twice }}
  }

  @by(desk)
  odd: rule {
    while {{ it's not the case that the payment is held }}
    must {{ refund it }}
  }

  @by(desk)
  stored: rule {
    must {{ keep the refund in the database for a sprint }}
  }

  @illustrates(refunds.late)
  click_flow: scenario {
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
	write(t, filepath.Join(dir, "index.heron"), `import = ["std/doc@1", "std/spec@1", "std/lint@1"]

desk: part {{ The console. }}

@by(desk)
r: rule {
  must {{ refund }}
}

c: constraint(reason = "The card network says so.") {
  must {{ refund to the card }}
}
`)
	got, _ := lintDir(t, dir, lint.NewProseCache())
	var un []string
	for _, f := range got {
		if f.code == "Spec.Unmotivated" {
			un = append(un, f.text)
		}
	}
	if len(un) != 1 || !strings.Contains(un[0], "r: rule") {
		t.Errorf("unmotivated: %q", un)
	}
}
