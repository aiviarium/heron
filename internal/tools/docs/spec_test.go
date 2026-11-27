package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specSrc holds one of each kind std/spec gives a specification.
const specSrc = `import = ["std/doc@1", "std/types@1", "std/http@1", "std/docs@1"]

Status: enum {
  OPEN: Self
  SHUT: Self
}

@realizes(refunds.never_twice, refunds.held)
Ticket: type {
  status: Status
}

desk: part {{ The support desk. }}

refunds: section {
  | Refunds of captured payments.

  capture_date: term {{ The day a payment was captured. }}

  @by(desk)
  window: rule {
    if {{ the capture date is past }}
    must {{ reject the request }}
    while {{ the payment is under no review }}
    then {{ the ticket stays open }}
    example {{ day 31 is rejected }}
  }

  never_twice: rule {
    mustNot {{ refund a ticket twice }}
    instead {{ answer with the first refund }}
  }

  held: rule {
    must {{ hold }}
  }

  later: nongoal {{ Partial refunds. }}

  @targets(Ticket)
  hints: guidance {{ Use the ledger client. }}

  quick: quality(measure = "Answers within two seconds.") {{ Refunds feel quick. }}

  @serves(refunds.quick)
  fast_answers: scenario {
    stimulus =| a refund is requested
    environment =| production
    response =| the request is answered
    measure =| within two seconds at the 99th percentile
  }

  ticket_life: states(Ticket.status) {
    initial = Status.OPEN
    terminal = [Status.SHUT]

    shut: transition(Status.OPEN, Status.SHUT) {
      when {{ the refund is paid }}
    }
  }
}

desk_api: api {
  @performs(refunds.ticket_life.shut)
  close(id: String): op(Ticket)
}

@targets(refunds.window)
@owner("finance")
which_days: spec.open {{ Business days or calendar days? }}
`

func TestSpecKindsRender(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.heron"), []byte(specSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	s := site(t, filepath.Join(dir, "index.heron"))
	render := func(path string) string {
		t.Helper()
		n := s.Tree.Find(path)
		if n == nil {
			t.Fatalf("no node %s", path)
		}
		f, err := s.RenderNode(n)
		if err != nil {
			t.Fatal(err)
		}
		return f.HTML
	}
	for path, wants := range map[string][]string{
		// The conditions lead, the subject and strength carry the behaviour.
		"refunds.window":       {"While the payment is under no review, if the capture date is past", "desk</a> <strong class=\"strength\">must</strong> reject the request, so the ticket stays open. <span class=\"rule-example\">For example, day 31 is rejected.</span>"},
		"refunds.never_twice":  {"A ticket <strong class=\"strength\">must not</strong> refund a ticket twice, and must instead answer with the first refund."},
		"refunds.held":         {"A ticket <strong class=\"strength\">must</strong> hold."},
		"refunds.capture_date": {`<dfn class="term-name">Capture date</dfn> — The day a payment was captured.`},
		"refunds.later":        {"Not a goal", "Partial refunds."},
		"refunds.hints":        {"Guidance", "Use the ledger client."},
		"refunds.fast_answers": {`<dt>Measure</dt><dd>within two seconds at the 99th percentile</dd>`, `<dt>Serves</dt><dd><a class="ref" href="refunds.html#refunds.quick">Quick</a></dd>`},
		"refunds.quick":        {`<dt>Measure</dt><dd>Answers within two seconds.</dd>`, `Served by <span class="derived-mark">derived</span></span> <a class="ref" href="refunds.html#refunds.fast_answers">Fast answers</a>`},
		// A lifecycle's transition is a defined node: its mark in the gutter.
		"refunds.ticket_life": {`<h2 class="h"><svg class="icon kind-mark"`, `<div class="rule transition" id="refunds.ticket_life.shut-detail"><p class="sentence"><a class="rule-link gutter-mark" href="#refunds.ticket_life.shut-detail" title="transition · Shut"`, "<code>shut</code>", "<code>OPEN</code>", "<code>SHUT</code>", ">close</code>"},
		"which_days":          {"Open question", `<span class="owner">finance</span>`, "Business days"},
	} {
		h := render(path)
		for _, want := range wants {
			if !strings.Contains(h, want) {
				t.Errorf("%s lacks %q in\n%s", path, want, h)
			}
		}
	}
}
