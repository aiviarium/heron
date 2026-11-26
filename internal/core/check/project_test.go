package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// checkFiles writes files under a temp dir and checks the entry.
func checkFiles(t *testing.T, files map[string]string, entry string) *check.Result {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id, err := resolve.Canonical(filepath.Join(dir, entry))
	if err != nil {
		t.Fatal(err)
	}
	return check.NewSession().Check(id)
}

const projectKinds = `
x: project(path = ".")

@realizes(x)
c: component("internal/c") {}

proves(x) {
  verify("internal/c/c_test.go", test = "TestC")
}

later: status<planned>(x)
built: status<untested>(x) {{ Built with the first cut. }}

first: release("1.0.0+build1", date = "2026-11-01") {{ The first release. }}
`

// std/project's kinds check clean with std/spec and std/product in scope,
// in either import order: std/project's package node doesn't hide
// std/spec's project kind, and its <planned> doesn't clash with
// std/product's.
func TestProjectKindsCheck(t *testing.T) {
	for _, imports := range []string{
		`["std/doc@1", "std/spec@1", "std/project@1"]`,
		`["std/project@1", "std/doc@1", "std/spec@1"]`,
	} {
		res := checkFiles(t, map[string]string{"index.heron": "import = " + imports + "\n" + projectKinds}, "index.heron")
		var msgs []string
		for _, d := range res.Diags {
			msgs = append(msgs, d.Code+": "+d.Message)
		}
		if len(msgs) > 0 {
			t.Errorf("%s: %s", imports, strings.Join(msgs, "\n"))
		}
	}
}

const shopSpec = `import = ["std/doc@1", "std/spec@1"]

shop: package(version = "2.1.3", sealed = true) {
  buyer: actor {{ Someone who buys. }}

  checkout: section {
    pay_once: rule(by = buyer) {
      must {{ pay for an order once }}
    }

    receipt: rule {
      must {{ send a receipt }}
    }
  }
}
`

func diagStrings(res *check.Result) []string {
	var out []string
	for _, d := range res.Diags {
		out = append(out, d.Code+": "+d.Message)
	}
	return out
}

func wantDiagContaining(t *testing.T, res *check.Result, sub string) {
	t.Helper()
	for _, d := range diagStrings(res) {
		if strings.Contains(d, sub) {
			return
		}
	}
	t.Errorf("no diagnostic containing %q in %v", sub, diagStrings(res))
}

const projectHead = `import = ["std/doc@1", "std/spec@1", "std/project@1", "../shop/"]

app: implements(shop, at = "2.1.3") {
  version = "2.1.3+build1"
}
`

// A tree that implements a spec is in project mode: each spec kind it
// declares is an error naming what it is; project-side kinds are fine.
func TestProjectMode(t *testing.T) {
	cases := map[string]string{
		"rule":    "extra: rule {\n  must {{ do more }}\n}\n",
		"goal":    "g: goal {{ More. }}\n",
		"term":    "word: term {{ A word. }}\n",
		"actor":   "admin: actor {{ An admin. }}\n",
		"error":   "d: error(\"E1\") {\n  title = \"bad\"\n}\n",
		"nested":  "notes: section {\n  r: rule {\n    must {{ x }}\n  }\n}\n",
		"command": "c: command(\"shop\") {}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			res := checkFiles(t, map[string]string{"shop/index.heron": shopSpec, "app/index.heron": projectHead + body}, "app/index.heron")
			wantDiagContaining(t, res, "project-mode: ")
			wantDiagContaining(t, res, "which is spec content: this tree implements shop")
		})
	}
	ok := projectHead + `
arch: section {
  @realizes(shop.buyer)
  cart: component("internal/cart") {
    responsibility {{ Holds the cart. }}
  }
}

proves(shop.checkout.pay_once) {
  verify("internal/cart/pay_test.go", test = "TestPayOnce")
}

status<planned>(shop.checkout.receipt)
`
	res := checkFiles(t, map[string]string{"shop/index.heron": shopSpec, "app/index.heron": ok}, "app/index.heron")
	if ds := diagStrings(res); len(ds) > 0 {
		t.Errorf("project-side kinds: %v", ds)
	}
	// The same spec kinds are fine in a tree that implements nothing.
	res = checkFiles(t, map[string]string{"index.heron": "import = [\"std/doc@1\", \"std/spec@1\"]\nr: rule {\n  must {{ x }}\n}\n"}, "index.heron")
	if ds := diagStrings(res); len(ds) > 0 {
		t.Errorf("no project: %v", ds)
	}
}

// implements stands once at the root and names an imported spec; proofs
// and statuses name the spec's requirements.
func TestProjectStructure(t *testing.T) {
	cases := map[string]struct{ body, want string }{
		"nested implements":  {"inner: section {\n  again: implements(shop, at = \"2.1.3\")\n}\n", "implements stands at the root"},
		"second implements":  {"again: implements(shop, at = \"2.1.3\")\n", "this is a second implements"},
		"own target":         {"mine: section {}\nproves(mine) {\n  verify(\"a_test.go\")\n}\n", "mine is the project's own"},
		"not a requirement":  {"proves(shop.checkout) {\n  verify(\"a_test.go\")\n}\n", "checkout is no spec requirement"},
		"proof without test": {"proves(shop.checkout.pay_once)\n", "a proof holds the verify"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := checkFiles(t, map[string]string{"shop/index.heron": shopSpec, "app/index.heron": projectHead + tc.body}, "app/index.heron")
			wantDiagContaining(t, res, tc.want)
		})
	}
	res := checkFiles(t, map[string]string{"shop/index.heron": shopSpec, "app/index.heron": strings.Replace(projectHead, "implements(shop,", "implements(local,", 1) + "local: section {}\n"}, "app/index.heron")
	wantDiagContaining(t, res, "which this tree declares")
}

// The conformance report gives each spec requirement its standing: proven
// by the tests of the proofs naming it, planned, untested, or missing; and
// the requirements added, removed and changed since the earlier spec.
func TestConformance(t *testing.T) {
	old := strings.NewReplacer(`version = "2.1.3"`, `version = "2.1.2"`, "send a receipt", "send a paper receipt").Replace(shopSpec)
	old = strings.Replace(old, "    receipt: rule {", "    gone: rule {\n      must {{ go away }}\n    }\n\n    receipt: rule {", 1)
	cur := strings.Replace(shopSpec, "  }\n}\n", "\n    refund: rule {\n      must {{ refund }}\n    }\n  }\n}\n", 1)
	src := projectHead[:strings.Index(projectHead, "app:")] + `app: implements(shop, at = "2.1.3") {
  version = "2.1.3+build1"
  since = "../shop-old/"
}

proves(shop.checkout.pay_once) {
  verify("internal/cart/pay_test.go", test = "TestPayOnce")
}

proves(shop.checkout.pay_once) {
  verify("scripts/e2e.sh", test = "pay")
}

status<planned>(shop.checkout.refund)
`
	res := checkFiles(t, map[string]string{"shop/index.heron": cur, "shop-old/index.heron": old, "app/index.heron": src}, "app/index.heron")
	if ds := diagStrings(res); len(ds) > 0 {
		t.Fatal(ds)
	}
	p := project.Find(res.Tree, res.Checker)
	rep := p.Conformance()
	got := map[string]string{}
	for _, r := range rep.Rows {
		got[r.Path] = r.Status
		if r.Path == "checkout.pay_once" && len(r.Proofs) != 2 {
			t.Errorf("pay_once: proofs %v", r.Proofs)
		}
	}
	want := map[string]string{"checkout.pay_once": "proven", "checkout.receipt": "missing", "checkout.refund": "planned"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s, want %s (all: %v)", k, got[k], v, got)
		}
	}
	ch := rep.Changes
	if ch == nil || ch.Err != "" {
		t.Fatalf("changes: %+v", ch)
	}
	if ch.From != "2.1.2" || ch.To != "2.1.3" || strings.Join(ch.Added, ",") != "checkout.refund" || strings.Join(ch.Removed, ",") != "checkout.gone" || strings.Join(ch.Changed, ",") != "checkout.receipt" {
		t.Errorf("changes: %+v", ch)
	}
	md := string(rep.Markdown())
	for _, s := range []string{"# Conformance of app to shop 2.1.3", "1 proven, 1 planned, 0 untested, 1 missing", "| `checkout.pay_once` | rule | proven | `internal/cart/pay_test.go` `TestPayOnce`, `scripts/e2e.sh` `pay` |", "## Changes from shop 2.1.2 to 2.1.3", "- Added: `checkout.refund`", "- Removed: `checkout.gone`", "- Changed: `checkout.receipt`"} {
		if !strings.Contains(md, s) {
			t.Errorf("markdown lacks %q:\n%s", s, md)
		}
	}
}

// The project version starts with the implemented spec version, which
// copies the spec package's; versions and releases follow the scheme the
// versioning config chooses, the 3-part one by default.
func TestProjectVersions(t *testing.T) {
	impl := func(at, version, extra string) string {
		return `import = ["std/doc@1", "std/spec@1", "std/project@1", "../shop/"]

app: implements(shop, at = "` + at + `") {
  version = "` + version + `"
` + extra + `}
`
	}
	patch := "  versioning.{ scheme = \"SM.Sm.Sp.IP\" }\n"
	cases := map[string]struct{ src, want string }{
		"spec part differs":  {impl("2.1.3", "2.1.2+build1", ""), "the project version 2.1.2+build1 starts with 2.1.2, but the project implements shop at 2.1.3"},
		"at not the spec's":  {impl("2.1.2", "2.1.2+build1", ""), "implements shop at 2.1.2, but the spec package is version 2.1.3"},
		"at with metadata":   {impl("2.1.3+build1", "2.1.3+build1", ""), "a spec version is SM.Sm.Sp"},
		"4 parts by default": {impl("2.1.3", "2.1.3.4", ""), "has 4 numbers before the metadata; the SM.Sm.Sp+<tag>IB scheme has 3"},
		"bad metadata":       {impl("2.1.3", "2.1.3+RC", ""), "the metadata after + is a lower-case label and the build number"},
		"3 parts in patch":   {impl("2.1.3", "2.1.3+rc1", patch), "the SM.Sm.Sp.IP+<tag>IB scheme has 4"},
		"patch spec differs": {impl("2.1.3", "2.1.4.0", patch), "starts with 2.1.4"},
		"unknown scheme":     {impl("2.1.3", "2.1.3", "  versioning.{ scheme = \"semver\" }\n"), "is no implementation version scheme"},
		"bad release":        {impl("2.1.3", "2.1.3+build2", "") + "r: release(\"2.1.3.1\")\n", "release: \"2.1.3.1\" has 4 numbers"},
		"newer release":      {impl("2.1.3", "2.1.3+build2", "") + "r: release(\"2.1.3+build3\")\n", "release 2.1.3+build3 is newer than the project version"},
		"repeated release":   {impl("2.1.3", "2.1.3+build2", "") + "r1: release(\"2.1.3+build1\")\nr2: release(\"2.1.3+build1\")\n", "recorded twice"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			res := checkFiles(t, map[string]string{"shop/index.heron": shopSpec, "app/index.heron": tc.src}, "app/index.heron")
			wantDiagContaining(t, res, "version: ")
			wantDiagContaining(t, res, tc.want)
		})
	}
	for name, src := range map[string]string{
		"default":  impl("2.1.3", "2.1.3+build2", "") + "r1: release(\"2.1.3+build1\")\nr2: release(\"2.1.2+rc4\")\n",
		"bare":     impl("2.1.3", "2.1.3", ""),
		"patch":    impl("2.1.3", "2.1.3.4+rc2", patch) + "r: release(\"2.1.3.4+rc1\")\nold: release(\"2.0.0.7\")\n",
		"patch IP": impl("2.1.3", "2.1.3.0", "  versioning.{ scheme = \"SM.Sm.Sp.IP+<tag>IB\" }\n"),
	} {
		res := checkFiles(t, map[string]string{"shop/index.heron": shopSpec, "app/index.heron": src}, "app/index.heron")
		if ds := diagStrings(res); len(ds) > 0 {
			t.Errorf("%s: %v", name, ds)
		}
	}
}

// A proof at an earlier revision than its requirement's is outdated and
// counts as no proof; intent is never a requirement; the gap report lists
// behaviour no contract entry realizes.
func TestConformanceRevisionsAndGaps(t *testing.T) {
	spec := `import = ["std/doc@1", "std/spec@1"]

shop: package(version = "1.0.0", sealed = true) {
  pay_fast: goal {{ Paying takes one step. }}

  payments: part {{ Takes the buyer's money. }}

  pay_once: rule(by = payments, rev = 2) {
    must {{ charge an order at most once }}
  }

  receipt: rule(by = payments) {
    must {{ send a receipt }}
  }

  @realizes(receipt)
  no_receipt: error("shop.NO_RECEIPT", by = payments) {
    title = "no receipt for {order}"
    order: field(String)
  }
}
`
	app := `import = ["std/doc@1", "std/spec@1", "std/project@1", "../shop/"]

app: implements(shop, at = "1.0.0") {
  version = "1.0.0+build1"
}

proves(shop.pay_once) {
  verify("pay_test.go", test = "TestPayOnce")
}

proves(shop.receipt, shop.no_receipt) {
  verify("receipt_test.go", test = "TestReceipt")
}
`
	res := checkFiles(t, map[string]string{"shop/index.heron": spec, "app/index.heron": app}, "app/index.heron")
	if ds := diagStrings(res); len(ds) > 0 {
		t.Fatal(ds)
	}
	rep := project.Find(res.Tree, res.Checker).Conformance()
	got := map[string]string{}
	for _, r := range rep.Rows {
		got[r.Path] = r.Status
	}
	want := map[string]string{"pay_once": "outdated", "receipt": "proven", "no_receipt": "proven"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if _, ok := got["pay_fast"]; ok {
		t.Errorf("a goal is a requirement: %v", got)
	}
	if strings.Join(rep.Gaps.Unrealized, ",") != "pay_once" {
		t.Errorf("unrealized: %v", rep.Gaps.Unrealized)
	}
	md := string(rep.Markdown())
	for _, s := range []string{"1 outdated, proven at an earlier revision only", "## Behaviour no contract entry realizes", "- `pay_once`"} {
		if !strings.Contains(md, s) {
			t.Errorf("markdown lacks %q:\n%s", s, md)
		}
	}
}
