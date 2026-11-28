package check_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// checkFiles writes files under a temp dir and checks the entry.
func checkFiles(t *testing.T, files map[string]string, entry string) *check.Result {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id, err := resolve.Canonical(filepath.Join(dir, entry))
	if err != nil {
		t.Fatal(err)
	}
	return check.NewSession().Check(id)
}

func diagStrings(res *check.Result) []string {
	var out []string
	for _, d := range res.Diags {
		out = append(out, d.Code+": "+d.Message)
	}
	return out
}

func wantDiagContaining(t *testing.T, res *check.Result, sub string) {
	t.Helper()
	for _, d := range diagStrings(res) {
		if strings.Contains(d, sub) {
			return
		}
	}
	t.Errorf("no diagnostic containing %q in %v", sub, diagStrings(res))
}

const implKinds = `import = ["std/doc@1", "std/impl@1"]

refunds: feature {
  late: rule {
    must {{ reject a late refund }}
  }
}

project(".") {
  members = [svc, types, ledger]
  toolchain = { node = "22" }

  setup(): task do {
    install_packages()
  }
}

svc: project(path = "services/refunds") {
  implements = [refunds]
  paths = { refunds = "src/refunds/**" }
  depends_on = [types, ledger]
}

types: project(path = "packages/types") {}

ledger: project<external>(path = "vendor/ledger") {
  source = git("https://example.com/ledger", ref = "v2.3.1")
}
`

// std/impl's project kind checks clean with its fields, a task, the
// external flag and a git source, the project at "." among them; a field
// it doesn't declare and a dependency that names nothing are reported.
func TestImplProjects(t *testing.T) {
	res := checkFiles(t, map[string]string{"index.heron": implKinds}, "index.heron")
	if ds := diagStrings(res); len(ds) > 0 {
		t.Fatal(ds)
	}
	res = checkFiles(t, map[string]string{"index.heron": `import = ["std/impl@1"]

p: project(path = "p") {
  owner = "x"
  depends_on = [nobody]
}
`}, "index.heron")
	wantDiagContaining(t, res, "p (a project) has no field owner")
	wantDiagContaining(t, res, "unknown name nobody")
}

// Contract coverage counts the rules whose subject is a part or the
// system, and those a contract entry implements or an error links.
func TestContractCoverage(t *testing.T) {
	src := `import = ["std/doc@1", "std/types@1"]

payments: part {{ Takes the buyer's money. }}

buyer: actor {{ Someone who buys. }}

pay_once: rule(payments) {
  must {{ charge an order at most once }}
}

receipt: rule(payments) {
  must {{ send a receipt }}
}

look: rule(buyer) {
  must {{ read the total before paying }}
}

no_receipt: error(422, id = "shop.NO_RECEIPT") {
  title = "no receipt for {order}"
  order: String
  | Reported as [receipt] says.
}
`
	res := checkFiles(t, map[string]string{"index.heron": src}, "index.heron")
	if ds := diagStrings(res); len(ds) > 0 {
		t.Fatal(ds)
	}
	var rules []*resolve.Node
	for _, name := range []string{"pay_once", "receipt", "look"} {
		rules = append(rules, res.Tree.Root.Child(name))
	}
	if done, all := project.ContractCoverage(res.Checker, rules, project.CarriedOut(res.Tree, res.Checker)); done != 1 || all != 2 {
		t.Errorf("contract coverage: %d of %d, want 1 of 2", done, all)
	}
}
