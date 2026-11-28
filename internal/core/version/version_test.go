package version

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestVersionMatchesSpec: the spec version is the version of the spec
// package heron.lock pins, and the product version starts with it.
func TestVersionMatchesSpec(t *testing.T) {
	src, err := os.ReadFile("../../../heron.lock")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`"github.com/aiviarium/heron-specs/heron" = \{ version = "([^"]+)"`).FindStringSubmatch(string(src))
	if m == nil {
		t.Fatal("heron.lock pins no heron-specs version")
	}
	if Spec() != m[1] || !strings.HasPrefix(Version, m[1]+"+") {
		t.Errorf("Version %q and Spec %q; heron.lock pins the spec at %q", Version, Spec(), m[1])
	}
}

// TestVersionLines: five lines, each opening with its word, in order, and
// unknown for a commit or a time the build doesn't know.
func TestVersionLines(t *testing.T) {
	in := Info{Version: "0.6.0+build1", Spec: "0.6.0", Commit: "unknown", Built: "unknown", Platform: "linux/amd64"}
	want := "heron 0.6.0+build1\nspec 0.6.0\ncommit unknown\nbuilt unknown\nplatform linux/amd64\n"
	if got := in.Lines(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	r := Read()
	ls := strings.Split(strings.TrimSuffix(r.Lines(), "\n"), "\n")
	words := []string{"heron", "spec", "commit", "built", "platform"}
	if len(ls) != len(words) {
		t.Fatalf("%d lines: %q", len(ls), ls)
	}
	for i, w := range words {
		f := strings.Fields(ls[i])
		if len(f) != 2 || f[0] != w || f[1] == "" {
			t.Errorf("line %d: %q", i+1, ls[i])
		}
	}
}
