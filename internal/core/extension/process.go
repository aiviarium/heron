package extension

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/helightdev/specs-v0/internal/core/extension/protocol"
)

// Process is one supervised extension process: started on first use,
// handshaken, and started again after a crash. Calls in flight when it
// exits fail with ForeignError.
type Process struct {
	Spec *Spec
	// Argv is the command to start, resolved.
	Argv []string
	Env  []string
	// Sandbox applies the declared limits with namespaces.
	Sandbox bool
	Wire    protocol.Wire
	Limits  protocol.Limits
	Init    protocol.InitializeParams
	// Host serves the extension's calls back into Heron.
	Host protocol.Host
	// Log receives the extension's stderr and $/log notifications.
	Log io.Writer
	// Warn receives warnings, such as limits that can't be enforced.
	Warn func(string)

	mu       sync.Mutex
	conn     *protocol.Conn
	cmd      *exec.Cmd
	result   *protocol.InitializeResult
	starts   []time.Time
	closed   bool
	exited   chan struct{}
	warnedSB bool
}

// MaxRestarts bounds how often a crashing process is started again within
// RestartWindow before calls fail without trying.
const (
	MaxRestarts   = 5
	RestartWindow = time.Minute
)

// ForeignError is what a call fails with when the foreign side broke rather
// than threw a declared error.
func ForeignError(format string, args ...any) *protocol.Error {
	return &protocol.Error{Code: protocol.CodeForeignError, Type: "ForeignError", Message: fmt.Sprintf(format, args...)}
}

// Start starts the process and runs the handshake, unless it's running.
func (p *Process) Start(ctx context.Context) (*protocol.InitializeResult, error) {
	_, res, err := p.ensure(ctx)
	return res, err
}

func (p *Process) ensure(ctx context.Context) (*protocol.Conn, *protocol.InitializeResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, nil, ForeignError("%s: the extension was shut down", p.Spec.Name)
	}
	if p.conn != nil {
		// The process can have exited before its connection has read the
		// end of its output: a connection to a process that exited is
		// dead too, and reusing it would fail the call that just
		// followed a crash instead of starting the process again.
		select {
		case <-p.conn.Done():
		case <-p.exited:
		default:
			return p.conn, p.result, nil
		}
	}
	now := time.Now()
	var recent []time.Time
	for _, t := range p.starts {
		if now.Sub(t) < RestartWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) >= MaxRestarts {
		return nil, nil, ForeignError("%s: the extension exited %d times in %s; not starting it again", p.Spec.Name, len(recent), RestartWindow)
	}
	p.starts = append(recent, now)
	conn, res, err := p.start(ctx)
	if err != nil {
		return nil, nil, err
	}
	p.conn, p.result = conn, res
	return conn, res, nil
}

func (p *Process) warn(msg string) {
	if p.Warn != nil {
		p.Warn(msg)
	}
}

func (p *Process) start(ctx context.Context) (*protocol.Conn, *protocol.InitializeResult, error) {
	if len(p.Argv) == 0 {
		return nil, nil, ForeignError("%s: no command to start", p.Spec.Name)
	}
	mk := func() *exec.Cmd {
		cmd := exec.Command(p.Argv[0], p.Argv[1:]...)
		cmd.Dir = p.Spec.Dir
		cmd.Env = append(os.Environ(), p.Env...)
		return cmd
	}
	cmd := mk()
	if p.Sandbox {
		ok, err := sandboxed(cmd, p.writable(), len(p.Spec.Network) > 0)
		if err != nil || !ok {
			p.warnSandbox("Linux namespaces aren't available on this system")
			cmd = mk()
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	logw := p.Log
	if logw == nil {
		logw = io.Discard
	}
	cmd.Stderr = prefixWriter{w: logw, prefix: p.Spec.Name + ": "}
	if err := cmd.Start(); err != nil {
		if p.Sandbox && sandboxUnavailable(err) {
			p.warnSandbox(fmt.Sprintf("user namespaces aren't allowed here (%v)", err))
			cmd = mk()
			stdin, _ = cmd.StdinPipe()
			stdout, _ = cmd.StdoutPipe()
			cmd.Stderr = prefixWriter{w: logw, prefix: p.Spec.Name + ": "}
			err = cmd.Start()
		}
		if err != nil {
			return nil, nil, ForeignError("%s: starting %s: %v", p.Spec.Name, strings.Join(p.Argv, " "), err)
		}
	}
	wire := p.Wire
	if wire == nil {
		wire = protocol.CBOR
	}
	limits := p.Limits
	if limits.MaxFrame == 0 {
		limits = protocol.DefaultLimits()
	}
	opts := protocol.Options{
		Wire:   wire,
		Limits: limits,
		Notifications: map[string]func(func(any) error){
			protocol.MethodLog: func(decode func(any) error) {
				var l protocol.LogParams
				if decode(&l) == nil {
					fmt.Fprintf(logw, "%s: %s: %s\n", p.Spec.Name, l.Level, l.Message)
				}
			},
			protocol.MethodProgress: func(func(any) error) {},
		},
	}
	if p.Host != nil {
		opts.Handlers = protocol.HostHandlers(p.Host)
	}
	conn := protocol.NewConn(stdout, stdin, opts)
	exited := make(chan struct{})
	go func() {
		conn.Serve()
		stdin.Close()
	}()
	go func() {
		cmd.Wait()
		close(exited)
	}()
	p.cmd, p.exited = cmd, exited
	init := p.Init
	init.Limits = limits
	hctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := protocol.ExtensionClient{C: conn}.Initialize(hctx, &init)
	if err != nil {
		cmd.Process.Kill()
		return nil, nil, ForeignError("%s: the handshake failed: %v", p.Spec.Name, err)
	}
	if major(res.Protocol) != major(protocol.Version) {
		cmd.Process.Kill()
		return nil, nil, ForeignError("%s: speaks protocol %s, and heron speaks %s", p.Spec.Name, res.Protocol, protocol.Version)
	}
	return conn, res, nil
}

func (p *Process) warnSandbox(why string) {
	if !p.warnedSB {
		p.warnedSB = true
		p.warn(fmt.Sprintf("%s: its file and network limits aren't enforced: %s", p.Spec.Name, why))
	}
}

// writable is what the extension may write: the files it declared, and
// the run-scoped temp directory.
func (p *Process) writable() []string {
	var out []string
	for _, f := range p.Spec.Files {
		if !filepath.IsAbs(f) {
			f = filepath.Join(p.Spec.Dir, f)
		}
		out = append(out, filepath.Clean(f))
	}
	if p.Init.Temp != "" {
		out = append(out, p.Init.Temp)
	}
	return out
}

func major(v string) string { return strings.SplitN(v, ".", 2)[0] }

// Call sends a request, starting the process if it isn't running. When the
// process exits while the call is in flight, the call fails with
// ForeignError and the next call starts it again.
func (p *Process) Call(ctx context.Context, method string, params, result any) error {
	return p.CallStream(ctx, method, params, result, nil)
}

// CallStream is Call for a streamed result.
func (p *Process) CallStream(ctx context.Context, method string, params, result any, item func(func(any) error) error) error {
	conn, _, err := p.ensure(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	exited := p.exited
	p.mu.Unlock()
	err = conn.CallStream(ctx, method, params, result, item)
	if err == nil {
		return nil
	}
	gone := errors.Is(err, protocol.ErrClosed)
	select {
	case <-exited:
		// Exited while the call was sent or in flight: writing to it may
		// fail with a broken pipe before the connection sees it close.
		var pe *protocol.Error
		gone = gone || !errors.As(err, &pe)
	default:
	}
	if gone {
		return ForeignError("%s: the extension exited during %s: %v", p.Spec.Name, method, conn.Err())
	}
	return err
}

// Implements returns the handshake's list of what the process implements.
func (p *Process) Implements() []protocol.Implemented {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.result == nil {
		return nil
	}
	return p.result.Implements
}

// Shutdown asks the process to exit, and kills it if it doesn't.
func (p *Process) Shutdown() {
	p.mu.Lock()
	p.closed = true
	conn, cmd, exited := p.conn, p.cmd, p.exited
	p.mu.Unlock()
	if conn == nil || cmd == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = conn.Call(ctx, protocol.MethodShutdown, &protocol.Empty{}, nil)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		cmd.Process.Kill()
		<-exited
	}
}

// Kill ends the process at once, as a crash would; tests use it.
func (p *Process) Kill() {
	p.mu.Lock()
	cmd, exited := p.cmd, p.exited
	p.mu.Unlock()
	if cmd != nil {
		cmd.Process.Kill()
		<-exited
	}
}

type prefixWriter struct {
	w      io.Writer
	prefix string
}

func (pw prefixWriter) Write(b []byte) (int, error) {
	for _, line := range strings.SplitAfter(string(b), "\n") {
		if line != "" {
			fmt.Fprint(pw.w, pw.prefix+line)
		}
	}
	return len(b), nil
}
