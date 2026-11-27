package version

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestVersionMatchesProject: the product version is the project tree's
// version, and the spec version the one its implements names.
func TestVersionMatchesProject(t *testing.T) {
	src, err := os.ReadFile("../../../project/index.heron")
	if err != nil {
		t.Fatal(err)
	}
	v := regexp.MustCompile(`(?m)^\s*version = "([^"]+)"`).FindStringSubmatch(string(src))
	at := regexp.MustCompile(`implements\(heron, at = "([^"]+)"\)`).FindStringSubmatch(string(src))
	if v == nil || at == nil {
		t.Fatal("project/index.heron names no version or no spec version")
	}
	if Version != v[1] || Spec() != at[1] {
		t.Errorf("Version %q and Spec %q; the project says %q at %q", Version, Spec(), v[1], at[1])
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
