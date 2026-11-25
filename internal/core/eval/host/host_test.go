package host

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/aiviarium/heron/internal/core/eval"
)

func TestRunPassesArgumentsWithoutAShell(t *testing.T) {
	if _, err := exec.LookPath("printf"); err != nil {
		t.Skip("no printf")
	}
	e, err := Processes{}.Run(context.Background(), eval.Command{Path: "printf", Args: []string{"%s|", "a b", "$HOME;", "*"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(e.Stdout); got != "a b|$HOME;|*|" {
		t.Errorf("got %q", got)
	}
	e, err = Processes{}.Run(context.Background(), eval.Command{Path: "sh", Args: []string{"-c", "echo oops >&2; exit 3"}})
	if err != nil || e.Code != 3 || string(e.Stderr) != "oops\n" {
		t.Errorf("exit: %+v %v", e, err)
	}
}

func TestFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b.txt")
	if err := (Files{}).Write(p, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	b, err := Files{}.Read(p)
	if err != nil || string(b) != "hi" {
		t.Errorf("read %q %v", b, err)
	}
}
