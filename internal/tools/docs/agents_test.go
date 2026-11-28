package docs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/gen"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

func agentFiles(t *testing.T, entry string, opts AgentOptions) []AgentFile {
	t.Helper()
	if entry == "" {
		t.Fatal(specMissing)
	}
	id, err := resolve.Canonical(entry)
	if err != nil {
		t.Fatal(err)
	}
	res := check.NewSession().Check(id)
	if res.Diags.HasErrors() {
		t.Fatalf("%s doesn't check: %v", entry, res.Diags)
	}
	files, err := Agents(res.Tree, res.Checker, opts)
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func fileByPath(files []AgentFile, p string) *AgentFile {
	for i := range files {
		if files[i].Path == p {
			return &files[i]
		}
	}
	return nil
}

var (
	mdLinkRe    = regexp.MustCompile(`\]\(([^)\s]+)\)`)
	mdHeadingRe = regexp.MustCompile(`(?m)^### [a-zA-Z_]+ (\S+)`)
	htmlTagRe   = regexp.MustCompile(`<(div|span|p|a|svg|section|details|table|ul|li|br)[\s>/]`)
)

// TestAgentExportOfSpec: the spec package's agent export has its index
// and llms.txt first, every file within the size budget unless it holds
// one node, plain Markdown with no HTML, every requirement under a heading
// with its full id, every relative link leading to a file and a heading of
// the export, layers in order, and the same bytes on a second run. An
// agent finds what the checker does when field numbers collide from the
// index and one file.
func TestAgentExportOfSpec(t *testing.T) {
	t.Parallel() // a session and site of its own; the slow tests run at once
	files := agentFiles(t, entries["spec"], AgentOptions{})
	if len(files) < 10 || files[0].Path != AgentIndex || files[1].Path != LLMsTxt {
		t.Fatalf("the export starts %v", paths(files))
	}
	ids := map[string]map[string]bool{}
	for _, f := range files {
		ids[f.Path] = map[string]bool{}
		for _, m := range mdHeadingRe.FindAllStringSubmatch(string(f.Content), -1) {
			ids[f.Path][strings.TrimSuffix(m[1], ":")] = true
		}
	}
	budget := 6000
	for _, f := range files {
		if strings.Contains(string(f.Content), "\n### howto ") || strings.Contains(string(f.Content), "\n### tutorial ") {
			t.Errorf("%s holds an authored page, which the agent lens hides", f.Path)
		}
		c := string(f.Content)
		if Tokens(f.Content) > budget && len(ids[f.Path]) > 1 && f.Path != AgentIndex {
			t.Errorf("%s: %d tokens, over the budget of %d, with %d nodes", f.Path, Tokens(f.Content), budget, len(ids[f.Path]))
		}
		plain := stripFences(c)
		if loc := htmlTagRe.FindStringIndex(plain); loc != nil {
			t.Errorf("%s: HTML at %q", f.Path, plain[loc[0]:min(len(plain), loc[0]+60)])
		}
		for _, m := range mdLinkRe.FindAllStringSubmatch(plain, -1) {
			href := m[1]
			if strings.Contains(href, "://") {
				continue
			}
			file, frag, _ := strings.Cut(href, "#")
			if ids[file] == nil {
				t.Errorf("%s: link to %s, which isn't a file of the export", f.Path, href)
				continue
			}
			if frag != "" && !ids[file][frag] && !strings.Contains(string(fileByPath(files, file).Content), frag) {
				t.Errorf("%s: link to %s, whose file holds no %s", f.Path, href, frag)
			}
		}
		if f.Path == AgentIndex || f.Path == LLMsTxt {
			continue
		}
		last := -1
		for i, g := range groupTitles {
			at := strings.Index(c, "\n## "+g+"\n")
			if at < 0 {
				continue
			}
			if at < last {
				t.Errorf("%s: %s comes before an earlier layer", f.Path, groupTitles[i])
			}
			last = at
		}
	}
	// The agent's question: what must the checker do when field numbers
	// collide? The index names the file, and the file holds the rule.
	idx := string(files[0].Content)
	// The index maps the top areas alone; each area's file maps the files
	// below it, so the index stays within the budget.
	for _, want := range []string{"## How to read these files", "## Areas", "[Language](language.md)", "tokens)"} {
		if !strings.Contains(idx, want) {
			t.Errorf("INDEX.md lacks %q", want)
		}
	}
	if strings.Contains(idx, "language.apis.sparrow.numbering.md") {
		t.Error("INDEX.md maps a file below a top area")
	}
	if n := Tokens(files[0].Content); n > 2000 {
		t.Errorf("INDEX.md is %d tokens, want at most 2000", n)
	}
	for _, step := range [][2]string{{"language.md", "language.apis.md"}, {"language.apis.md", "language.apis.sparrow.md"}, {"language.apis.sparrow.md", "language.apis.sparrow.numbering.md"}} {
		if f := fileByPath(files, step[0]); f == nil || !strings.Contains(string(f.Content), "## Files below") || !strings.Contains(string(f.Content), "]("+step[1]+")") {
			t.Errorf("%s doesn't map %s under Files below", step[0], step[1])
		}
	}
	f := fileByPath(files, "language.apis.sparrow.numbering.md")
	if f == nil {
		t.Fatal("no language.apis.sparrow.numbering.md")
	}
	for _, want := range []string{
		"### rule language.apis.sparrow.numbering.unique_tags",
		"must: If two fields of a struct have one tag, the checker must report duplicate tag at the second.",
		"by [system.checker](system.md#system.checker)",
		"## Specification",
		"## Terms used here",
	} {
		if !strings.Contains(string(f.Content), want) {
			t.Errorf("%s lacks %q:\n%s", f.Path, want, f.Content)
		}
	}
	again := agentFiles(t, entries["spec"], AgentOptions{})
	if len(again) != len(files) {
		t.Fatalf("a second run wrote %d files, the first %d", len(again), len(files))
	}
	for i := range files {
		if files[i].Path != again[i].Path || !bytes.Equal(files[i].Content, again[i].Content) {
			t.Errorf("%s: a second run wrote other bytes", files[i].Path)
		}
	}
}

func paths(fs []AgentFile) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Path)
	}
	return out
}

var fenceRe = regexp.MustCompile("(?s)```.*?```|`[^`\n]*`")

var agentHrefRe = regexp.MustCompile(`href="[^"]*(agents/|INDEX\.md|llms\.txt)`)

func stripFences(s string) string { return fenceRe.ReplaceAllString(s, "") }

// TestAgentContextPack: a pack is one file of the subtree under packs/,
// never split, its own nodes linked within it and the rest of the export
// through ../, with what it links outside the subtree after it; an id
// that names no node is an error.
func TestAgentContextPack(t *testing.T) {
	t.Parallel() // a session and site of its own; the slow tests run at once
	files := agentFiles(t, entries["spec"], AgentOptions{Pack: "language.apis.grpc"})
	p := files[len(files)-1]
	if p.Path != "packs/language.apis.grpc.md" {
		t.Fatalf("the last file is %s", p.Path)
	}
	c := string(p.Content)
	for _, want := range []string{"# Context pack: ", "### rule language.apis.grpc.numbering.unique_numbers", "## Linked from here", "](../INDEX.md)", "](#language.apis.grpc.proto_service)", "](../system.md#system.checker)"} {
		if !strings.Contains(c, want) {
			t.Errorf("the pack lacks %q", want)
		}
	}
	if strings.Contains(c, "part 1 of") {
		t.Error("the pack is split")
	}
	id, _ := resolve.Canonical(entries["petstore"])
	res := check.NewSession().Check(id)
	if _, err := Agents(res.Tree, res.Checker, AgentOptions{Pack: "no.such.node"}); err == nil || err.Error() != "no node no.such.node in the tree" {
		t.Errorf("an unknown pack id: %v", err)
	}
}

// TestAgentUsecaseLines: a use case is written as one typed line per step
// and extension, with its party, the rules it uses and, for an extension,
// its condition and outcome, and no flow diagram.
func TestAgentUsecaseLines(t *testing.T) {
	t.Parallel() // a session and site of its own; the slow tests run at once
	files := agentFiles(t, entries["spec"], AgentOptions{})
	var uc string
	for _, f := range files {
		c := string(f.Content)
		if i := strings.Index(c, "### usecase tools.vscode.fixing.fix_a_diagnostic"); i >= 0 {
			uc = c[i:]
			if j := strings.Index(uc[4:], "\n### "); j >= 0 {
				uc = uc[:j+4]
			}
		}
	}
	if uc == "" {
		t.Fatal("no use case fix_a_diagnostic in the export")
	}
	for _, want := range []string{
		"- actor [actors.author](actors.md#actors.author) · level USER",
		"- step 1 · by [system.editor_extension](system.md#system.editor_extension): ",
		"- extension 1a · at step 1 · if The language server isn't running · by [system.editor_extension](system.md#system.editor_extension) · outcome fails: ",
		"· outcome back to 4: ",
	} {
		if !strings.Contains(uc, want) {
			t.Errorf("the use case lacks %q:\n%s", want, uc)
		}
	}
	if strings.Contains(uc, "svg") || strings.Contains(uc, "```") {
		t.Errorf("the use case has a diagram:\n%s", uc)
	}
}

const agentTree = `import = ["std/schema@1", "std/doc@1", "std/http@1", "std/docs@1"]

shop: package(version = "1.0") {
  | A shop that sells things.

  sell: goal {{ Customers buy what they want. }}

  orders: section {
    | How orders are placed.

    order_term: term(aliases = ["order"]) {{ A request to buy things, with a total. }}

    @lens(user)
    place: rule(sell) {
      when {{ a customer places an [order_term] }}
      must {{ record it }}
      then {{ it can be paid }}
    }

    happy: scenario(place) {
      given {{ a cart }}
      when {{ the customer checks out }}
      then {{ an order is recorded }}
    }

    @display("Out of stock")
    @status(409)
    out_of_stock: error("shop.OUT_OF_STOCK") {
      title = "{item} is out of stock"
      item: field(String)
      | Reported as [orders.place] says.
    }

    BULK
  }

  risk(level: String): kind(in = [doc.block]) {
    accept(prose)
  }

  late: risk("high") {{ Parcels may arrive late. }}

  @display("Placing an order")
  placing: example {
    | ` + "```heron" + `
    | cart = ["book"]
    | ` + "```" + `
  }

  tutorial_page: tutorial {
    | A tutorial for people.
  }
}

agent: lens {
  title = "Agents"
  nav = LensNav.TREE
  format = LensFormat.MARKDOWN
  marks = false
  hide = [docs.authored]
  summarize = [spec.guidance]
  budget = 500
  prefix = "agents"
}

docs {
  lenses = [user, explore]
}
`

// TestAgentExportSplitsAndOrders: a tree's own agent lens decides what's
// written: its marks unread, its hidden kinds left out, its summed-up
// kinds as one line; a file over its budget is split into parts that link
// each other, each within the budget; and each file's parts follow the
// layers, with the derived lists marked.
func TestAgentExportSplitsAndOrders(t *testing.T) {
	var bulk strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&bulk, "    rule_%02d: rule {\n      must {{ keep rule number %d of the shop's many rules, written out at some length so the file grows past its budget }}\n    }\n\n", i, i)
	}
	bulk.WriteString("    hint: guidance {{ Write rules short. They read better. }}\n")
	dir := t.TempDir()
	p := filepath.Join(dir, "index.heron")
	if err := os.WriteFile(p, []byte(strings.Replace(agentTree, "BULK", bulk.String(), 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	files := agentFiles(t, p, AgentOptions{})
	var parts []AgentFile
	for _, f := range files {
		if strings.HasPrefix(f.Path, "orders") {
			parts = append(parts, f)
		}
		if strings.Contains(string(f.Content), "tutorial_page") {
			t.Errorf("%s holds the tutorial the lens hides", f.Path)
		}
	}
	if len(parts) < 3 || parts[0].Path != "orders.md" || parts[1].Path != "orders.2.md" {
		t.Fatalf("orders is written as %v", paths(parts))
	}
	for i, f := range parts {
		c := string(f.Content)
		if Tokens(f.Content) > 500 {
			t.Errorf("%s: %d tokens, over the budget", f.Path, Tokens(f.Content))
		}
		if i+1 < len(parts) && !strings.Contains(c, "Next part: ["+parts[i+1].Path+"]") {
			t.Errorf("%s doesn't link %s", f.Path, parts[i+1].Path)
		}
	}
	first := string(parts[0].Content)
	for _, want := range []string{
		"# Orders (`orders`), part 1 of ",
		"How orders are placed.",
		"## Specification\n\n### term orders.order_term\n\n**order**",
		"### rule orders.place\n\nmust: When a customer places an [order](orders.md#orders.order_term), the system must record it.",
		"- then: it can be paid",
		"serves [sell](_root.md#sell)",
		"- Scenarios (derived): [orders.happy](orders.md#orders.happy)",
		"- Errors (derived): [orders.out_of_stock](",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("orders.md lacks %q:\n%s", want, first)
		}
	}
	all := ""
	for _, f := range parts {
		all += string(f.Content)
	}
	if strings.Index(all, "## Contract") < strings.Index(all, "### rule orders.rule_39") {
		t.Error("the contract comes before the last rule")
	}
	for _, want := range []string{"### error orders.out_of_stock: Out of stock\n\n- id `shop.OUT_OF_STOCK` · severity error · HTTP 409", "- field `item` String", "### guidance orders.hint\n\nWrite rules short.\n"} {
		if !strings.Contains(all, want) {
			t.Errorf("the parts lack %q", want)
		}
	}
	idx := string(files[0].Content)
	for _, want := range []string{"# Shop: agent export", "A shop that sells things.", "- goal [sell](_root.md#sell): Customers buy what they want.", "- [Orders](orders.md): How orders are placed. (~"} {
		if !strings.Contains(idx, want) {
			t.Errorf("INDEX.md lacks %q:\n%s", want, idx)
		}
	}
	llms := string(files[1].Content)
	if !strings.HasPrefix(llms, "# Shop\n\n> A shop that sells things.\n") || !strings.Contains(llms, "- [Index](INDEX.md)") || !strings.Contains(llms, "- [Orders](orders.md)") {
		t.Errorf("llms.txt:\n%s", llms)
	}
}

// TestDocsSiteCarriesAgentExport: generating the docs writes the export
// under the agent lens's prefix and llms.txt at the site's root, its
// links leading into that folder.
func TestDocsSiteCarriesAgentExport(t *testing.T) {
	r := registry.New()
	Register(r)
	id, _ := resolve.Canonical(entries["petstore"])
	res := check.NewSession().Check(id)
	dir := t.TempDir()
	var got map[string]string
	for _, g := range r.Generators("docs") {
		files, err := g.Generate(context.Background(), &gen.Input{Tree: res.Tree, Checker: res.Checker, Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		got = map[string]string{}
		for _, f := range files {
			got[f.Path] = string(f.Content)
		}
	}
	if _, ok := got[".heron/docs/agents/INDEX.md"]; !ok {
		t.Fatal("the site has no agents/INDEX.md")
	}
	if l := got[".heron/docs/llms.txt"]; !strings.Contains(l, "](agents/INDEX.md)") || strings.Contains(l, "](INDEX.md)") {
		t.Errorf("llms.txt at the site's root:\n%s", l)
	}
	for _, g := range r.Generators("agents") {
		files, err := g.Generate(context.Background(), &gen.Input{Tree: res.Tree, Checker: res.Checker, Dir: dir})
		if err != nil {
			t.Fatal(err)
		}
		if len(files) < 3 || files[0].Path != ".heron/agents/INDEX.md" || files[len(files)-1].Path != ".heron/agents/.manifest" {
			t.Errorf("the agents target wrote %d files, first %s", len(files), files[0].Path)
		}
	}
}

// TestAgentContractForms: an op is written as its signature, its bindings
// and its errors with every projection; a type as its declaration without
// its doc comments; an example as its code block; and a node of a kind a
// package declares as its prose and the arguments it sets.
func TestAgentContractForms(t *testing.T) {
	files := agentFiles(t, "../../../examples/parcels/index.heron", AgentOptions{})
	all := ""
	for _, f := range files {
		all += string(f.Content)
	}
	for _, want := range []string{
		"### op parcels.get_parcel",
		"```heron\nget_parcel(@path id: String, @header(\"Tenant\") tenant?: String): Parcel\n```",
		"- HTTP GET `/v1/parcels/:id`",
		"- gRPC `/acme.parcels.v1.ParcelsService/GetParcel` (unary)",
		"- JSON-RPC method `parcels.get`",
		"· HTTP 404 · gRPC NOT_FOUND · JSON-RPC -32004 · Sparrow not_found (5)",
		"- Sparrow method `acme.parcels/getParcel` (id 0, unary, `GetParcelInput` → `Parcel`, idempotent)",
		"- Sparrow method `acme.parcels/navigate` (id 4, bidi, `stream<Position>` → `stream<Instruction>`, input_end, output_end)",
		"· HTTP 422 · gRPC INVALID_ARGUMENT · Sparrow sigil InvalidAddress (80)",
		"Sparrow service `Parcels` (id 0) of facet `acme.parcels` 1.0.0 (id 16, hash `01",
		"- param `tenant`: The account the parcel belongs to",
		"- example labelled: id = \"p_1042\"",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("parcels' export lacks %q", want)
		}
	}
	i := strings.Index(all, "\n### type ")
	if i < 0 {
		t.Fatal("no type in parcels' export")
	}
	decl := all[i:]
	decl = decl[:strings.Index(decl, "\n```\n")]
	if !strings.Contains(decl, "```heron\n") || strings.Contains(decl, "///") {
		t.Errorf("a type is written as %q", decl)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "index.heron")
	if err := os.WriteFile(p, []byte(strings.Replace(agentTree, "BULK", "", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	all = ""
	for _, f := range agentFiles(t, p, AgentOptions{}) {
		all += string(f.Content)
	}
	for _, want := range []string{"### risk late\n\nParcels may arrive late.\n\n- level: high", "### example placing: Placing an order\n\n```heron\ncart = [\"book\"]\n```"} {
		if !strings.Contains(all, want) {
			t.Errorf("the export lacks %q:\n%s", want, all)
		}
	}
}

// TestAgentLensNotSwitched: no page of the site lists the agent lens in
// its lens switcher or links into the export.
func TestAgentLensNotSwitched(t *testing.T) {
	t.Parallel() // a session and site of its own; the slow tests run at once
	s := site(t, entries["spec"])
	ls := NewLenses(s.Tree, s.Checker, Options{})
	files, err := ls.Files()
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".html") && agentHrefRe.Match(f.Content) {
			t.Errorf("%s links the agent export", f.Path)
		}
	}
}
