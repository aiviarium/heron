package project

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/resolve"
	"github.com/helightdev/specs-v0/internal/core/types"
)

// Report is the conformance of a project to the spec it implements: each
// spec requirement and where it stands, and what changed in the spec since
// the earlier version `since` names.
type Report struct {
	Project string `json:"project"`
	Spec    string `json:"spec"`
	At      string `json:"at"`
	Version string `json:"version,omitempty"`
	Rows    []Row  `json:"requirements"`
	// Counts by status: proven, planned, untested, missing.
	Counts  map[string]int `json:"counts"`
	Changes *Changes       `json:"changes,omitempty"`
}

// Row is one requirement of the report.
type Row struct {
	Path   string  `json:"path"`
	Kind   string  `json:"kind"`
	Status string  `json:"status"`
	Proofs []Proof `json:"proofs,omitempty"`
	// Node is the spec node, for renderers that link to it.
	Node *resolve.Node `json:"-"`
}

// Proof is a test that proves a requirement: its file and test name.
type Proof struct {
	Path string `json:"path"`
	Test string `json:"test,omitempty"`
}

// Changes are the requirements added, removed and changed between two
// versions of a spec, by path inside the package.
type Changes struct {
	From    string   `json:"from"`
	To      string   `json:"to"`
	Added   []string `json:"added"`
	Removed []string `json:"removed"`
	Changed []string `json:"changed"`
	// Err says why the earlier version couldn't be read.
	Err string `json:"error,omitempty"`
}

// Statuses in report order.
var Statuses = []string{"proven", "planned", "untested", "missing"}

// Conformance computes the project's report. The earlier spec version, if
// `since` names one, is read from its path relative to the file that
// declares implements.
func (p *Project) Conformance() *Report {
	r := &Report{Project: p.Node.Name, Spec: p.SpecName(), At: p.At, Version: p.Version, Counts: map[string]int{}}
	if r.Project == "" {
		r.Project = "project"
	}
	for _, s := range Statuses {
		r.Counts[s] = 0
	}
	for _, q := range p.Requirements() {
		row := Row{Path: q.Path, Kind: kindLabel(q.Kind), Status: q.Status, Node: q.Node}
		for _, v := range q.Proofs {
			path, _ := ArgString(KindArgs(v), 0, "path")
			test, _ := ArgString(KindArgs(v), 1, "test")
			row.Proofs = append(row.Proofs, Proof{Path: path, Test: test})
		}
		r.Rows = append(r.Rows, row)
		r.Counts[q.Status]++
	}
	if p.Since != "" {
		r.Changes = p.changes()
	}
	return r
}

func kindLabel(k string) string {
	if i := strings.LastIndexByte(k, '.'); i >= 0 {
		return k[i+1:]
	}
	return k
}

// changes compares the spec's requirements with the earlier version's.
func (p *Project) changes() *Changes {
	ch := &Changes{To: p.SpecVersion(), Added: []string{}, Removed: []string{}, Changed: []string{}}
	dir := "."
	if f := p.Node.Decl.File; f != nil {
		dir = filepath.Dir(string(f.ID))
	}
	path := p.Since
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		path = filepath.Join(path, "index.heron")
	}
	id, err := resolve.Canonical(path)
	if err != nil {
		ch.Err = err.Error()
		return ch
	}
	old := resolve.New(resolve.NewLoader()).Resolve(id)
	if old.Entry == nil {
		ch.Err = "cannot read " + p.Since
		return ch
	}
	oc := types.New(old)
	var pkgNode *resolve.Node
	for _, n := range old.Root.Children {
		if resolve.IsPackageNode(n) {
			pkgNode = n
			if v := resolve.KindArg(n, "version"); v != "" {
				ch.From = v
			}
			break
		}
	}
	before := requirementTexts(old.Root, oc, pkgNode)
	var after map[string]string
	if p.Package != nil && p.Package.Tree != nil {
		after = requirementTexts(p.Package.Tree.Root, p.Checker, p.Package.Node)
	}
	for k, v := range after {
		switch old, ok := before[k]; {
		case !ok:
			ch.Added = append(ch.Added, k)
		case old != v:
			ch.Changed = append(ch.Changed, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			ch.Removed = append(ch.Removed, k)
		}
	}
	sort.Strings(ch.Added)
	sort.Strings(ch.Removed)
	sort.Strings(ch.Changed)
	return ch
}

// requirementTexts maps each requirement under root, by its path inside
// the package, to its source with whitespace collapsed.
func requirementTexts(root *resolve.Node, c *types.Checker, pkgNode *resolve.Node) map[string]string {
	out := map[string]string{}
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range n.Children {
			if ch.Shape == resolve.ShapeMounted {
				continue
			}
			if RequirementKinds[KindPath(c, ch)] {
				path := ch.Path()
				if pkgNode != nil {
					path = strings.TrimPrefix(path, pkgNode.Name+".")
				}
				text := ""
				if ch.Decl.N != nil {
					text = strings.Join(strings.Fields(ch.Decl.N.Text()), " ")
				}
				for _, s := range ch.Sites {
					if s.N != nil {
						text += " " + strings.Join(strings.Fields(s.N.Text()), " ")
					}
				}
				out[path] = text
			}
			walk(ch)
		}
	}
	walk(root)
	return out
}

// JSON is the report as indented JSON.
func (r *Report) JSON() []byte {
	b, _ := json.MarshalIndent(r, "", "  ")
	return append(b, '\n')
}

// Markdown is the report as a Markdown page.
func (r *Report) Markdown() []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# Conformance of %s to %s %s\n\n", r.Project, r.Spec, r.At)
	if r.Version != "" {
		fmt.Fprintf(&b, "Project version %s. ", r.Version)
	}
	fmt.Fprintf(&b, "%d requirements: %d proven, %d planned, %d untested, %d missing.\n\n",
		len(r.Rows), r.Counts["proven"], r.Counts["planned"], r.Counts["untested"], r.Counts["missing"])
	b.WriteString("| Requirement | Kind | Status | Proven by |\n| --- | --- | --- | --- |\n")
	for _, row := range r.Rows {
		var proofs []string
		for _, p := range row.Proofs {
			s := "`" + p.Path + "`"
			if p.Test != "" {
				s += " `" + p.Test + "`"
			}
			proofs = append(proofs, s)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", row.Path, row.Kind, row.Status, strings.Join(proofs, ", "))
	}
	if ch := r.Changes; ch != nil {
		fmt.Fprintf(&b, "\n## Changes from %s %s to %s\n\n", r.Spec, orDash(ch.From), orDash(ch.To))
		if ch.Err != "" {
			fmt.Fprintf(&b, "The earlier version couldn't be read: %s\n", ch.Err)
		} else {
			for _, g := range []struct {
				title string
				paths []string
			}{{"Added", ch.Added}, {"Removed", ch.Removed}, {"Changed", ch.Changed}} {
				fmt.Fprintf(&b, "- %s:", g.title)
				if len(g.paths) == 0 {
					b.WriteString(" none")
				}
				for i, p := range g.paths {
					if i > 0 {
						b.WriteString(",")
					}
					b.WriteString(" `" + p + "`")
				}
				b.WriteString("\n")
			}
		}
	}
	return []byte(b.String())
}

func orDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}
