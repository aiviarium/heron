package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/helightdev/specs-v0/internal/core/registry"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// `heron check --watch` checks again when a file changes, and rechecks only
// the node the edit touched.
func TestCheckWatch(t *testing.T) {
	dir := t.TempDir()
	idx := filepath.Join(dir, "index.heron")
	os.WriteFile(idx, []byte("import = [\"std/doc@1\"]\na: section {{ One. }}\nb: section {{ Two. }}\n"), 0o644)
	var out, errb syncBuf
	env := &registry.Env{Dir: dir, Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb}
	stop := make(chan struct{})
	done := make(chan int)
	go func() { done <- runCheck(env, []string{"--watch"}, stop) }()
	waitFor(t, &errb, "watching 1 files")
	os.WriteFile(idx, []byte("import = [\"std/doc@1\"]\na: section {{ One. }}\nb: section {{ Two [x](#nowhere). }}\n"), 0o644)
	waitFor(t, &errb, "1 errors, 1 nodes rechecked")
	if !strings.Contains(out.String(), "the link to #nowhere doesn't resolve") {
		t.Errorf("out %q", out.String())
	}
	close(stop)
	<-done
}

func waitFor(t *testing.T, b *syncBuf, s string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(b.String(), s) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %q in %q", s, b.String())
}
