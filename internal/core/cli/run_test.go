package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/cli"
	"github.com/aiviarium/heron/internal/core/records"
	"github.com/aiviarium/heron/internal/core/registry"
)

const tasksSrc = `import = ["std/types@1", "std/files@1", "std/process@1"]

Color: enum {
  RED: Self
  BLUE: Self
}

prepare: task do {
  write("prepared.txt", "yes")
}

/// Deploys.
@needs(prepare)
deploy(count: Int, color: Color, tags: List(String) = [], dry?: Bool): task(String) do {
  return "deployed $count $color ${tags.join("+")} $dry"
}

migrate: task do {
  write("migrated.txt", "yes")
}

unmigrate: task do {
  write("unmigrated.txt", "yes")
}

@needs(migrate)
@undo(unmigrate)
seed: task do {
  write("seeded.txt", "yes")
}

@needs(seed)
verify: task do {
  run("sh", args = ["-c", "exit 4"])
}

stats: task(Map(String, Int)) do {
  return { pets = 3, orders = 1 }
}

wipe: task<confirm> do {
  write("wiped.txt", "yes")
}

@needs(loop_b)
loop_a: task do {
  log("a")
}

@needs(loop_a)
loop_b: task do {
  log("b")
}
`

// terminal is stdin that claims to be a terminal, with typed answers.
type terminal struct{ *strings.Reader }

func (terminal) IsTerminal() bool { return true }

func runHeron(t *testing.T, dir string, stdin string, tty bool, args ...string) (int, string, string) {
	t.Helper()
	r := registry.New()
	cli.Register(r)
	e, out, errb := env(dir)
	if tty {
		e.Stdin = terminal{strings.NewReader(stdin)}
	} else {
		e.Stdin = strings.NewReader(stdin)
	}
	code := cli.Main(r, append([]string{"run"}, args...), e)
	return code, out.String(), errb.String()
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestRunParamsAndNeeds(t *testing.T) {
	dir := repo(t, map[string]string{"index.heron": tasksSrc})
	code, out, errs := runHeron(t, dir, "", false, "deploy", "--count", "50", "--color=BLUE", "--tags", "a,b", "--tags", "c", "--dry")
	if code != 0 || out != "deployed 50 BLUE a+b+c true\n" {
		t.Fatalf("code %d, out %q, err %q", code, out, errs)
	}
	if !exists(dir, "prepared.txt") {
		t.Error("@needs(prepare) didn't run")
	}
	code, _, errs = runHeron(t, dir, "", false, "deploy", "--count", "x", "--color", "RED")
	if code != 2 || !strings.Contains(errs, "--count takes an Int") {
		t.Errorf("bad int: %d %q", code, errs)
	}
	code, _, errs = runHeron(t, dir, "", false, "deploy", "--count", "1", "--color", "GREEN")
	if code != 2 || !strings.Contains(errs, "one of RED, BLUE") {
		t.Errorf("bad enum: %d %q", code, errs)
	}
	code, _, errs = runHeron(t, dir, "", false, "deploy", "--count", "1")
	if code != 2 || !strings.Contains(errs, "deploy needs --color (Color)") {
		t.Errorf("missing without a terminal: %d %q", code, errs)
	}
	code, out, errs = runHeron(t, dir, "PURPLE\nRED\n", true, "deploy", "--count", "1")
	if code != 0 || out != "deployed 1 RED  null\n" || !strings.Contains(errs, "color (Color): ") {
		t.Errorf("asked on a terminal: %d %q %q", code, out, errs)
	}
	code, out, _ = runHeron(t, dir, "", false, "--json", "stats")
	if code != 0 || !strings.Contains(out, `"pets": 3`) {
		t.Errorf("json: %d %q", code, out)
	}
	code, _, errs = runHeron(t, dir, "", false, "nope")
	if code != 2 || !strings.Contains(errs, "no task nope") {
		t.Errorf("unknown task: %d %q", code, errs)
	}
	code, _, errs = runHeron(t, dir, "", false, "loop_a")
	if code != 2 || !strings.Contains(errs, "cycle") {
		t.Errorf("cycle: %d %q", code, errs)
	}
}

func TestRunUndoAndConfirm(t *testing.T) {
	dir := repo(t, map[string]string{"index.heron": tasksSrc})
	code, _, errs := runHeron(t, dir, "", false, "verify")
	if code != 1 || !strings.Contains(errs, "verify failed") || exists(dir, "unmigrated.txt") {
		t.Fatalf("failure without undo: %d %q", code, errs)
	}
	code, _, errs = runHeron(t, dir, "", false, "--undo-on-failure", "verify")
	if code != 1 || !exists(dir, "unmigrated.txt") || !strings.Contains(errs, "undoing seed with unmigrate") {
		t.Fatalf("undo: %d %q", code, errs)
	}
	code, _, errs = runHeron(t, dir, "", false, "wipe")
	if code != 2 || exists(dir, "wiped.txt") || !strings.Contains(errs, "--yes") {
		t.Errorf("confirm without a terminal: %d %q", code, errs)
	}
	code, _, _ = runHeron(t, dir, "n\n", true, "wipe")
	if code != 1 || exists(dir, "wiped.txt") {
		t.Errorf("declined: %d", code)
	}
	code, _, _ = runHeron(t, dir, "y\n", true, "wipe")
	if code != 0 || !exists(dir, "wiped.txt") {
		t.Errorf("confirmed: %d", code)
	}
}

func TestRunIsRecorded(t *testing.T) {
	dir := repo(t, map[string]string{"index.heron": tasksSrc})
	if code, _, errs := runHeron(t, dir, "", false, "deploy", "--count", "7", "--color", "RED"); code != 0 {
		t.Fatalf("deploy: %d %q", code, errs)
	}
	if code, _, _ := runHeron(t, dir, "", false, "--undo-on-failure", "verify"); code != 1 {
		t.Fatalf("verify: %d", code)
	}
	r := registry.New()
	cli.Register(r)
	runs, err := records.Open(dir, records.Config{}, r).List(records.RunKind)
	if err != nil || len(runs) != 2 {
		t.Fatalf("runs: %v %v", runs, err)
	}
	failed, ok := runs[0], runs[1]
	if ok.Data["task"] != "deploy" || ok.Data["status"] != "ok" || ok.Data["result"] != "deployed 7 RED  null" {
		t.Errorf("ok run: %v", ok.Data)
	}
	if a := ok.Data["args"].(map[string]any); a["count"] != float64(7) || a["color"] != "RED" {
		t.Errorf("args: %v", a)
	}
	if failed.Data["status"] != "failed" || failed.Data["exit"] != float64(1) {
		t.Errorf("failed run: %v", failed.Data)
	}
	var roles []string
	for _, x := range failed.Data["tasks"].([]any) {
		m := x.(map[string]any)
		roles = append(roles, m["task"].(string)+":"+m["role"].(string)+":"+m["status"].(string))
	}
	if got := strings.Join(roles, " "); got != "migrate:needed:ok seed:needed:ok verify:task:failed unmigrate:undo:ok" {
		t.Errorf("tasks: %s", got)
	}
	procs := failed.Data["processes"].([]any)
	if len(procs) != 1 || procs[0].(map[string]any)["name"] != "sh" || procs[0].(map[string]any)["exit"] != float64(4) {
		t.Errorf("processes: %v", procs)
	}
}

// A path that names no task from the root is looked up inside the tree's
// repository, and a task two others need runs once, before both.
func TestRunFindsRepositoryTasksAndRunsNeedsOnce(t *testing.T) {
	dir := repo(t, map[string]string{"index.heron": `import = ["std/repo@1"]

repository {
  base: task do {
    log("base")
  }

  @needs(base)
  left: task do {
    log("left")
  }

  @needs(base)
  right: task do {
    log("right")
  }

  @needs(left, right)
  setup: task(String) do {
    return "set up"
  }
}
`})
	code, out, errs := runHeron(t, dir, "", false, "setup")
	if code != 0 || out != "set up\n" {
		t.Fatalf("code %d, out %q, err %q", code, out, errs)
	}
	if errs != "base\nleft\nright\n" {
		t.Errorf("order: %q", errs)
	}
	code, out, _ = runHeron(t, dir, "", false, "--json", "repository.left")
	if code != 0 || out != "null\n" {
		t.Errorf("a task without a result under --json: %d %q", code, out)
	}
}

// --yes runs a <confirm> task without a terminal, a run that can't be
// recorded keeps its exit status, a tree with an error runs
// nothing, @needs of a node that isn't a task stops the run before it
// starts, and an undo that fails doesn't stop the undos after it.
func TestRunYesChecksAndFailingUndo(t *testing.T) {
	dir := repo(t, map[string]string{"index.heron": tasksSrc})
	if code, _, errs := runHeron(t, dir, "", false, "--yes", "wipe"); code != 0 || !exists(dir, "wiped.txt") {
		t.Errorf("--yes: %d %q", code, errs)
	}
	blocked := repo(t, map[string]string{"index.heron": tasksSrc, ".heron": "not a folder"})
	if code, _, errs := runHeron(t, blocked, "", false, "prepare"); code != 0 || !strings.Contains(errs, "the run wasn't recorded") {
		t.Errorf("unrecorded run: %d %q", code, errs)
	}
	bad := repo(t, map[string]string{"index.heron": "a: task do {\n  log(nope)\n}\n"})
	if code, _, errs := runHeron(t, bad, "", false, "a"); code != 1 || !strings.Contains(errs, "the tree doesn't check") {
		t.Errorf("checked first: %d %q", code, errs)
	}
	undo := repo(t, map[string]string{"index.heron": `import = ["std/files@1", "std/process@1"]

x: Int = 1

@needs(x)
odd: task do {
  log("odd")
}

broken_undo: task do {
  run("sh", args = ["-c", "exit 2"])
}

good_undo: task do {
  write("undone.txt", "yes")
}

@undo(good_undo)
first: task do {
  log("first")
}

@needs(first)
@undo(broken_undo)
second: task do {
  log("second")
}

@needs(second)
last: task do {
  run("sh", args = ["-c", "exit 4"])
}
`})
	if code, _, errs := runHeron(t, undo, "", false, "odd"); code != 2 || !strings.Contains(errs, "odd needs x, which is no task") {
		t.Errorf("needs a non-task: %d %q", code, errs)
	}
	code, _, errs := runHeron(t, undo, "", false, "--undo-on-failure", "last")
	if code != 1 || !exists(undo, "undone.txt") || !strings.Contains(errs, "broken_undo failed") {
		t.Errorf("failing undo: %d %q", code, errs)
	}
}
