// Package check runs the stages of `heron check` over an entry point:
// parse, resolve and type-check, with results memoized per file and per
// resolved node by content hash, so a watch loop or the language server
// redoes only what an edit touches.
package check

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/icon"
	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/rpc"
	"github.com/aiviarium/heron/internal/core/specmodel"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// Session holds what stays valid between checks: parsed files, resolved
// packages and per-node results.
type Session struct {
	Loader   *resolve.Loader
	Resolver *resolve.Resolver
	memo     map[[32]byte]diag.List
	// Recomputed counts the nodes type-checked rather than served from
	// the memo, for tests of incrementality.
	Recomputed int
}

// NewSession returns an empty session.
func NewSession() *Session {
	l := resolve.NewLoader()
	r := resolve.New(l)
	r.Built = types.PackageBuilt
	return &Session{Loader: l, Resolver: r, memo: map[[32]byte]diag.List{}}
}

// Result is one check of an entry point.
type Result struct {
	Tree    *resolve.Tree
	Checker *types.Checker
	Diags   diag.List
	// served lists the nodes whose diagnostics came from the memo, so the
	// checker holds no facts (Refs, LocalUses) about them yet.
	served []*resolve.Node
}

// Complete type-checks the nodes whose diagnostics were served from the
// memo, discarding their diagnostics, so the checker's facts, such as Refs
// and LocalUses, cover the whole tree. Lints and the language server's
// queries need them; diagnostics alone don't.
func (r *Result) Complete() {
	for _, n := range r.served {
		r.Checker.CheckNode(n)
	}
	r.served = nil
}

// Ensure checks a memo-served node, so the checker's facts cover it.
func (r *Result) Ensure(n *resolve.Node) {
	for i, x := range r.served {
		if x == n {
			r.Checker.CheckNode(x)
			r.served = append(r.served[:i:i], r.served[i+1:]...)
			return
		}
	}
}

// Check parses, resolves and type-checks the tree of an entry point.
func (s *Session) Check(entry resolve.FileID) *Result {
	return s.checkTree(s.Resolver.Resolve(entry))
}

// checkTree type-checks a resolved tree. It reads the packages the tree
// imports and never changes them, so trees sharing a package can be
// checked at once.
func (s *Session) checkTree(t *resolve.Tree) *Result {
	c := types.New(t)
	res := &Result{Tree: t, Checker: c}
	res.Diags = append(res.Diags, t.Diags...)
	res.Diags = append(res.Diags, t.PackageDiags()...)
	iface := interfaceHash(t)
	next := map[[32]byte]diag.List{}
	nodes := append(append([]*resolve.Node(nil), t.Root.Children...), t.Root)
	for _, n := range nodes {
		key := nodeKey(n, iface)
		ds, ok := s.memo[key]
		if !ok {
			s.Recomputed++
			ds = c.CheckNode(n)
		} else {
			res.served = append(res.served, n)
		}
		next[key] = ds
		res.Diags = append(res.Diags, ds...)
	}
	for k, v := range next {
		s.memo[k] = v
	}
	// Requirements written as elements in nodes the memo served read their
	// attributes before conformance and the docs do; a package's were read
	// when it was built.
	c.ElementArgs(t)
	// Project mode and the implementation version scheme, over the whole
	// tree: a tree that implements a spec leaves the spec's kinds to it.
	res.Diags = append(res.Diags, project.Check(t, c)...)
	// The spec layers and the shapes of std/spec's kinds.
	res.Diags = append(res.Diags, specmodel.Check(t, c)...)
	// The RPC bindings: gRPC services and JSON-RPC methods (std/proto,
	// std/jsonrpc), their names, numbers and what can't be projected.
	res.Diags = append(res.Diags, rpc.Build(t, c).Diags...)
	// The docs' lenses and authored pages (std/docs). Their diagnostics
	// name a lens where the checker would only say the name is unknown.
	lds, replaced := lens.Check(t, c, CheckExample)
	if len(replaced) > 0 {
		keep := res.Diags[:0]
		for _, d := range res.Diags {
			if d.Code == "unresolved" && replaced[fmt.Sprintf("%s:%d", d.File, d.Start)] {
				continue
			}
			keep = append(keep, d)
		}
		res.Diags = keep
	}
	res.Diags = append(res.Diags, lds...)
	// Icon marks name icons a package declares (std/docs).
	res.Diags = append(res.Diags, icon.Check(t, c)...)
	res.Diags = res.Diags.Sort()
	return res
}

// interfaceHash covers what any node's check may depend on beyond its own
// sites: every file's structure, without the prose, docs and code inside
// bodies, which only their own node reads. A prose file's text is kept
// whole, since its headings declare sections.
func interfaceHash(t *resolve.Tree) [32]byte {
	h := sha256.New()
	files := append([]*resolve.File(nil), t.Files...)
	seen := map[*resolve.Package]bool{}
	var pkgs func(*resolve.Tree)
	pkgs = func(t *resolve.Tree) {
		for _, p := range t.Imports {
			if seen[p] || p.Tree == nil {
				continue
			}
			seen[p] = true
			files = append(files, p.Tree.Files...)
			pkgs(p.Tree)
		}
	}
	pkgs(t)
	if t.Context != nil && t.Context.File != nil {
		files = append(files, t.Context.File)
	}
	for _, f := range files {
		h.Write([]byte(f.ID))
		h.Write([]byte{0})
		if strings.HasSuffix(string(f.ID), ".heron.md") {
			h.Write(f.Hash[:])
			continue
		}
		writeStructure(h, f.Tree.Root)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func writeStructure(h hash.Hash, n *syntax.Node) {
	switch n.Kind {
	case syntax.ProseLines, syntax.ProseBlock, syntax.Doc, syntax.DoBlock, syntax.CommentLine:
		h.Write([]byte{1})
		return
	}
	for _, c := range n.Children {
		switch c := c.(type) {
		case *syntax.Token:
			if !c.Kind.IsTrivia() {
				h.Write([]byte(c.Text))
				h.Write([]byte{0})
			}
		case *syntax.Node:
			writeStructure(h, c)
		}
	}
}

// nodeKey identifies a node's check: the interface, and the text and
// position of every site of the node and its descendants. Positions are in
// the key because diagnostics carry them.
func nodeKey(n *resolve.Node, iface [32]byte) [32]byte {
	h := sha256.New()
	h.Write(iface[:])
	h.Write([]byte(n.Path()))
	var site func(resolve.Site)
	site = func(s resolve.Site) {
		if s.N == nil || s.File == nil {
			return
		}
		start, _ := s.N.Span()
		h.Write([]byte(s.File.ID))
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(start))
		h.Write(b[:])
		h.Write([]byte(s.N.Text()))
	}
	var walk func(*resolve.Node, bool)
	walk = func(n *resolve.Node, deep bool) {
		site(n.Decl)
		for _, l := range [][]resolve.Site{n.Sites, n.Prose, n.Code, n.Annotations, n.Docs, n.GroupAnnotations} {
			for _, s := range l {
				site(s)
			}
		}
		site(n.ValueSite)
		if deep {
			for _, c := range n.Children {
				walk(c, true)
			}
		}
	}
	walk(n, n.Shape != resolve.ShapeRoot)
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

// Pos maps a diagnostic's file and offset to a line and column, through
// the files the session has loaded.
func (s *Session) Pos(file string, off int) (int, int) {
	if f := s.Loader.Cached(resolve.FileID(file)); f != nil {
		return syntax.LineCol(f.Src, off)
	}
	return 0, 0
}

// exampleMemo remembers each example source's result, so a watch loop or
// the language server checks an authored page's examples once.
var (
	exampleMu   sync.Mutex
	exampleMemo = map[[32]byte]exampleResult{}
)

type exampleResult struct {
	msg  string
	line int
	ok   bool
}

// CheckExample checks the source of an authored page's example as the
// entry point of a tree of its own, in a folder with no other file, where
// only the standard packages resolve, and returns its first error.
func CheckExample(src string) (string, int, bool) {
	key := sha256.Sum256([]byte(src))
	exampleMu.Lock()
	if r, ok := exampleMemo[key]; ok {
		exampleMu.Unlock()
		return r.msg, r.line, r.ok
	}
	exampleMu.Unlock()
	r := exampleResult{ok: true}
	dir, err := os.MkdirTemp("", "heron-example-")
	if err != nil {
		return "", 0, true
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "index.heron")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		return "", 0, true
	}
	if id, err := resolve.Canonical(path); err == nil {
		res := NewSession().Check(id)
		for _, d := range res.Diags {
			if d.Severity == diag.Error {
				line, _ := syntax.LineCol(src, d.Start)
				r = exampleResult{msg: d.Message, line: line}
				break
			}
		}
	}
	exampleMu.Lock()
	exampleMemo[key] = r
	exampleMu.Unlock()
	return r.msg, r.line, r.ok
}
