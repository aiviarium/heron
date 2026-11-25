package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func pageHTML(t *testing.T, s *Site, file string) string {
	t.Helper()
	for _, p := range s.Pages {
		if p.File == file {
			h, err := s.RenderPage(p)
			if err != nil {
				t.Fatal(err)
			}
			return h
		}
	}
	t.Fatalf("no page %s", file)
	return ""
}

// A project's site has a conformance page: each spec requirement with the
// tests proving it or its status, and the spec's changes since `since`.
func TestConformancePage(t *testing.T) {
	s := site(t, entries["shop"])
	h := pageHTML(t, s, "_conformance.html")
	for _, want := range []string{
		"Store implements shop at 2.1.3: 3 proven, 1 planned, 0 untested, 1 missing of 5 requirements",
		"Project version 2.1.3+build2",
		`<code>checkout.pay_once</code>`,
		`<code class="path">internal/payments/charge_test.go</code> <code class="test">TestChargeOnce</code>`,
		`<span class="badge status-planned">planned</span>`,
		`<span class="badge status-missing">missing</span>`,
		"Changes from shop 2.1.2 to 2.1.3",
		`<code>checkout.empty_cart</code>`,
		`<code>checkout.pay_later</code>`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("conformance page lacks %q", want)
		}
	}
	if !strings.Contains(h, `href="_spec.html#shop.checkout.pay_once"`) {
		t.Error("a requirement links to the spec page")
	}
	if site(t, entries["petstore"]).project != nil {
		t.Error("petstore implements no spec, so its site has no conformance page")
	}
}

// A project's site renders its own pages and the spec it implements
// read-only, marked as the spec, which every spec reference links to; a
// component says which spec subjects it realizes.
func TestProjectSite(t *testing.T) {
	s := site(t, entries["shop"])
	spec := pageHTML(t, s, "_spec.html")
	for _, want := range []string{`class="spec-readonly" id="shop"`, "shown read-only", `id="shop.checkout.pay_once"`} {
		if !strings.Contains(spec, want) {
			t.Errorf("spec page lacks %q", want)
		}
	}
	rel := pageHTML(t, s, "_releases.html")
	if !strings.Contains(rel, "<code>v2.1.3+build2</code>") || strings.Index(rel, "build2") > strings.Index(rel, "build1") {
		t.Errorf("releases page, newest first: %s", rel)
	}
	comp := pageHTML(t, s, "architecture.cart.html")
	for _, want := range []string{`href="_spec.html#shop.parts.cart"`, "<code>parts.cart</code>", "<code>buyer</code>"} {
		if !strings.Contains(comp, want) {
			t.Errorf("component page lacks %q", want)
		}
	}
	home := pageHTML(t, s, "index.html")
	if !strings.Contains(home, "Implements shop at 2.1.3") {
		t.Error("the home page shows what the project implements")
	}
}

// With `docs` naming the spec's rendered site, spec references link there,
// at the page that site shows each node on, and the spec isn't repeated.
func TestProjectSiteLinksToSpecSite(t *testing.T) {
	dir := t.TempDir()
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	app := strings.Replace(read("../../../examples/shop/index.heron"), `  since = "../shop-spec-2.1.2/"`, `  docs = "https://specs.example/shop/"`, 1)
	for name, src := range map[string]string{"shop-spec/index.heron": read("../../../examples/shop-spec/index.heron"), "shop/index.heron": app} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644)
	}
	s := site(t, filepath.Join(dir, "shop/index.heron"))
	for _, p := range s.Pages {
		if p.File == "_spec.html" {
			t.Error("with a spec site, the spec isn't rendered again")
		}
	}
	h := pageHTML(t, s, "_conformance.html")
	if !strings.Contains(h, `href="https://specs.example/shop/shop.checkout.html#shop.checkout.pay_once"`) {
		t.Errorf("conformance links to the spec site: %s", h)
	}
}

// A requirement whose subject is a person or an agent, with no proof and
// no status, is a practice: listed in its own group and not counted among
// what the code must meet, while one a proof names stays proven. A rule
// on a system actor with neither is still missing.
func TestConformancePractices(t *testing.T) {
	dir := t.TempDir()
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	spec := strings.Replace(read("../../../examples/shop-spec/index.heron"), "  /// The parts of the system", `  helper: actor(kind = ActorKind.AGENT) {{ An agent that builds the shop. }}
  ledger: actor(kind = ActorKind.SYSTEM) {{ The books. }}

  conduct: section {
    tidy_carts: rule(parts.cart, by = buyer) {
      should {{ empty a cart they won't pay for }}
    }

    small_changes: rule(parts.cart, by = helper) {
      must {{ change one part at a time }}
    }

    proven_habit: rule(parts.cart, by = buyer) {
      must {{ see the total before paying }}
    }

    books_balance: rule(parts.payments, by = ledger) {
      must {{ balance every day }}
    }
  }

  /// The parts of the system`, 1)
	app := strings.Replace(read("../../../examples/shop/index.heron"), "  refunds_later:", `  proves(shop.conduct.proven_habit) {
    verify("internal/cart/total_test.go", test = "TestTotalShown")
  }

  refunds_later:`, 1)
	app = strings.Replace(app, `  since = "../shop-spec-2.1.2/"`+"\n", "", 1)
	for name, src := range map[string]string{"shop-spec/index.heron": spec, "shop/index.heron": app} {
		os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644)
	}
	s := site(t, filepath.Join(dir, "shop/index.heron"))
	rep := s.project.Conformance()
	status := map[string]string{}
	for _, r := range rep.Rows {
		status[r.Path] = r.Status
	}
	for path, want := range map[string]string{
		"conduct.tidy_carts": "practice", "conduct.small_changes": "practice",
		"conduct.proven_habit": "proven", "conduct.books_balance": "missing",
	} {
		if status[path] != want {
			t.Errorf("%s is %q, want %q", path, status[path], want)
		}
	}
	if rep.Counts["practice"] != 2 || rep.Obligations() != len(rep.Rows)-2 {
		t.Errorf("counts %v of %d rows, obligations %d", rep.Counts, len(rep.Rows), rep.Obligations())
	}
	h := pageHTML(t, s, "_conformance.html")
	for _, want := range []string{"(2 practices of people and agents, not counted)", `id="practices"`, `<span class="badge status-practice">practice</span>`} {
		if !strings.Contains(h, want) {
			t.Errorf("conformance page lacks %s", want)
		}
	}
	if md := string(rep.Markdown()); !strings.Contains(md, "## Practices") || !strings.Contains(md, "2 practices of people and agents, not counted.") {
		t.Errorf("markdown lacks the practices:\n%s", md)
	}
}
