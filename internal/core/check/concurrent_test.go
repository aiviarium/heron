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

const sharedApp = `import = ["std/doc@1", "../shop/"]

app: section {
  | Builds on [the shop](#shop).
}
`

// A package's nodes don't change after it's built, so two checks of trees
// that share it run at once without a race (run with -race), and each
// reads a requirement written as an element with its arguments: the rule's
// subject is the actor its refs name in both.
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
			pkg := trees[i].Imports[len(trees[i].Imports)-1].Tree
			pkg.Walk(func(n *resolve.Node) {
				if n.Name == "tidy" {
					if by := project.Subject(res.Checker, n); by != nil {
						got[i] = by.Name
					}
				}
			})
		}()
	}
	wg.Wait()
	for i, g := range got {
		if g != "buyer" {
			t.Errorf("check %d: the element rule's subject is %q, want buyer", i, g)
		}
	}
}
