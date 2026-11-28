package check

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
)

const sharedSpec = `import = ["std/doc@1"]

shop: package(version = "1.0.0", sealed = true) {
  buyer: actor {{ Someone who buys. }}

  checkout: section {
    | <rule id="tidy" refs="buyer"><must>keep the cart tidy</must></rule>
  }
}
`

const sharedApp = `import = ["std/doc@1", "std/project@1", "../shop/"]

app: implements(shop, at = "1.0.0") {
  version = "1.0.0+build1"
}
`

// A package's nodes don't change after it's built, so two checks of trees
// that share it run at once without a race (run with -race), and each
// reads a requirement written as an element with its arguments: a rule by
// a person is a practice in both.
func TestConcurrentChecksSharePackage(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{"shop/index.heron": sharedSpec, "app/index.heron": sharedApp, "app2/index.heron": sharedApp} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s1 := NewSession()
	s2 := &Session{Loader: s1.Loader, Resolver: s1.Resolver, memo: map[[32]byte]diag.List{}}
	var trees []*resolve.Tree
	for _, e := range []string{"app", "app2"} {
		id, err := resolve.Canonical(filepath.Join(dir, e, "index.heron"))
		if err != nil {
			t.Fatal(err)
		}
		trees = append(trees, s1.Resolver.Resolve(id))
	}
	if trees[0].Imports[len(trees[0].Imports)-1] != trees[1].Imports[len(trees[1].Imports)-1] {
		t.Fatal("the trees don't share the package")
	}
	var wg sync.WaitGroup
	got := make([]string, 2)
	for i, s := range []*Session{s1, s2} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := s.checkTree(trees[i])
			res.Complete()
			if len(res.Diags) > 0 {
				got[i] = res.Diags[0].Message
				return
			}
			for _, r := range project.Find(res.Tree, res.Checker).Conformance().Rows {
				if r.Path == "checkout.tidy" {
					got[i] = r.Status
				}
			}
		}()
	}
	wg.Wait()
	for i, g := range got {
		if g != "practice" {
			t.Errorf("check %d: the element rule by a person is %q, want practice", i, g)
		}
	}
}
