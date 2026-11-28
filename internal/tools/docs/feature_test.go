package docs

import (
	"path/filepath"
	"strings"
	"testing"
)

const featureTree = `import = ["std/doc@1", "std/types@1", "std/http@1", "std/proto@1"]

sell: goal {{ Customers buy what they want. }}

buyer: actor {{ Someone who buys. }}

desk: part {{ The console. }}

safety: feature<security> {{ What keeps payments safe. }}

refunds: feature(sell, buyer) {
  | Refunds of captured payments.

  late: rule(desk) {
    if {{ a refund is late }}
    must {{ reject it }}
  }

  card_only: rule(desk, safety) {
    must {{ refund to the card }}
  }

  late_case: scenario(late) {
    given {{ a payment captured 31 days ago }}
    when {{ a refund is asked }}
    then {{ it's rejected }}
  }

  gifts: feature(optional = true) {
    | Refunds to gift cards.

    gift_limit: rule(desk) {
      where {{ [refunds.gifts] }}
      must {{ refund at most the payment }}
    }
  }

  @status(404)
  gone: error("refund.GONE") {
    title = "payment {payment} is gone"
    payment: String
    | Reported as [late] says.
  }

  @status(409)
  @exit(3)
  busy: error("refund.BUSY") {
    title = "another refund runs"
    | Reported as [late] says.
  }

  @throws(busy)
  refund(
    /// The payment to refund.
    payment: String,
    /// Changes nothing.
    @option(short = "-n")
    dry_run?: Bool,
  ): command("shop refund", late) {{ Refunds a payment. }}
}

@proto("shop.v1")
payments: api {
  @http<post>("/refunds")
  @throws(refunds.gone)
  create_refund(payment: String): op(String, refunds.late)
}
`

// The Features page lists every feature, nested, with its goals, its marks
// and the number of its requirements; a feature's page shows its facts,
// its requirements by kind and their coverage; an error shows its
// effective codes marked derived or explicit; a command shows its usage
// line and its parameters; and the agent export maps the features and
// says which features a rule joins.
func TestFeaturePages(t *testing.T) {
	dir := writeTree(t, map[string]string{"index.heron": featureTree})
	s := site(t, filepath.Join(dir, "index.heron"))
	idx := pageHTML(t, s, "_features.html")
	mustHave(t, "features page", idx, `>Refunds</a>`, `>Gifts</a>`, `>Safety</a>`, `Optional`, `security`, `Requirements 3`, `>Sell</a>`)
	if strings.Index(idx, ">Refunds</a>") > strings.Index(idx, ">Gifts</a>") {
		t.Error("gifts is listed before the feature it nests in")
	}
	f := pageHTML(t, s, "refunds.html")
	mustHave(t, "feature page", f, `class="section feature"`, `For <a`, `>Buyer</a>`, `Covered 1 / 3`, `Not covered by a scenario, an example or a proof`, `>Card only</a>`)
	saf := pageHTML(t, s, "safety.html")
	mustHave(t, "cross-cutting feature", saf, `>Card only</a>`, `Covered 0 / 1`)
	mustHave(t, "error codes", f, `HTTP <code>404 refund.GONE</code>`, `gRPC <code>NOT_FOUND</code> <span class="mark">derived</span>`, `exit <code>3</code> <span class="mark">explicit</span>`)
	cmd := pageHTML(t, s, "refunds.refund.html")
	mustHave(t, "command page", cmd, `shop refund [--dry-run] &lt;payment&gt;`, `<code>-n</code><span class="sep">, </span><code>--dry-run</code>`, `<code>&lt;payment&gt;</code>`, `The payment to refund.`, `<code>3</code> another refund runs`)

	files := agentFiles(t, filepath.Join(dir, "index.heron"), AgentOptions{})
	in := string(fileByPath(files, "INDEX.md").Content)
	mustHave(t, "agent index", in, "## Features", "- [Refunds](refunds.md#refunds): 3 requirements", "  - [Gifts](refunds.gifts.md#refunds.gifts): 1 requirements, optional", "- [Safety](safety.md#safety): 1 requirements")
	var all string
	for _, f := range files {
		all += string(f.Content)
	}
	mustHave(t, "agent export", all, "joins [safety](safety.md#safety)", "Usage:\n\n```sh\nshop refund [--dry-run] <payment>\n```", "option `-n`, `--dry-run` Bool")
}
