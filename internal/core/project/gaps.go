package project

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/usecase"
)

var testFunc = regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]*)\(`)

// gaps are the conformance report's lists of what the links leave open.
func (p *Project) gaps() Gaps {
	var g Gaps
	if p.Package == nil || p.Package.Tree == nil {
		return g
	}
	c := p.Checker
	realized := map[*resolve.Node]bool{}
	p.Package.Tree.Walk(func(n *resolve.Node) {
		for _, t := range Carried(c, n) {
			realized[t] = true
		}
		// An error's prose links the rules that say when it's reported.
		if KindPath(c, n) == "spec.error" {
			for _, t := range ErrorRules(c, n) {
				realized[t] = true
			}
		}
	})
	isPart := func(n *resolve.Node) bool { return n != nil && KindPath(c, n) == "spec.part" }
	// The innermost feature an entry stands in, which the report groups
	// it by.
	inFeature := func(n *resolve.Node, path string) {
		if f := Feature(c, n); f != nil {
			if g.Features == nil {
				g.Features = map[string]string{}
			}
			g.Features[path] = RelPath(p.Package, f)
		}
	}
	p.Package.Tree.Walk(func(n *resolve.Node) {
		switch KindPath(c, n) {
		case "spec.rule":
			if s := Subject(c, n); s != nil && !isPart(s) {
				return
			}
			if !realized[n] {
				g.Unrealized = append(g.Unrealized, RelPath(p.Package, n))
				inFeature(n, RelPath(p.Package, n))
			}
		case "spec.usecase":
			for _, ps := range n.Prose {
				if ps.N == nil || ps.File == nil {
					continue
				}
				leads := map[int]*resolve.Node{}
				ps.N.Walk(func(x *syntax.Node) bool {
					if x.Kind == syntax.ShortLink {
						st, _ := x.Span()
						x.Walk(func(y *syntax.Node) bool {
							if y.Kind == syntax.Path {
								leads[st] = c.Refs[y]
								return false
							}
							return true
						})
						return false
					}
					return true
				})
				st, en := ps.N.Span()
				uc := usecase.Parse(ps.File.Src[st:en], st)
				for _, step := range uc.Steps {
					if isPart(leads[step.Start]) && !realized[n.Child(step.Num)] && !realized[n] {
						g.Unrealized = append(g.Unrealized, RelPath(p.Package, n)+"."+step.Num)
						inFeature(n, RelPath(p.Package, n)+"."+step.Num)
					}
				}
			}
		}
	})
	// Tests of the project's Go code that no verify names.
	cited := map[string]bool{}
	for _, pr := range p.Proofs {
		for _, v := range childrenOfKind(c, pr, KindVerify) {
			path, _ := ArgString(KindArgs(v), 0, "path")
			test, _ := ArgString(KindArgs(v), 1, "test")
			cited[path+"\x00"+test] = true
		}
	}
	if p.Node != nil && p.Node.Decl.File != nil && filepath.IsAbs(string(p.Node.Decl.File.ID)) {
		root := p.repoRoot()
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				switch d.Name() {
				case "node_modules", ".heron", ".git", "vendor", "testdata":
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for _, m := range testFunc.FindAllStringSubmatch(string(b), -1) {
				if !cited[rel+"\x00"+m[1]] {
					g.UncitedTests = append(g.UncitedTests, rel+" "+m[1])
				}
			}
			return nil
		})
	}
	sort.Strings(g.UncitedTests)
	return g
}

// repoRoot is the folder the project's verify paths are relative to: the
// folder of the tree's entry point.
func (p *Project) repoRoot() string {
	if p.Tree != nil && p.Tree.Entry != nil {
		return filepath.Dir(string(p.Tree.Entry.ID))
	}
	return filepath.Dir(string(p.Node.Decl.File.ID))
}
