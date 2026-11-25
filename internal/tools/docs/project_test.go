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
