//go:build !unit1

package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/cli"
	"github.com/aiviarium/heron/internal/core/fixtures"
	"github.com/aiviarium/heron/internal/core/project"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
	"github.com/aiviarium/heron/internal/core/specmodel"
)

// TestHelpIsTheSpec proves that `heron help <command>` prints, for every
// command the binary registers, the usage line the spec's declaration of
// the command derives from its parameters, and that every command the
// spec declares is registered, but `heron help`, which the command line
// answers itself. A command with subcommands shows theirs, joined by |.
func TestHelpIsTheSpec(t *testing.T) {
	r := registry.New()
	for _, register := range units {
		register(r)
	}
	entry, err := fixtures.SpecEntry(filepath.Join("..", ".."))
	if err != nil {
		t.Skip(err)
	}
	id, _ := resolve.Canonical(entry)
	res := check.NewSession().Check(id)
	cliNode := res.Tree.Find("heron.cli")
	if cliNode == nil {
		t.Fatal("the spec has no cli scope")
	}
	declared := map[string]bool{}
	for _, n := range cliNode.Children {
		if project.KindPath(res.Checker, n) != "spec.command" {
			continue
		}
		name := strings.TrimPrefix(specmodel.CommandName(n), "heron ")
		declared[name] = true
		if name == "help" {
			continue
		}
		c := r.Command(name)
		if c == nil {
			t.Errorf("the spec declares heron %s, which the binary doesn't register", name)
			continue
		}
		want := specmodel.CommandUsage(res.Checker, n)
		var subs []string
		for _, ch := range n.Children {
			if project.KindPath(res.Checker, ch) == "spec.command" {
				subs = append(subs, strings.TrimPrefix(specmodel.CommandUsage(res.Checker, ch), "heron "+name+" "))
			}
		}
		if len(subs) > 0 {
			want = "heron " + name + " " + strings.Join(subs, " | ")
		}
		var out, errb bytes.Buffer
		env := &registry.Env{Dir: t.TempDir(), Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}
		if code := cli.Main(r, []string{"help", name}, env); code != 0 {
			t.Errorf("heron help %s: exit %d", name, code)
		}
		if got := strings.SplitN(out.String(), "\n", 2)[0]; got != "usage: "+want {
			t.Errorf("heron help %s prints %q, and the spec derives %q", name, got, "usage: "+want)
		}
	}
	for _, c := range r.Commands() {
		if !declared[c.Name] {
			t.Errorf("the binary registers heron %s, which the spec doesn't declare", c.Name)
		}
	}
}
