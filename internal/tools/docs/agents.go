package docs

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/aiviarium/heron/internal/core/ast"
	"github.com/aiviarium/heron/internal/core/lens"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/rpc"
	"github.com/aiviarium/heron/internal/core/specmodel"
	"github.com/aiviarium/heron/internal/core/syntax"
	"github.com/aiviarium/heron/internal/core/types"
)

// The agent export: the tree written through the `agent` lens as plain
// Markdown files for agents, an index first, then one file per area,
// split by the lens's size budget, each ordered by layer.

// AgentFile is one file of the agent export.
type AgentFile struct {
	// Path is relative to the export's folder, as "language.apis.md".
	Path    string
	Title   string
	ID      string
	Summary string
	Content []byte
}

// Tokens is a text's size in approximate tokens: its bytes over four,
// rounded up.
func Tokens(b []byte) int { return (len(b) + 3) / 4 }

// AgentOptions configure the export.
type AgentOptions struct {
	// Pack is the stable id of a node whose context pack is written too,
	// to packs/<id>.md.
	Pack string
}

// NoNodeError is a context pack's id naming no node of the tree.
type NoNodeError struct{ ID string }

func (e *NoNodeError) Error() string { return "no node " + e.ID + " in the tree" }

// Export file names.
const (
	AgentIndex = "INDEX.md"
	LLMsTxt    = "llms.txt"
	rootBase   = "_root"
	packDir    = "packs/"
)

// layer groups of a file, in the order they're written.
const (
	groupSections = iota
	groupIntent
	groupSpec
	groupContract
	groupNotes
	groupCount
)

var groupTitles = [groupCount]string{"Sections", "Intent", "Specification", "Contract", "Notes"}

// agentItem is one entry of a file: a section, inline or linked, or a
// node of a layer.
type agentItem struct {
	node  *resolve.Node
	group int
	// linked marks a section that has a file of its own.
	linked bool
	text   string
}

// agentUnit is a node that owns files: the tree's top or a section or
// folder scope, with the entries it holds and how they're split.
type agentUnit struct {
	node  *resolve.Node
	id    string
	base  string
	items []*agentItem
	// inline are the sections it holds that have no file of their own.
	inline []*resolve.Node
	// limit is how full a part may get, in bytes; 0 writes one file.
	limit int
	// parts are the item ranges of each file, in order.
	parts [][]*agentItem
}

type agentExport struct {
	s      *Site
	l      *lens.Lens
	top    *resolve.Node
	depth  int
	budget int
	owns   map[*resolve.Node]bool
	units  []*agentUnit
	fileOf map[*resolve.Node]string
	files  map[*agentUnit][]string
	// pack, when set, links within its own file and out of packs/.
	pack *agentUnit
	// Entries are written once, their links as placeholders that name a
	// node, and the placeholders are resolved as each file is written, so
	// splitting a file never renders an entry again.
	cache   map[agentKey]string
	linkTo  []*resolve.Node
	linkIdx map[*resolve.Node]int
	ops     map[*resolve.Node]*opInfo
	terms   map[*resolve.Node][]*resolve.Node
	// headed are the nodes written under a heading of their own; a link
	// to any other node leads to the nearest of them above it.
	headed map[*resolve.Node]bool
}

type agentKey struct {
	n      *resolve.Node
	linked bool
}

// Agents writes the agent export of a checked tree, its index and
// llms.txt first and then each file in the order of the tree, and the
// context pack opts asks for.
func Agents(t *resolve.Tree, c *types.Checker, opts AgentOptions) ([]AgentFile, error) {
	l := lens.Agent(t, c)
	s := New(t, c, Options{Lens: l})
	s.kindPaths = map[*resolve.Node]string{}
	e := &agentExport{s: s, l: l, top: agentTop(s), owns: map[*resolve.Node]bool{}, budget: l.Budget,
		cache: map[agentKey]string{}, linkIdx: map[*resolve.Node]int{}, ops: map[*resolve.Node]*opInfo{},
		terms: map[*resolve.Node][]*resolve.Node{}, headed: map[*resolve.Node]bool{}}
	if e.budget <= 0 {
		e.budget = lens.DefaultBudget
	}
	e.depth = s.depth()
	if e.depth <= 0 {
		e.depth = 2
	}
	s.mdHref = e.placeholder
	e.assign()
	out := e.write()
	if opts.Pack != "" {
		n := e.find(opts.Pack)
		if n == nil {
			return nil, &NoNodeError{ID: opts.Pack}
		}
		out = append(out, e.writePack(n))
	}
	return out, nil
}

// agentTop is the node the export starts at: the tree's one package when
// its root holds nothing else but the docs' configuration, else the root.
func agentTop(s *Site) *resolve.Node {
	r := s.Tree.Root
	if len(r.Prose) > 0 {
		return r
	}
	var pkg *resolve.Node
	for _, c := range r.Children {
		if !own(c) || c.Shape == resolve.ShapeSet || c.Shape == resolve.ShapeValue || c == s.Config.Node {
			continue
		}
		if k := s.kindPath(c); k == "docs.lens" || k == "docs.site" {
			continue
		}
		if !resolve.IsPackageNode(c) || pkg != nil {
			return r
		}
		pkg = c
	}
	if pkg == nil {
		return r
	}
	return pkg
}

// title is the export's title: the site's, or its package's name.
func (e *agentExport) siteTitle() string {
	if e.top != e.s.Tree.Root {
		return displayName(e.s, e.top)
	}
	return e.s.title()
}

// find returns the node a stable id names.
func (e *agentExport) find(id string) *resolve.Node {
	var found *resolve.Node
	e.s.Tree.Walk(func(n *resolve.Node) {
		if found == nil && own(n) && n.Name != "" && stableID(n) == id {
			found = n
		}
	})
	return found
}

// children are the nodes below n the export writes, in the order of the
// tree.
func (e *agentExport) children(n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	for _, ch := range n.Children {
		if !own(ch) || ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue || ch.Shape == resolve.ShapeStep {
			continue
		}
		if ch.Name == "" && ch.Shape != resolve.ShapeAnon {
			continue
		}
		if k := e.s.kindPath(ch); k == "docs.site" || k == "docs.lens" || ch == e.s.Config.Node {
			continue
		}
		if e.s.hidden(ch) {
			continue
		}
		out = append(out, ch)
	}
	return out
}

func (e *agentExport) container(n *resolve.Node) bool {
	if e.s.isContainer(n) {
		return true
	}
	// A package node inside the tree's top holds sections as a scope does.
	return n != e.top && resolve.IsPackageNode(n)
}

// ownKind reports whether a node gets a file of its own at any depth, as
// a command and an api do.
func (e *agentExport) ownKind(n *resolve.Node) bool {
	k := e.s.kindPath(n)
	return n.Name != "" && (k == "spec.command" || k == "types.api")
}

// assign gives files to the sections less than depth levels down, then
// splits each file over the budget until every file fits or is one node.
func (e *agentExport) assign() {
	var walk func(n *resolve.Node, depth int)
	walk = func(n *resolve.Node, depth int) {
		for _, ch := range e.children(n) {
			if e.ownKind(ch) {
				e.owns[ch] = true
				continue
			}
			if !e.container(ch) {
				continue
			}
			if depth < e.depth {
				e.owns[ch] = true
			}
			walk(ch, depth+1)
		}
	}
	walk(e.top, 0)
	for round := 0; round < 256; round++ {
		e.collect()
		if !e.split() {
			if os.Getenv("AGENTS_DEBUG") != "" {
				fmt.Fprintln(os.Stderr, "rounds", round)
			}
			return
		}
	}
}

// placeholder stands for the link to a node the export holds, until the
// file it's written in is known.
func (e *agentExport) placeholder(n *resolve.Node) string {
	if n == nil || !own(n) || e.s.hidden(n) {
		return ""
	}
	return e.ph('N', n)
}

func (e *agentExport) ph(kind byte, n *resolve.Node) string {
	i, ok := e.linkIdx[n]
	if !ok {
		i = len(e.linkTo)
		e.linkTo = append(e.linkTo, n)
		e.linkIdx[n] = i
	}
	return "\x01" + string(kind) + strconv.Itoa(i) + "\x02"
}

var phLinkRe = regexp.MustCompile("\x01([NFT])(\\d+)\x02")

// resolveLinks writes each placeholder as the link it stands for, from
// the file being written.
func (e *agentExport) resolveLinks(t string) string {
	return phLinkRe.ReplaceAllStringFunc(t, func(m string) string {
		i, _ := strconv.Atoi(m[2 : len(m)-1])
		n := e.linkTo[i]
		switch m[1] {
		case 'F':
			return e.hrefFile(n)
		case 'T':
			return e.fileOf[n]
		}
		if h := e.href(n); h != "" {
			return h
		}
		return "#" + stableID(n)
	})
}

// collect reads each unit's entries.
func (e *agentExport) collect() {
	old := map[*resolve.Node]*agentUnit{}
	for _, u := range e.units {
		old[u.node] = u
	}
	e.units = nil
	e.fileOf = map[*resolve.Node]string{}
	var units func(n *resolve.Node)
	add := func(n *resolve.Node) {
		u := &agentUnit{node: n, id: stableID(n), base: stableID(n)}
		if n == e.top {
			u.id, u.base = "", rootBase
		}
		if o := old[n]; o != nil {
			u.limit = o.limit
		}
		e.gather(u, n)
		e.units = append(e.units, u)
	}
	units = func(n *resolve.Node) {
		for _, ch := range e.children(n) {
			if (e.container(ch) || e.ownKind(ch)) && e.owns[ch] {
				add(ch)
			}
			if e.container(ch) || resolve.IsPackageNode(ch) {
				units(ch)
			}
		}
	}
	add(e.top)
	units(e.top)
	e.render()
	e.files = map[*agentUnit][]string{}
	e.headed = map[*resolve.Node]bool{}
	for _, u := range e.units {
		e.partition(u)
	}
}

// gather lists the entries of a unit: its sections in order, then its
// nodes by layer.
func (e *agentExport) gather(u *agentUnit, n *resolve.Node) {
	if e.ownKind(n) {
		e.block(u, n)
		sort.SliceStable(u.items, func(i, j int) bool { return u.items[i].group < u.items[j].group })
		return
	}
	var walk func(n *resolve.Node)
	walk = func(n *resolve.Node) {
		for _, ch := range e.children(n) {
			if e.ownKind(ch) && e.owns[ch] {
				u.items = append(u.items, &agentItem{node: ch, group: groupSections, linked: true})
				continue
			}
			if e.container(ch) {
				if e.owns[ch] {
					u.items = append(u.items, &agentItem{node: ch, group: groupSections, linked: true})
					continue
				}
				u.items = append(u.items, &agentItem{node: ch, group: groupSections})
				u.inline = append(u.inline, ch)
				walk(ch)
				continue
			}
			e.block(u, ch)
		}
	}
	walk(n)
	sort.SliceStable(u.items, func(i, j int) bool { return u.items[i].group < u.items[j].group })
}

// separate reports whether a child of a node gets a heading of its own
// rather than being written with its parent.
func (e *agentExport) separate(ch, parent *resolve.Node) bool {
	if ch.Name == "" {
		return false
	}
	switch e.s.kindPath(ch) {
	case "spec.rule", "spec.constraint", "spec.quality", "spec.guidance", "spec.open", "spec.goal", "spec.nongoal",
		"spec.assumption", "doc.term", "spec.scenario", "spec.usecase", "types.op", "types.api", "spec.command",
		"spec.part", "spec.actor", "spec.persona", "spec.states", "grammar.grammar":
		return true
	case "spec.error":
		return e.s.kindPath(parent) != "spec.command"
	case "spec.example":
		return !strengthHolder(e.s.kindPath(parent)) && e.s.kindPath(parent) != "spec.command"
	}
	return e.s.isTypeDecl(ch) && !e.s.isTypeDecl(parent)
}

func strengthHolder(k string) bool {
	return k == "spec.rule" || k == "spec.constraint" || k == "spec.quality"
}

// block adds a node of a layer, and the nodes below it that get headings
// of their own.
func (e *agentExport) block(u *agentUnit, n *resolve.Node) {
	g := groupNotes
	switch specmodel.LayerOf(e.s.Checker, n) {
	case specmodel.Intent:
		g = groupIntent
	case specmodel.Specification:
		g = groupSpec
	case specmodel.Contract:
		g = groupContract
	}
	u.items = append(u.items, &agentItem{node: n, group: g})
	if e.s.summarized(n) {
		return
	}
	for _, ch := range e.children(n) {
		if e.separate(ch, n) {
			e.block(u, ch)
		}
	}
}

// partition splits a unit's entries into its files by its limit, and
// records the file of each node.
func (e *agentExport) partition(u *agentUnit) {
	u.parts = nil
	if u.limit <= 0 {
		u.parts = [][]*agentItem{u.items}
	} else {
		var cur []*agentItem
		size := 0
		for _, it := range u.items {
			n := len(it.text) + 64
			if len(cur) > 0 && size+n > u.limit {
				u.parts = append(u.parts, cur)
				cur, size = nil, 0
			}
			cur = append(cur, it)
			size += n
		}
		u.parts = append(u.parts, cur)
	}
	var names []string
	for i, part := range u.parts {
		name := u.base + ".md"
		if i > 0 {
			name = u.base + "." + strconv.Itoa(i+1) + ".md"
		}
		names = append(names, name)
		for _, it := range part {
			e.mark(it.node, name, it.linked)
		}
	}
	e.files[u] = names
	if _, ok := e.fileOf[u.node]; !ok || u.node == e.top {
		e.fileOf[u.node] = names[0]
	}
}

// mark records the file a node and the nodes written with it are in.
func (e *agentExport) mark(n *resolve.Node, file string, linked bool) {
	if linked {
		return
	}
	e.headed[n] = true
	if _, ok := e.fileOf[n]; !ok {
		e.fileOf[n] = file
	}
	if e.container(n) {
		return
	}
	var walk func(x *resolve.Node)
	walk = func(x *resolve.Node) {
		for _, ch := range x.Children {
			if _, ok := e.fileOf[ch]; ok || e.container(ch) {
				continue
			}
			if e.separate(ch, x) && ch.Name != "" {
				continue
			}
			e.fileOf[ch] = file
			walk(ch)
		}
	}
	walk(n)
}

// href is where a link to a node leads: its file and its id, relative to
// the file being written.
func (e *agentExport) href(n *resolve.Node) string {
	f := e.fileFor(n)
	if f == "" {
		return ""
	}
	for x := n; x != nil; x = x.Parent {
		if e.headed[x] || e.fileOf[x] != "" && e.container(x) || x == e.top {
			n = x
			break
		}
	}
	if e.pack != nil {
		if e.inPack(n) {
			return "#" + stableID(n)
		}
		f = "../" + f
	}
	return f + "#" + stableID(n)
}

// fileFor is the file that holds a node, or the nearest node above it
// that the export holds.
func (e *agentExport) fileFor(n *resolve.Node) string {
	for x := n; x != nil; x = x.Parent {
		if f, ok := e.fileOf[x]; ok {
			return f
		}
		if !own(x) {
			return ""
		}
	}
	return ""
}

// render writes each entry's text, for sizing and for the files.
func (e *agentExport) render() {
	for _, u := range e.units {
		for _, it := range u.items {
			k := agentKey{it.node, it.linked}
			t, ok := e.cache[k]
			if !ok {
				t = e.itemText(it)
				e.cache[k] = t
			}
			it.text = t
		}
	}
}

// split gives sections of their own to files over the budget, or splits
// files with none into parts, and reports whether anything changed.
func (e *agentExport) split() bool {
	limit := e.budget * 4
	changed := false
	for _, u := range e.units {
		for i := range u.parts {
			b := e.fileText(u, i)
			if len(b) <= limit {
				continue
			}
			if os.Getenv("AGENTS_DEBUG") != "" {
				fmt.Fprintln(os.Stderr, "over", u.base, i, len(b), len(u.parts), len(u.parts[i]), len(u.inline), u.limit)
			}
			if len(u.inline) > 0 {
				for _, x := range u.inline {
					if !e.owns[x] {
						e.owns[x] = true
						changed = true
					}
				}
				break
			}
			if len(u.parts[i]) < 2 {
				continue
			}
			// Parts: each as full as the budget allows, leaving room for
			// the heading, the links between parts and the terms.
			was := u.limit
			switch {
			case u.limit == 0:
				u.limit = limit * 85 / 100
			default:
				u.limit = u.limit * 9 / 10
			}
			if u.limit < limit/4 {
				u.limit = limit / 4
			}
			changed = changed || u.limit != was
			break
		}
	}
	return changed
}

// write writes the index, llms.txt and every file.
func (e *agentExport) write() []AgentFile {
	var files []AgentFile
	for _, u := range e.units {
		for i := range u.parts {
			b := e.fileText(u, i)
			if u.node == e.top && len(u.parts) == 1 && len(u.items) == 0 {
				// The top holds nothing but its sections, which the index
				// already introduces.
				continue
			}
			title := e.title(u)
			if i > 0 {
				title += fmt.Sprintf(" (part %d)", i+1)
			}
			sum := ""
			if i == 0 {
				sum = e.resolveLinks(e.summary(u.node))
			}
			files = append(files, AgentFile{Path: e.files[u][i], Title: title, ID: u.id, Summary: sum, Content: b})
		}
	}
	idx := AgentFile{Path: AgentIndex, Title: e.siteTitle(), Content: e.index(files)}
	llms := AgentFile{Path: LLMsTxt, Title: e.siteTitle(), Content: e.llms(files, "")}
	return append([]AgentFile{idx, llms}, files...)
}

func (e *agentExport) title(u *agentUnit) string {
	if u.node == e.top {
		return e.siteTitle()
	}
	return displayName(e.s, u.node)
}

// summary is the first sentence of a node's prose, as one line.
func (e *agentExport) summary(n *resolve.Node) string {
	t := strings.Join(strings.Fields(e.s.proseMD(n)), " ")
	return mdFirstSentence(t)
}

// mdFirstSentence cuts Markdown after its first sentence, outside code
// spans and link targets.
func mdFirstSentence(t string) string {
	if strings.HasPrefix(t, "|") || strings.HasPrefix(t, "```") {
		// A table or a code block, not a sentence.
		return ""
	}
	code, paren := false, 0
	for i := 0; i < len(t); i++ {
		switch c := t[i]; {
		case c == '`':
			code = !code
		case code:
		case c == '(':
			paren++
		case c == ')' && paren > 0:
			paren--
		case c == '.' && paren == 0 && (i+1 == len(t) || t[i+1] == ' '):
			return t[:i+1]
		}
	}
	if strings.HasPrefix(t, "```") {
		return ""
	}
	return t
}

// fileText writes one file of a unit.
func (e *agentExport) fileText(u *agentUnit, part int) []byte {
	var b strings.Builder
	title := e.title(u)
	if u.id != "" {
		title += " (`" + u.id + "`)"
	}
	if len(u.parts) > 1 {
		title += fmt.Sprintf(", part %d of %d", part+1, len(u.parts))
	}
	b.WriteString("# " + title + "\n\n")
	nav := "Agent export of " + e.siteTitle() + ": start at [" + AgentIndex + "](" + AgentIndex + ")."
	if part > 0 {
		nav += " Previous part: [" + e.files[u][part-1] + "](" + e.files[u][part-1] + ")."
	}
	if part+1 < len(u.parts) {
		nav += " Next part: [" + e.files[u][part+1] + "](" + e.files[u][part+1] + ")."
	}
	b.WriteString(nav + "\n")
	if part == 0 {
		if p := e.s.proseMD(u.node); p != "" && !e.ownKind(u.node) {
			b.WriteString("\n" + p + "\n")
		}
	}
	e.writeItems(&b, u.parts[part])
	e.writeTerms(&b, u.parts[part], u.node)
	return []byte(e.resolveLinks(b.String()))
}

// writeItems writes entries under their groups' headings.
func (e *agentExport) writeItems(b *strings.Builder, items []*agentItem) {
	g := -1
	for _, it := range items {
		if it.group != g {
			g = it.group
			b.WriteString("\n## " + groupTitles[g] + "\n")
		}
		b.WriteString("\n" + it.text + "\n")
	}
}

// itemText writes one entry: its heading and its body.
func (e *agentExport) itemText(it *agentItem) string {
	n := it.node
	s := e.s
	id := stableID(n)
	if it.group == groupSections {
		kind := "section"
		if k := s.Checker.KindOf(n); k != nil && e.ownKind(n) {
			kind = k.Name
		}
		head := "### " + kind + " " + id
		if t := displayName(s, n); t != "" {
			head += ": " + t
		}
		if it.linked {
			return head + "\n\nIn [" + e.ph('T', n) + "](" + e.ph('F', n) + ")" + summarySuffix(e.summary(n))
		}
		if p := s.proseMD(n); p != "" {
			return head + "\n\n" + p
		}
		return head
	}
	return e.nodeText(n)
}

func summarySuffix(s string) string {
	if s == "" {
		return "."
	}
	return ": " + s
}

// hrefFile is the link to the file a section owns.
func (e *agentExport) hrefFile(n *resolve.Node) string {
	f := e.fileOf[n]
	if e.pack != nil {
		return "../" + f
	}
	return f
}

// heading is a node's heading: its kind's name and its stable id.
func (e *agentExport) heading(n *resolve.Node) string {
	kind := "node"
	if k := e.s.Checker.KindOf(n); k != nil {
		kind = k.Name
	}
	id := stableID(n)
	if n.Name == "" && n.Parent != nil {
		id = "in " + stableID(n.Parent)
	}
	h := "### " + kind + " " + id
	if a := annotation(e.s, n, "display"); a != nil {
		h += ": " + displayName(e.s, n)
	}
	return h
}

// nodeText writes a node in the form of its kind.
func (e *agentExport) nodeText(n *resolve.Node) string {
	s := e.s
	var b strings.Builder
	b.WriteString(e.heading(n))
	body := func(t string) {
		if t = strings.TrimSpace(t); t != "" {
			b.WriteString("\n\n" + t)
		}
	}
	k := s.kindPath(n)
	if s.summarized(n) {
		if strengthHolder(k) {
			body(e.sentence(n))
		} else {
			body(e.summary(n))
		}
		return b.String()
	}
	switch {
	case strengthHolder(k):
		body(e.requirement(n))
	case k == "doc.term":
		body(e.term(n))
	case k == "spec.usecase":
		body(e.usecase(n))
	case k == "spec.scenario":
		body(e.scenario(n))
	case k == "spec.command":
		body(e.command(n))
	case k == "spec.error":
		body(e.errorNode(n))
	case k == "types.op":
		body(e.op(n))
	case k == "types.api":
		body(e.api(n))
	case s.isTypeDecl(n):
		body(e.typeDecl(n))
	case k == "spec.example":
		body(e.example(n))
	default:
		body(e.generic(n))
	}
	if d := e.derived(n); d != "" {
		// The derived lists join the node's list of links where it ends
		// with one.
		if t := b.String(); strings.HasPrefix(t[strings.LastIndex(t, "\n")+1:], "- ") {
			b.WriteString("\n" + d)
		} else {
			b.WriteString("\n\n" + d)
		}
	}
	return b.String()
}

// lines writes a list of lines, leaving out empty ones.
func lines(ls ...string) string {
	var out []string
	for _, l := range ls {
		if strings.TrimSpace(l) != "" {
			out = append(out, "- "+l)
		}
	}
	return strings.Join(out, "\n")
}

func (e *agentExport) links(ns []*resolve.Node) string {
	var out []string
	for _, n := range ns {
		out = append(out, e.s.mdLink(n, e.refWords(n)))
	}
	return strings.Join(out, ", ")
}

// refWords is how a linked node reads: a term by its word, anything else
// by its id.
func (e *agentExport) refWords(n *resolve.Node) string {
	if e.s.kindPath(n) == "doc.term" {
		return termName(e.s, n)
	}
	return stableID(n)
}

// sentence is a requirement's strength first, then its EARS sentence.
func (e *agentExport) sentence(n *resolve.Node) string {
	s := e.s
	strength, behaviour := "", ""
	for _, ch := range n.Children {
		if k := s.kindPath(ch); strengthKinds[k] {
			strength = strings.TrimPrefix(k, "spec.")
			behaviour = s.inlineMD(ch)
		}
	}
	if strength == "" {
		return s.inlineMD(n)
	}
	var lead []string
	for _, k := range leadConditions {
		if ch := s.childOfKind(n, k); ch != nil {
			lead = append(lead, kindWords(strings.TrimPrefix(k, "spec."))+" "+s.inlineMD(ch))
		}
	}
	subj := e.subject(n)
	var out string
	if len(lead) > 0 {
		out = mdCapital(strings.Join(lead, ", ")) + ", " + subj
	} else {
		out = mdCapital(subj)
	}
	out += " " + kindWords(strength) + " " + behaviour
	out = strings.TrimRight(out, " ")
	if !strings.HasSuffix(out, ".") {
		out += "."
	}
	return strength + ": " + out
}

func mdCapital(t string) string {
	if t == "" || t[0] < 'a' || t[0] > 'z' {
		return t
	}
	return string(t[0]-32) + t[1:]
}

// subject is who a requirement binds, in words.
func (e *agentExport) subject(n *resolve.Node) string {
	s := e.s
	if by := s.ruleBy(n); by != nil {
		return "the " + subjectWords(s, by)
	}
	for _, t := range s.targets(n) {
		if s.isTypeDecl(t) {
			w := kindWords(t.Name)
			if strings.ContainsRune("aeiou", rune(w[0])) {
				return "an " + w
			}
			return "a " + w
		}
	}
	return label("docs.rule.subject")
}

// requirement writes a rule, a constraint or a quality.
func (e *agentExport) requirement(n *resolve.Node) string {
	s := e.s
	var ls []string
	for _, c := range []string{"spec.then", "spec.instead", "spec.unless"} {
		if ch := s.childOfKind(n, c); ch != nil {
			ls = append(ls, strings.TrimPrefix(c, "spec.")+": "+s.inlineMD(ch))
		}
	}
	for _, ex := range s.childrenOfKind(n, "spec.example") {
		ls = append(ls, "example: "+s.inlineMD(ex))
	}
	if ap := s.childOfKind(n, "spec.applies"); ap != nil {
		ls = append(ls, "applies: "+s.inlineMD(ap))
	}
	for _, a := range []string{"reason", "rationale", "measure"} {
		if v, ok := argString(kindArgs(n), -1, a); ok && v != "" {
			ls = append(ls, a+": "+v)
		}
	}
	for _, ch := range n.Children {
		k := s.kindPath(ch)
		if ch.Name != "" || k == "" || strengthKinds[k] || strings.HasPrefix(k, "spec.") || k == "project.verify" {
			continue
		}
		ls = append(ls, kindWords(s.Checker.KindOf(ch).Name)+": "+s.inlineMD(ch))
	}
	var refs []string
	if by := s.ruleBy(n); by != nil {
		refs = append(refs, "by "+s.mdLink(by, stableID(by)))
	}
	if ts := s.targets(n); len(ts) > 0 {
		refs = append(refs, "targets "+e.links(ts))
	}
	for _, a := range []string{"serves", "derives", "relies"} {
		if ts := s.argRefs(n, a); len(ts) > 0 {
			refs = append(refs, a+" "+e.links(ts))
		}
	}
	if len(refs) > 0 {
		ls = append(ls, strings.Join(refs, " · "))
	}
	out := e.sentence(n)
	if l := lines(ls...); l != "" {
		out += "\n\n" + l
	}
	if p := e.ownProse(n); p != "" {
		out += "\n\n" + p
	}
	return out
}

// ownProse is a node's prose besides what its children say.
func (e *agentExport) ownProse(n *resolve.Node) string {
	if n.Value != nil && strengthHolder(e.s.kindPath(n)) {
		return ""
	}
	return e.s.proseMD(n)
}

func (e *agentExport) term(n *resolve.Node) string {
	s := e.s
	head := "**" + termName(s, n) + "**"
	if al := termAliases(n); len(al) > 0 {
		head += " (also: " + strings.Join(al, ", ") + ")"
	}
	return head + ": " + s.proseMD(n)
}

func (e *agentExport) scenario(n *resolve.Node) string {
	s := e.s
	var ls []string
	for _, k := range []string{"spec.given", "spec.when", "spec.then"} {
		for _, ch := range s.childrenOfKind(n, k) {
			ls = append(ls, strings.TrimPrefix(k, "spec.")+": "+s.inlineMD(ch))
		}
	}
	if t := s.argRef(n, -1, "illustrates"); t != nil {
		ls = append(ls, "illustrates "+s.mdLink(t, stableID(t)))
	}
	if ts := s.argRefs(n, "serves"); len(ts) > 0 {
		ls = append(ls, "serves "+e.links(ts))
	}
	out := lines(ls...)
	var rest []string
	for _, ch := range n.Children {
		if k := s.kindPath(ch); k != "spec.given" && k != "spec.when" && k != "spec.then" && ch.Name == "" && ch.Value == nil {
			continue
		}
	}
	if p := s.proseMD(n); p != "" {
		rest = append(rest, p)
	}
	return strings.TrimSpace(out + "\n\n" + strings.Join(rest, "\n\n"))
}

// usecase writes a use case as its facts, then one typed line per step and
// extension.
func (e *agentExport) usecase(n *resolve.Node) string {
	s := e.s
	var facts []string
	if a := s.argRef(n, 0, "actor"); a != nil {
		facts = append(facts, "actor "+s.mdLink(a, stableID(a)))
	}
	level := "USER"
	if x := namedArg(kindArgs(n), "level"); x != nil {
		level = variantName(x)
	}
	facts = append(facts, "level "+level)
	if ts := s.argRefs(n, "serves"); len(ts) > 0 {
		facts = append(facts, "serves "+e.links(ts))
	}
	ls := []string{strings.Join(facts, " · ")}
	for _, a := range []string{"success", "minimal"} {
		if v, ok := argString(kindArgs(n), -1, a); ok {
			ls = append(ls, a+": "+v)
		}
	}
	out := lines(ls...)
	p, ok := specmodel.ParseUseCase(s.Checker, n)
	if !ok || len(p.Steps) == 0 {
		return out + "\n\n" + s.proseMD(n)
	}
	body := p.Site
	bs, _ := body.N.Span()
	if first := p.Steps[0].Start; first > bs {
		intro := s.rangeMD(body, bs, lineStart(body.File.Src, first))
		intro = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(stripBraces(intro)), "{{"))
		if intro != "" {
			out += "\n\n" + intro
		}
	}
	step := func(kind, num string, start, end int, extra ...string) string {
		f := []string{kind + " " + num}
		f = append(f, extra...)
		by, uses, includes := e.stepLinks(p, start, end)
		if by != nil {
			f = append(f, "by "+s.mdLink(by, stableID(by)))
		}
		if len(uses) > 0 {
			f = append(f, "uses "+e.links(uses))
		}
		if len(includes) > 0 {
			f = append(f, "includes "+e.links(includes))
		}
		return strings.Join(f, " · ") + ": " + s.rangeMD(body, start, end)
	}
	var sl []string
	for _, st := range p.Steps {
		sl = append(sl, step("step", st.Num, st.Start, st.End))
	}
	for _, x := range p.Exts {
		var parts []string
		for _, st := range x.Steps {
			parts = append(parts, s.rangeMD(body, st.Start, st.End))
		}
		by, uses, includes := e.stepLinks(p, x.Start, x.End)
		f := []string{"extension " + x.Num, "at step " + strconv.Itoa(x.At), "if " + strings.TrimSpace(x.Cond)}
		if by != nil {
			f = append(f, "by "+s.mdLink(by, stableID(by)))
		}
		if len(uses) > 0 {
			f = append(f, "uses "+e.links(uses))
		}
		if len(includes) > 0 {
			f = append(f, "includes "+e.links(includes))
		}
		if o := outcomeLabel(x); o != "" {
			f = append(f, "outcome "+strings.ToLower(strings.TrimSuffix(o, ".")))
		}
		line := strings.Join(f, " · ")
		if len(parts) > 0 {
			line += ": " + strings.Join(parts, " ")
		}
		sl = append(sl, line)
	}
	return out + "\n\n" + lines(sl...)
}

// stepLinks are a step's party, the rules it uses and the use cases it
// includes, in the order written.
func (e *agentExport) stepLinks(p specmodel.Parsed, start, end int) (by *resolve.Node, uses, includes []*resolve.Node) {
	var offs []int
	for at := start; at < end; at++ {
		if p.Target(at) != nil {
			offs = append(offs, at)
		}
	}
	for i, at := range offs {
		t := p.Target(at)
		switch k := e.s.kindPath(t); {
		case i == 0 && (k == "spec.actor" || k == "spec.part" || k == "spec.persona"):
			by = t
		case k == "spec.usecase":
			includes = append(includes, t)
		case k == "spec.rule" || k == "spec.constraint" || k == "doc.term":
			uses = append(uses, t)
		}
	}
	return
}

// command writes a command's usage, arguments, options and exits.
func (e *agentExport) command(n *resolve.Node) string {
	s := e.s
	var out []string
	if u := n.Child("usage"); u != nil && u.Value != nil {
		var ls []string
		for _, x := range arrayItems(u.Value) {
			if v, ok := resolve.StringValue(x.Syntax()); ok {
				ls = append(ls, v)
			}
		}
		if len(ls) > 0 {
			out = append(out, "Usage:\n\n```sh\n"+strings.Join(ls, "\n")+"\n```")
		}
	}
	if p := s.proseMD(n); p != "" {
		out = append(out, p)
	}
	var ls []string
	for _, a := range s.childrenOfKind(n, "spec.argument") {
		an, _ := argString(kindArgs(a), 0, "name")
		shown := "<" + an + ">"
		if boolArg(a, "variadic") {
			shown += "…"
		}
		if boolArg(a, "optional") {
			shown = "[" + shown + "]"
		}
		l := "argument `" + shown + "`"
		if t := e.argText(a, "type", 1); t != "" {
			l += " " + t
		}
		ls = append(ls, withDoc(l, s.proseMD(a)))
	}
	for _, o := range s.childrenOfKind(n, "spec.option") {
		on, _ := argString(kindArgs(o), 0, "name")
		l := "option `" + on + "`"
		if t := e.argText(o, "type", 1); t != "" {
			l += " " + t
		}
		if d := e.argText(o, "default", 2); d != "" {
			l += ", default `" + d + "`"
		}
		ls = append(ls, withDoc(l, s.proseMD(o)))
	}
	errs := append(s.childrenOfKind(n, "spec.error"), s.argRefList(n, "errors")...)
	for _, x := range errs {
		code := ""
		if v := namedArg(kindArgs(x), "exit"); v != nil {
			code = strings.TrimSpace(v.Syntax().Text())
		}
		title := ""
		if t := x.Child("title"); t != nil && t.Value != nil {
			title, _ = resolve.StringValue(t.Value.Syntax())
		}
		ls = append(ls, "exit "+code+": "+title)
	}
	for _, ex := range s.childrenOfKind(n, "spec.example") {
		ls = append(ls, "example: "+s.inlineMD(ex))
	}
	if l := lines(ls...); l != "" {
		out = append(out, l)
	}
	return strings.Join(out, "\n\n")
}

func withDoc(l, doc string) string {
	doc = strings.TrimSpace(doc)
	if doc == "" {
		return l
	}
	if !strings.Contains(doc, "\n") {
		return l + ": " + doc
	}
	// A doc of several lines or blocks continues the list item.
	ls := strings.Split(doc, "\n")
	for i, x := range ls {
		if x != "" {
			ls[i] = "  " + x
		}
	}
	return l + ":\n\n" + strings.Join(ls, "\n")
}

// argText is a kind argument as written, a string without its quotes.
func (e *agentExport) argText(n *resolve.Node, name string, index int) string {
	args := kindArgs(n)
	x := namedArg(args, name)
	if x == nil {
		es := argExprs(args)
		if index < len(es) {
			x = es[index]
		}
	}
	if x == nil {
		return ""
	}
	if v, ok := resolve.StringValue(x.Syntax()); ok {
		return v
	}
	return strings.TrimSpace(x.Syntax().Text())
}

// errorNode writes an error node with its projections and fields.
func (e *agentExport) errorNode(n *resolve.Node) string {
	s := e.s
	args := kindArgs(n)
	id, _ := argString(args, 0, "id")
	f := []string{"id `" + id + "`"}
	sev := "error"
	if x := namedArg(args, "severity"); x != nil {
		sev = variantName(x)
	}
	f = append(f, "severity "+sev)
	if x := namedArg(args, "status"); x != nil {
		f = append(f, "HTTP "+strings.TrimSpace(x.Syntax().Text()))
	}
	if x := namedArg(args, "exit"); x != nil {
		f = append(f, "exit "+strings.TrimSpace(x.Syntax().Text()))
	}
	g, j, sp := e.rpcCodes(n)
	if g != "" {
		f = append(f, "gRPC "+g)
	}
	if j != "" {
		f = append(f, "JSON-RPC "+j)
	}
	if sp != "" {
		f = append(f, "Sparrow "+sp)
	}
	if by := s.ruleBy(n); by != nil {
		f = append(f, "by "+s.mdLink(by, stableID(by)))
	}
	ls := []string{strings.Join(f, " · ")}
	if t := n.Child("title"); t != nil && t.Value != nil {
		v, _ := resolve.StringValue(t.Value.Syntax())
		ls = append(ls, "title: "+v)
	}
	for _, ch := range n.Children {
		if s.kindPath(ch) == "spec.field" {
			ls = append(ls, "field `"+ch.Name+"` "+e.argText(ch, "type", 0))
		}
	}
	var realizes []*resolve.Node
	for _, a := range annotationsNamed(n, "realizes") {
		for _, x := range argExprs(a.Args()) {
			if t := s.refTarget(x.Syntax(), n.Decl); t != nil {
				realizes = append(realizes, t)
			}
		}
	}
	if len(realizes) > 0 {
		ls = append(ls, "realizes "+e.links(realizes))
	}
	out := lines(ls...)
	if p := s.proseMD(n); p != "" {
		out += "\n\n" + p
	}
	return out
}

// rpcCodes are the gRPC, JSON-RPC and Sparrow codes an error has on the
// tree's bindings, where an op answers it.
func (e *agentExport) rpcCodes(n *resolve.Node) (grpc, jsonrpc, sparrow string) {
	m := e.s.rpc()
	for _, svc := range m.Services {
		for _, meth := range svc.Methods {
			for _, x := range meth.Errors {
				if x.Node == n && x.GRPC != "" && grpc == "" {
					grpc = x.GRPC
				}
			}
		}
	}
	for _, api := range m.JSONAPIs {
		for _, meth := range api.Methods {
			for _, x := range meth.Errors {
				if x.Node == n && x.JSONRPC != "" && jsonrpc == "" {
					jsonrpc = x.JSONRPC
				}
			}
		}
	}
	for _, f := range m.Facets {
		for _, svc := range f.Services {
			for _, meth := range svc.Methods {
				for i := range meth.Errors {
					if x := &meth.Errors[i]; x.Node == n && sparrow == "" {
						sparrow = sparrowCode(x)
					}
				}
			}
		}
	}
	return
}

// sparrowCode is an error's Sparrow status: its core code, as not_found
// (5), or the sigil of its own, as sigil InvalidAddress (80).
func sparrowCode(x *rpc.Error) string {
	if x.Sparrow != "" {
		n, _ := rpc.SparrowCode(x.Sparrow)
		return x.Sparrow + " (" + strconv.FormatUint(n, 10) + ")"
	}
	if x.SparrowSigil != nil {
		return "sigil " + x.SparrowSigil.Name + " (" + strconv.FormatUint(x.SparrowSigil.ID, 10) + ")"
	}
	return ""
}

// api writes an api's base path and its bindings.
func (e *agentExport) api(n *resolve.Node) string {
	s := e.s
	var ls []string
	if b := basePath(s, n); b != "" {
		ls = append(ls, "HTTP base path `"+b+"`")
	}
	if w := wsRoute(n); w != "" {
		ls = append(ls, "websocket `"+w+"`")
	}
	if svc := s.rpc().ServiceOf(n); svc != nil {
		ls = append(ls, "gRPC service `"+svc.FullName()+"` over "+strings.Join(svc.Protocols, ", "))
	}
	if j := s.rpc().JSONAPIOf(n); j != nil {
		ls = append(ls, "JSON-RPC over "+j.Framing+", "+j.Encoding)
	}
	if svc := s.rpc().SparrowServiceOf(n); svc != nil {
		f := svc.Facet
		ls = append(ls, "Sparrow service `"+svc.Name+"` (id "+strconv.FormatUint(svc.ID, 10)+") of facet `"+f.Name+"` "+f.Version+" (id "+strconv.FormatUint(f.ID, 10)+", hash `"+f.HashHex()+"`)")
	}
	out := lines(ls...)
	if p := s.proseMD(n); p != "" {
		out = strings.TrimSpace(out + "\n\n" + p)
	}
	return out
}

// op writes an op as its signature, its bindings and its errors with
// their projections.
func (e *agentExport) op(n *resolve.Node) string {
	s := e.s
	o := e.ops[n]
	if o == nil {
		o = s.readOp(n)
		e.ops[n] = o
	}
	var params []string
	for _, p := range o.params {
		t := p.typ.text
		if t == "" {
			t = p.typ.name
		}
		x := p.name
		if p.optional {
			x += "?"
		}
		x += ": " + t
		switch {
		case p.loc == "header":
			x = "@header(" + strconv.Quote(p.header) + ") " + x
		case p.loc != "":
			x = "@" + p.loc + " " + x
		}
		params = append(params, x)
	}
	ret := o.returns.text
	if ret == "" {
		ret = o.returns.name
	}
	sig := n.Name + "(" + strings.Join(params, ", ") + ")"
	if ret != "" {
		sig += ": " + ret
	}
	out := "```heron\n" + sig + "\n```"
	var ls []string
	if o.method != "" {
		ls = append(ls, "HTTP "+o.method+" `"+o.base+o.path+"`")
	}
	if w := wsRoute(n); w != "" {
		ls = append(ls, "websocket `"+o.base+w+"`")
	}
	meth := s.rpc().Method(n)
	if meth != nil {
		ls = append(ls, "gRPC `"+meth.Path()+"` ("+meth.Kind.Key()+")")
	}
	jm := s.rpc().JSONMethod(n)
	if jm != nil {
		ls = append(ls, "JSON-RPC method `"+jm.Name+"`")
	}
	sm := s.rpc().SparrowMethod(n)
	if sm != nil {
		x := "Sparrow method `" + sm.Service.Facet.Name + "/" + sm.Name + "` (id " + strconv.FormatUint(sm.ID, 10) + ", " + sm.Shape() + ", `" + sm.Input.String() + "` → `" + sm.Output.String() + "`"
		if fl := sm.FlagNames(); len(fl) > 0 {
			x += ", " + strings.Join(fl, ", ")
		}
		ls = append(ls, x+")")
	}
	for _, p := range o.params {
		if p.doc != nil {
			if d := e.docLine(p.doc, p.site); d != "" {
				ls = append(ls, "param `"+p.name+"`: "+d)
			}
		}
	}
	for _, er := range o.errors {
		f := []string{"throws " + s.mdLink(er.typ, stableID(er.typ))}
		if er.code != "" {
			f = append(f, "id `"+er.code+"`")
		}
		if er.status != "" {
			f = append(f, "HTTP "+er.status)
		}
		if meth != nil {
			for _, x := range meth.Errors {
				if x.Node == er.typ && x.GRPC != "" {
					f = append(f, "gRPC "+x.GRPC)
					break
				}
			}
		}
		if jm != nil {
			for _, x := range jm.Errors {
				if x.Node == er.typ && x.JSONRPC != "" {
					f = append(f, "JSON-RPC "+x.JSONRPC)
					break
				}
			}
		}
		if sm != nil {
			for i := range sm.Errors {
				if x := &sm.Errors[i]; x.Node == er.typ {
					if c := sparrowCode(x); c != "" {
						f = append(f, "Sparrow "+c)
					}
					break
				}
			}
		}
		if x := namedArg(kindArgs(er.typ), "exit"); x != nil && s.kindPath(er.typ) == "spec.error" {
			f = append(f, "exit "+strings.TrimSpace(x.Syntax().Text()))
		}
		ls = append(ls, strings.Join(f, " · "))
	}
	if len(o.realizes) > 0 {
		ls = append(ls, "realizes "+e.links(o.realizes))
	}
	if len(o.performs) > 0 {
		ls = append(ls, "performs "+e.links(o.performs))
	}
	for _, ex := range o.examples {
		var vs []string
		for _, ch := range ex.node.Children {
			if ch.Value != nil {
				vs = append(vs, ch.Name+" = "+strings.Join(strings.Fields(ch.Value.Syntax().Text()), " "))
			}
		}
		name := ex.node.Name
		if name == "" {
			name = "example"
		}
		ls = append(ls, "example "+name+": "+strings.Join(vs, "; "))
	}
	if l := lines(ls...); l != "" {
		out += "\n\n" + l
	}
	if p := s.proseMD(n); p != "" {
		out = p + "\n\n" + out
	}
	return out
}

// docLine is a parameter's doc comment as one line.
func (e *agentExport) docLine(doc *syntax.Node, site resolve.Site) string {
	p := &proseBuf{s: e.s, site: resolve.Site{File: site.File, N: doc, Scope: site.Scope}, md: true}
	p.container(doc)
	return strings.Join(strings.Fields(p.mdBlocks()), " ")
}

// typeDecl writes a type as its doc text, then its declaration without
// its doc comments.
func (e *agentExport) typeDecl(n *resolve.Node) string {
	s := e.s
	var out []string
	if p := s.proseMD(n); p != "" {
		out = append(out, p)
	}
	if n.Decl.N != nil {
		var b strings.Builder
		writeSkipping(&b, n.Decl.N, func(x *syntax.Node) bool { return x.Kind == syntax.Doc })
		src := dedent(b.String(), declIndent(resolve.Site{File: n.Decl.File, N: n.Decl.N}))
		var keep []string
		for _, l := range strings.Split(src, "\n") {
			if t := strings.TrimSpace(l); t == "" || strings.HasPrefix(t, "///") {
				continue
			}
			keep = append(keep, strings.TrimRight(l, " \t"))
		}
		out = append(out, "```heron\n"+strings.Join(keep, "\n")+"\n```")
	}
	return strings.Join(out, "\n\n")
}

// writeSkipping writes a syntax node's text without the nodes skip
// accepts.
func writeSkipping(b *strings.Builder, n *syntax.Node, skip func(*syntax.Node) bool) {
	for _, ch := range n.Children {
		switch x := ch.(type) {
		case *syntax.Token:
			b.WriteString(x.Text)
		case *syntax.Node:
			if !skip(x) {
				writeSkipping(b, x, skip)
			}
		}
	}
}

// example writes an example node as its prose and code blocks, as written.
func (e *agentExport) example(n *resolve.Node) string {
	return e.s.proseMD(n)
}

// generic writes a node of a kind with no form of its own: its prose and
// the arguments and fields it sets.
func (e *agentExport) generic(n *resolve.Node) string {
	s := e.s
	var ls []string
	attrs := s.kindAttrs(n)
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	names := s.paramNames(s.Checker.KindOf(n))
	order := map[string]int{}
	for i, x := range names {
		order[x] = i
	}
	sort.SliceStable(keys, func(i, j int) bool { return order[keys[i]] < order[keys[j]] })
	args := kindArgs(n)
	for _, k := range keys {
		if x := namedArg(args, k); x != nil {
			ls = append(ls, k+": "+e.valueMD(x, n.Decl))
			continue
		}
		for i, x := range argExprs(args) {
			if i < len(names) && names[i] == k {
				ls = append(ls, k+": "+e.valueMD(x, n.Decl))
			}
		}
	}
	for _, ch := range n.Children {
		if ch.Value != nil && (ch.Shape == resolve.ShapeSet || ch.Shape == resolve.ShapeValue || e.s.kindPath(ch) == "") && proseValue(ch.Value.Syntax()) == nil {
			ls = append(ls, ch.Name+": "+e.valueMD(ch.Value, ch.ValueSite))
		}
	}
	for _, ch := range n.Children {
		if ch.Name == "" || e.separate(ch, n) || ch.Value != nil && proseValue(ch.Value.Syntax()) == nil {
			continue
		}
		if k := s.Checker.KindOf(ch); k != nil {
			if t := s.inlineMD(ch); t != "" {
				ls = append(ls, k.Name+" `"+ch.Name+"`: "+t)
			}
		}
	}
	out := ""
	if p := s.proseMD(n); p != "" {
		out = p
	}
	if l := lines(ls...); l != "" {
		out = strings.TrimSpace(out + "\n\n" + l)
	}
	return out
}

// valueMD is an expression as written, a reference as a link.
func (e *agentExport) valueMD(x ast.Expr, site resolve.Site) string {
	if pe, ok := x.(ast.PathExpr); ok {
		if t := e.s.refTarget(pe.Path().N, site); t != nil && own(t) {
			return e.s.mdLink(t, stableID(t))
		}
	}
	if v, ok := resolve.StringValue(x.Syntax()); ok {
		return v
	}
	return "`" + strings.Join(strings.Fields(x.Syntax().Text()), " ") + "`"
}

var derivedKeys = []struct{ key, words string }{
	{"realized_by", "realized by"}, {"illustrated_by", "illustrated by"}, {"served_by", "served by"},
	{"refined_by", "refined by"}, {"relied_on_by", "relied on by"}, {"derived_by", "derived by"},
}

// derived writes a node's derived lists, each marked as derived.
func (e *agentExport) derived(n *resolve.Node) string {
	m := e.s.specLinks()[n]
	if len(m) == 0 {
		return ""
	}
	var ls []string
	for _, k := range derivedKeys {
		ns := append([]*resolve.Node(nil), m[k.key]...)
		sort.SliceStable(ns, func(i, j int) bool { return stableID(ns[i]) < stableID(ns[j]) })
		var keep []*resolve.Node
		for _, x := range ns {
			if own(x) && !e.s.hidden(x) {
				keep = append(keep, x)
			}
		}
		if len(keep) > 0 {
			ls = append(ls, k.words+" (derived): "+e.links(keep))
		}
	}
	return lines(ls...)
}

// writeTerms lists the terms a file's entries link that it doesn't
// define.
func (e *agentExport) writeTerms(b *strings.Builder, items []*agentItem, unit *resolve.Node) {
	defined := map[*resolve.Node]bool{}
	var nodes []*resolve.Node
	for _, it := range items {
		defined[it.node] = true
		if !it.linked {
			nodes = append(nodes, it.node)
		}
	}
	nodes = append(nodes, unit)
	seen := map[*resolve.Node]bool{}
	var terms []*resolve.Node
	for _, n := range nodes {
		for _, t := range e.linkedTerms(n, unit) {
			if !defined[t] && !seen[t] {
				seen[t] = true
				terms = append(terms, t)
			}
		}
	}
	if len(terms) == 0 {
		return
	}
	sort.SliceStable(terms, func(i, j int) bool {
		a, b := strings.ToLower(termName(e.s, terms[i])), strings.ToLower(termName(e.s, terms[j]))
		if a != b {
			return a < b
		}
		return stableID(terms[i]) < stableID(terms[j])
	})
	b.WriteString("\n## Terms used here\n\n")
	var ls []string
	for _, t := range terms {
		ls = append(ls, e.s.mdLink(t, termName(e.s, t))+": "+e.summary(t))
	}
	b.WriteString(lines(ls...) + "\n")
}

// linkedTerms are the terms a node's own prose and arguments link, and
// its children's that are written with it.
func (e *agentExport) linkedTerms(n, unit *resolve.Node) (out []*resolve.Node) {
	if ts, ok := e.terms[n]; ok && n != unit {
		return ts
	}
	defer func() {
		if n != unit {
			e.terms[n] = out
		}
	}()
	var visit func(x *resolve.Node, top bool)
	visit = func(x *resolve.Node, top bool) {
		var sites []resolve.Site
		sites = append(sites, x.Prose...)
		sites = append(sites, x.Docs...)
		if x.Value != nil {
			sites = append(sites, x.ValueSite)
		}
		sites = append(sites, e.argSites(x)...)
		for _, site := range sites {
			if site.N == nil {
				continue
			}
			site.N.Walk(func(y *syntax.Node) bool {
				if y.Kind == syntax.Path {
					if t := e.s.Checker.Refs[y]; t != nil && e.s.kindPath(t) == "doc.term" && own(t) && t != x {
						out = append(out, t)
					}
					return false
				}
				if y.Kind == syntax.Tag && y != site.N && e.s.Tree.ElementNode(y) != nil {
					return false
				}
				return true
			})
		}
		if x == unit || e.container(x) && !top {
			return
		}
		for _, ch := range x.Children {
			if ch.Name != "" && e.separate(ch, x) || e.container(ch) {
				continue
			}
			visit(ch, false)
		}
	}
	visit(n, true)
	return out
}

// index writes INDEX.md: what the product is, how to read the files and
// their map.
func (e *agentExport) index(files []AgentFile) []byte {
	s := e.s
	var b strings.Builder
	b.WriteString("# " + e.siteTitle() + ": agent export\n\n")
	b.WriteString("The specification of " + e.siteTitle() + " for agents, as plain Markdown files written from its Heron tree. Read this file first, then open the one file whose summary matches the task.\n")
	top := e.top
	var about []string
	if p := s.proseMD(top); p != "" {
		about = append(about, p)
	}
	var ls []string
	for _, ch := range e.children(top) {
		switch s.kindPath(ch) {
		case "spec.goal":
			l := "goal " + s.mdLink(ch, stableID(ch)) + ": " + strings.Join(strings.Fields(s.inlineMD(ch)), " ")
			if v, ok := argString(kindArgs(ch), -1, "measure"); ok {
				l += " Measure: " + v
			}
			ls = append(ls, l)
		case "spec.nongoal":
			ls = append(ls, "nongoal "+s.mdLink(ch, stableID(ch))+": "+strings.Join(strings.Fields(s.inlineMD(ch)), " "))
		}
	}
	if l := lines(ls...); l != "" {
		about = append(about, l)
	}
	if len(about) > 0 {
		b.WriteString("\n## What it is\n\n" + strings.Join(about, "\n\n") + "\n")
	}
	b.WriteString(`
## How to read these files

- Each file holds one area of the tree: a section, a folder, a command or an api, named by its id, as ` + "`language.apis.grpc.md`" + `. A file over the size budget of ` + strconv.Itoa(e.budget) + ` tokens is split into parts, ` + "`<id>.2.md`" + ` and on, each linking the next.
- A file opens with its title, its id and its context. Then come its parts, in this order: Sections (the context of each section it holds, or a link to the section's own file), Intent (goals, actors, qualities: why), Specification (rules, terms, use cases, scenarios: what the product guarantees), Contract (errors, commands, ops, types, grammars and examples: the exact surface), Notes (guidance and open questions), and Terms used here (the words it links that other files define). Within a part, nodes are in the order the tree writes them.
- Every node has a heading of its kind and its full id, as ` + "`### rule language.apis.grpc.numbering.unique_numbers`" + `. An id is the node's path in the tree, dots between its scopes; it's stable, so search for it to find the node.
- A requirement opens with its strength: must, mustNot, should, shouldNot or may, as RFC 2119 means them. Its conditions read in the order where, while, when, if; ` + "`then`" + ` says what follows, and ` + "`instead`" + ` what to do in its place. Its last line links who it binds (by), what it's about (targets) and the goals it serves.
- A link leads to a file and to the id of the node, as ` + "`[x](file.md#id)`" + `. A name followed by an id in code isn't in these files.
- Lists marked (derived), such as realized by and illustrated by, are worked out from links written on other nodes, never written on this one.
- Sizes are approximate tokens: a file's bytes over four.
`)
	b.WriteString("\n## Files\n\n")
	var fl []string
	for _, f := range files {
		l := "[" + mdLinkText(f.Title) + "](" + f.Path + ")"
		if f.Summary != "" {
			l += ": " + clipLine(unlink(f.Summary), summaryWidth)
		}
		l += " (~" + strconv.Itoa(Tokens(f.Content)) + " tokens)"
		fl = append(fl, e.indent(f)+"- "+l)
	}
	b.WriteString(strings.Join(fl, "\n") + "\n")
	return []byte(e.resolveLinks(b.String()))
}

// indent nests a file in the map under the file of its section.
func (e *agentExport) indent(f AgentFile) string {
	if f.ID == "" {
		return ""
	}
	return strings.Repeat("  ", strings.Count(f.ID, "."))
}

// llms writes llms.txt, its links under prefix.
func (e *agentExport) llms(files []AgentFile, prefix string) []byte {
	var b strings.Builder
	b.WriteString("# " + e.siteTitle() + "\n\n")
	if sum := unlink(e.resolveLinks(e.summary(e.top))); sum != "" {
		b.WriteString("> " + sum + "\n\n")
	}
	b.WriteString("The specification as Markdown for agents: read the index first, then the file a task needs.\n\n## Spec\n\n")
	b.WriteString("- [Index](" + prefix + AgentIndex + "): what it is, how to read the files, and the map of them\n")
	for _, f := range files {
		l := "- [" + f.Title + "](" + prefix + f.Path + ")"
		if f.Summary != "" {
			l += ": " + unlink(f.Summary)
		}
		b.WriteString(l + "\n")
	}
	return []byte(b.String())
}

// LLMsFor is llms.txt for a site that holds the export under prefix, as
// "agents/".
func LLMsFor(files []AgentFile, prefix string) []byte {
	for _, f := range files {
		if f.Path == LLMsTxt {
			c := string(f.Content)
			c = strings.ReplaceAll(c, "]("+AgentIndex+")", "]("+prefix+AgentIndex+")")
			for _, g := range files {
				if g.Path != AgentIndex && g.Path != LLMsTxt {
					c = strings.ReplaceAll(c, "]("+g.Path+")", "]("+prefix+g.Path+")")
				}
			}
			return []byte(c)
		}
	}
	return nil
}

// inPack reports whether a node is in the pack's subtree.
func (e *agentExport) inPack(n *resolve.Node) bool {
	for x := n; x != nil; x = x.Parent {
		if x == e.pack.node {
			return true
		}
	}
	return false
}

// writePack writes the context pack of a node: its subtree as one file,
// never split, then the surface of what it links outside it.
func (e *agentExport) writePack(n *resolve.Node) AgentFile {
	u := &agentUnit{node: n, id: stableID(n), base: stableID(n)}
	if e.container(n) || n == e.top {
		saved := e.owns
		e.owns = map[*resolve.Node]bool{}
		e.gather(u, n)
		e.owns = saved
	} else {
		e.block(u, n)
	}
	e.pack = u
	defer func() { e.pack = nil }()
	for _, it := range u.items {
		it.text = e.itemText(it)
	}
	u.parts = [][]*agentItem{u.items}
	e.files[u] = []string{packDir + u.base + ".md"}
	var b strings.Builder
	b.WriteString("# Context pack: " + displayName(e.s, n) + " (`" + u.id + "`)\n\n")
	b.WriteString("One part of " + e.siteTitle() + "'s agent export, in full: [the index](../" + AgentIndex + ") maps the rest.\n")
	if p := e.s.proseMD(n); p != "" && e.container(n) {
		b.WriteString("\n" + p + "\n")
	}
	e.writeItems(&b, u.items)
	e.writeTerms(&b, u.items, n)
	// What the subtree links outside it, once, in the order of the tree.
	out := map[*resolve.Node]bool{}
	for _, it := range u.items {
		for _, t := range e.outLinks(it.node) {
			if !e.inPack(t) && own(t) && t.Name != "" && e.s.kindPath(t) != "doc.term" {
				out[t] = true
			}
		}
	}
	var ext []*resolve.Node
	e.s.Tree.Walk(func(x *resolve.Node) {
		if out[x] {
			ext = append(ext, x)
		}
	})
	if len(ext) > 0 {
		b.WriteString("\n## Linked from here\n")
		for _, t := range ext {
			line := ""
			if strengthHolder(e.s.kindPath(t)) {
				line = e.sentence(t)
			} else {
				line = e.summary(t)
			}
			b.WriteString("\n" + e.heading(t) + "\n\n")
			if line != "" {
				b.WriteString(line + " ")
			}
			b.WriteString("In " + e.s.mdLink(t, "its file") + ".\n")
		}
	}
	return AgentFile{Path: packDir + u.base + ".md", Title: displayName(e.s, n), ID: u.id, Summary: e.summary(n), Content: []byte(e.resolveLinks(b.String()))}
}

// outLinks are the nodes a node's text and arguments name, in the order
// written.
func (e *agentExport) outLinks(n *resolve.Node) []*resolve.Node {
	var out []*resolve.Node
	var sites []resolve.Site
	sites = append(sites, n.Prose...)
	sites = append(sites, n.Docs...)
	sites = append(sites, e.argSites(n)...)
	for _, ch := range n.Children {
		if !e.separate(ch, n) && !e.container(ch) {
			sites = append(sites, ch.Prose...)
		}
	}
	for _, site := range sites {
		if site.N == nil {
			continue
		}
		site.N.Walk(func(y *syntax.Node) bool {
			if y.Kind == syntax.Path {
				if t := e.s.Checker.Refs[y]; t != nil {
					out = append(out, t)
				}
				return false
			}
			return true
		})
	}
	return out
}

// argSites are the syntax of a node's kind arguments and annotations, or
// of its whole declaration for a type.
func (e *agentExport) argSites(n *resolve.Node) []resolve.Site {
	if n.Decl.N == nil {
		return nil
	}
	if e.s.isTypeDecl(n) {
		return []resolve.Site{{File: n.Decl.File, N: n.Decl.N, Scope: n.Decl.Scope}}
	}
	var out []resolve.Site
	if a := kindArgs(n); a.N != nil {
		out = append(out, resolve.Site{File: n.Decl.File, N: a.N, Scope: n.Decl.Scope})
	}
	for _, a := range n.Annotations {
		out = append(out, a)
	}
	return out
}

var mdLinkTextRe = regexp.MustCompile(`\[([^\]]*)\]\([^)\s]*\)`)

// unlink writes Markdown links as their text, for a line that's read
// from another folder.
func unlink(t string) string { return mdLinkTextRe.ReplaceAllString(t, "$1") }

// summaryWidth is how long a file's summary in the index's map may be.
const summaryWidth = 100

// clipLine cuts a line at a word to at most n bytes, with an ellipsis.
func clipLine(t string, n int) string {
	if len(t) <= n {
		return t
	}
	cut := strings.LastIndexByte(t[:n], ' ')
	if cut <= 0 {
		cut = n
	}
	t = strings.TrimRight(t[:cut], " ,;:")
	if strings.Count(t, "`")%2 == 1 {
		t = t[:strings.LastIndexByte(t, '`')]
		t = strings.TrimRight(t, " ,;:")
	}
	return t + "…"
}
