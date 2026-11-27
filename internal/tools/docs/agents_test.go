package docs

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
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
		t.Fatalf("%s doesn't check", entry)
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
	for _, want := range []string{"## How to read these files", "## Files", "[language.apis.grpc.numbering.md](language.apis.grpc.numbering.md)", "tokens)"} {
		if !strings.Contains(idx, want) {
			t.Errorf("INDEX.md lacks %q", want)
		}
	}
	f := fileByPath(files, "language.apis.grpc.numbering.md")
	if f == nil {
		t.Fatal("no language.apis.grpc.numbering.md")
	}
	for _, want := range []string{
		"### rule language.apis.grpc.numbering.unique_numbers",
		"must: If two fields of a message, or two values of an enum, have one number, the checker must report number used twice at the second.",
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

func stripFences(s string) string { return fenceRe.ReplaceAllString(s, "") }
