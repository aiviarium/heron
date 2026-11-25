// Package host supplies eval's capabilities from the operating system:
// processes through os/exec with an argument list, never a shell, files
// through os, the process environment and the wall clock.
package host

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/aiviarium/heron/internal/core/eval"
)

// Caps returns the real capabilities, with log lines going to log.
func Caps(log func(string)) eval.Caps {
	return eval.Caps{Processes: Processes{}, Files: Files{}, Env: Env{}, Clock: Clock{}, Log: log}
}

// Processes runs commands with os/exec.
type Processes struct{}

// Run starts a command, waits for it and returns its exit status and
// output. A non-zero exit is no error here; eval turns it into a throw.
func (Processes) Run(ctx context.Context, c eval.Command) (eval.Exit, error) {
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Env, cmd.Dir = c.Env, c.Dir
	if c.Stdin != nil {
		cmd.Stdin = bytes.NewReader(c.Stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return eval.Exit{Code: ee.ExitCode(), Stdout: out.Bytes(), Stderr: errb.Bytes()}, nil
	}
	if err != nil {
		return eval.Exit{}, err
	}
	return eval.Exit{Stdout: out.Bytes(), Stderr: errb.Bytes()}, nil
}

// Files reads and writes the file system.
type Files struct{}

// Read reads a file.
func (Files) Read(p string) ([]byte, error) { return os.ReadFile(p) }

// Write writes a file, making its directory.
func (Files) Write(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// TempDir makes a directory for the run's temp(…) files.
func (Files) TempDir() (string, error) { return os.MkdirTemp("", "heron-run-") }

// RemoveAll removes a path and what it holds.
func (Files) RemoveAll(p string) error { return os.RemoveAll(p) }

// Env is the process environment.
type Env struct{}

// Environ returns the environment as KEY=value.
func (Env) Environ() []string { return os.Environ() }

// Clock is the wall clock.
type Clock struct{}

// Now returns the current time.
func (Clock) Now() time.Time { return time.Now() }
