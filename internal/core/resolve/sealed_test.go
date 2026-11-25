package resolve

import (
	"strings"
	"testing"
)

const sealedSpec = `shop: package(version = "2.1.3", sealed = true) {
  checkout: section {
    pay_once: rule {}
  }
  parts<export>: section {
    cart: section {}
  }
}
`

// A sealed package's nodes are referred to from outside, never amended:
// entering, scoping, setting or declaring into them is an error at the
// site that names the package, through a mount or an import's name alike.
func TestSealedPackage(t *testing.T) {
	cases := map[string]string{
		"enter":     "shop.checkout.{\n  extra: section {}\n}\n",
		"scope":     "shop.checkout.pay_once.{ x = 1 }\n",
		"set":       "shop.checkout.owner = \"me\"\n",
		"declare":   "shop.checkout.more: section {}\n",
		"mounted":   "parts.cart.{\n  x: section {}\n}\n",
		"mount-set": "parts.note = \"x\"\n",
		"nested":    "parts.{\n  cart.{ y = 1 }\n}\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeTree(t, map[string]string{
				"shop/index.heron": sealedSpec,
				"app/index.heron":  "import = [\"../shop/\"]\n" + body,
			})
			tr := resolveDir(t, dir, "app/index.heron")
			wantDiag(t, tr, "sealed: ")
			wantDiag(t, tr, "sealed package shop 2.1.3")
		})
	}
}

// References to a sealed package and the tree's own names are fine, and an
// unsealed package may be amended as before.
func TestSealedPackageAllows(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"shop/index.heron": sealedSpec,
		"app/index.heron":  "import = [\"../shop/\"]\nmine: section {}\nmine.{ x = 1 }\nshop_ref = shop.checkout.pay_once\n",
	})
	wantNoDiags(t, resolveDir(t, dir, "app/index.heron"))
	// The sealed package's own tree amends itself freely.
	dir = writeTree(t, map[string]string{
		"shop/index.heron": strings.Replace(sealedSpec, "}\n}\n", "}\n  checkout.{ y = 1 }\n}\n", 1),
		"app/index.heron":  "import = [\"../shop/\"]\n",
	})
	wantNoDiags(t, resolveDir(t, dir, "app/index.heron"))
	dir = writeTree(t, map[string]string{
		"shop/index.heron": strings.Replace(sealedSpec, ", sealed = true", "", 1),
		"app/index.heron":  "import = [\"../shop/\"]\nparts.cart.{\n  x: section {}\n}\n",
	})
	wantNoDiags(t, resolveDir(t, dir, "app/index.heron"))
}
