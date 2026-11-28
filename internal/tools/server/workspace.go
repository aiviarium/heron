package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// Workspace is the repository the server runs on: its entry point and one
// check session that every request shares, so the analysis stays
// incremental across edits.
type Workspace struct {
	Root  string // the entry point's folder
	Entry resolve.FileID
	Reg   *registry.Registry

	mu   sync.Mutex
	sess *check.Session
	last *check.Result
	at   time.Time // when last was checked
}

// OpenWorkspace opens the workspace of an entry point, a file or a folder
// with index.heron.
func OpenWorkspace(entry string, r *registry.Registry) (*Workspace, error) {
	if st, err := os.Stat(entry); err == nil && st.IsDir() {
		entry = filepath.Join(entry, "index.heron")
	}
	if _, err := os.Stat(entry); err != nil {
		return nil, fmt.Errorf("no entry point %s", entry)
	}
	id, err := resolve.Canonical(entry)
	if err != nil {
		return nil, err
	}
	return &Workspace{Root: id.Dir(), Entry: id, Reg: r, sess: check.NewSession()}, nil
}

// Rel returns a file's path relative to the workspace, with slashes.
func (w *Workspace) Rel(id resolve.FileID) string {
	rel, err := filepath.Rel(w.Root, string(id))
	if err != nil {
		return string(id)
	}
	return filepath.ToSlash(rel)
}

// Analysis is a check of the tree as it stands on disk.
type Analysis struct {
	W   *Workspace
	Res *check.Result
	// Pos maps an offset in a file to a 1-based line and column.
	Pos func(file string, off int) (int, int)
}

// Analyze checks the tree and calls f with the result while holding the
// session. Unchanged files and nodes are served from the session's memo.
func (w *Workspace) Analyze(f func(a *Analysis) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	// The disk can change under the server, so a result is reused only
	// briefly, for the requests one page load makes.
	if w.last == nil || time.Since(w.at) > time.Second {
		w.last = w.sess.Check(w.Entry)
		w.at = time.Now()
	}
	return f(&Analysis{W: w, Res: w.last, Pos: w.sess.Pos})
}
