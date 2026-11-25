package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specSrc holds one of each kind std/spec gives a specification.
const specSrc = `import = ["std/doc@1", "std/types@1", "std/spec@1", "std/tech@1", "std/product@1", "std/http@1"]

Status: enum {
  OPEN: Self
  SHUT: Self
}

Ticket: type {
  status: Status
}

desk: component(path = "internal/desk", lang = "go")

refunds: section {
  | Refunds of captured payments.

  capture_date: term {{ The day a payment was captured. }}

  window: rule(Ticket, by = desk) {
    when {{ a refund is requested }}
    if {{ the capture date is past }}
    must {{ reject the request }}
    unless {{ the payment is under review }}
    then {{ the ticket stays open }}
    example {{ day 31 is rejected }}
  }

  never_twice: rule<planned>(Ticket) {
    mustNot {{ refund a ticket twice }}
    instead {{ answer with the first refund }}
  }

  held: rule(Ticket) {
    must {{ hold }}
  }

  later: outOfScope {{ Partial refunds. }}

  hints: guidance(Ticket) {{ Use the ledger client. }}

  latency: metric(Duration) {{ The time from request to answer. }}

  fast_answers: quality(Ticket) {
    metric = latency
    at_most = "2s"
    percentile = 0.99
    should {{ answer within two seconds }}
    applies {{ in production }}
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

which_days: spec.open(refunds.window, owner = "finance") {{ Business days or calendar days? }}
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
		// The conditions lead, the subject and strength carry the behaviour, unless closes.
		"refunds.window":       {"When a refund is requested, if the", ">desk</code></a> <strong class=\"strength\">must</strong> reject the request, unless the payment is under review, so the ticket stays open. <span class=\"rule-example\">For example, day 31 is rejected.</span>"},
		"refunds.never_twice":  {`<span class="status status-planned">planned</span>`, "A ticket <strong class=\"strength\">must not</strong> refund a ticket twice, and must instead answer with the first refund."},
		"refunds.held":         {"A ticket <strong class=\"strength\">must</strong> hold."},
		"refunds.capture_date": {`<dt class="term-name">`, "The day a payment was captured."},
		"refunds.later":        {"Out of scope", "Partial refunds."},
		"refunds.hints":        {"Guidance", "Use the ledger client."},
		"refunds.fast_answers": {`<code class="chip chip-fact"><a class="ref code-ref" href="refunds.html#refunds.latency"><span class="tk-name">latency</span></a> ≤ 2s p<span class="tk-num">0.99</span></code>`, `<span class="rule-extra">Applies: `, "<strong class=\"strength\">should</strong> answer within two seconds"},
		"refunds.ticket_life":  {"<svg", "<code>shut</code>", "<code>OPEN</code>", "<code>SHUT</code>", ">close</code>"},
		"which_days":           {"Open question", `<span class="owner">finance</span>`, "Business days"},
	} {
		h := render(path)
		for _, want := range wants {
			if !strings.Contains(h, want) {
				t.Errorf("%s lacks %q in\n%s", path, want, h)
			}
		}
	}
}
