package eval

import (
	"context"
	"time"
)

// Caps are the capabilities code reaches the world through. Eval itself
// does no I/O; package eval/host supplies the real ones, and tests supply
// fakes. A nil capability fails the call that needs it.
type Caps struct {
	Processes Processes
	Files     Files
	Env       Env
	Clock     Clock
	// Log receives each line of log(…); nil drops them (they are still
	// recorded as events).
	Log func(line string)
}

// Processes starts processes. A command is always an argument list: the
// program and its arguments, never a line for a shell to split.
type Processes interface {
	Run(ctx context.Context, c Command) (Exit, error)
}

// Command is a process to start.
type Command struct {
	Path  string   // the program, looked up on PATH when it has no slash
	Args  []string // its arguments, without the program
	Env   []string // the whole environment, as KEY=value
	Dir   string   // the working directory
	Stdin []byte   // nil for no input
}

// Exit is a finished process.
type Exit struct {
	Code   int
	Stdout []byte
	Stderr []byte
}

// Files reads and writes files. Paths are absolute: eval joins relative
// ones to the machine's directory first.
type Files interface {
	Read(path string) ([]byte, error)
	Write(path string, data []byte) error
	// TempDir makes a new directory that belongs to the run.
	TempDir() (string, error)
	RemoveAll(path string) error
}

// Env is the environment processes inherit.
type Env interface {
	Environ() []string
}

// Clock is the only source of time.
type Clock interface {
	Now() time.Time
}

// FixedClock is a clock that always reads one time, for replayable runs.
type FixedClock time.Time

// Now returns the fixed time.
func (c FixedClock) Now() time.Time { return time.Time(c) }
