package release

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/registry"
)

const spec = `import = ["std/doc@1", "std/spec@1"]

shop: package(version = "2.1.3", sealed = true) {
  pay_once: rule {
    must {{ pay once }}
  }
}
`

func run(t *testing.T, app string, args ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	for name, src := range map[string]string{"shop/index.heron": spec, "app/index.heron": app} {
		p := filepath.Join(dir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out, errb bytes.Buffer
	env := &registry.Env{Dir: filepath.Join(dir, "app"), Stdout: &out, Stderr: &errb}
	code := command(env, args)
	return code, out.String(), errb.String()
}

func app(at, version, extra string) string {
	return `import = ["std/doc@1", "std/spec@1", "std/project@1", "../shop/"]

app: implements(shop, at = "` + at + `") {
  version = "` + version + `"
` + extra + `}
`
}

// `heron release next` bumps the build counter in the default scheme and
// the implementation patch in the patch scheme, starting again on a spec
// change; `heron release check` reports versions off the spec.
func TestReleaseCommand(t *testing.T) {
	src := app("2.1.3", "2.1.3+build2", "") + "r1: release(\"2.1.3+build1\")\nr2: release(\"2.1.3+build2\")\n"
	if code, out, errs := run(t, src, "next"); code != 0 || strings.TrimSpace(out) != "2.1.3+build3" {
		t.Errorf("next: %d %q %q", code, out, errs)
	}
	if code, out, _ := run(t, src, "next", "--tag", "rc"); code != 0 || strings.TrimSpace(out) != "2.1.3+rc3" {
		t.Errorf("next --tag rc: %d %q", code, out)
	}
	patch := app("2.1.3", "2.1.3.1", "  versioning.{ scheme = \"SM.Sm.Sp.IP\" }\n") + "r1: release(\"2.1.3.0\")\nr2: release(\"2.1.3.1\")\nold: release(\"2.1.2.5\")\n"
	if code, out, errs := run(t, patch, "next"); code != 0 || strings.TrimSpace(out) != "2.1.3.2" {
		t.Errorf("patch next: %d %q %q", code, out, errs)
	}
	// A spec change: the project moved to 2.1.3, its releases are of 2.1.2.
	moved := app("2.1.3", "2.1.3.0", "  versioning.{ scheme = \"SM.Sm.Sp.IP\" }\n") + "old: release(\"2.1.2.5\")\n"
	if code, out, errs := run(t, moved, "next"); code != 0 || strings.TrimSpace(out) != "2.1.3.0" {
		t.Errorf("spec change: %d %q %q", code, out, errs)
	}
	if code, out, errs := run(t, src, "check"); code != 0 || !strings.Contains(out, "app implements shop at 2.1.3, version 2.1.3+build2") {
		t.Errorf("check: %d %q %q", code, out, errs)
	}
	bad := app("2.1.3", "2.1.2+build1", "")
	if code, _, errs := run(t, bad, "check"); code != 1 || !strings.Contains(errs, "the project version 2.1.2+build1 starts with 2.1.2") {
		t.Errorf("check off the spec: %d %q", code, errs)
	}
	if code, _, errs := run(t, "import = [\"std/doc@1\"]\n", "next"); code != 1 || !strings.Contains(errs, "implements no spec") {
		t.Errorf("no project: %d %q", code, errs)
	}
	if code, _, _ := run(t, src, "bump"); code != 2 {
		t.Errorf("unknown subcommand: %d", code)
	}
}
