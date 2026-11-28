package server

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
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
	key  string    // the overlays of last
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

// Abs returns the identity of a workspace-relative path, refusing paths
// that leave the workspace.
func (w *Workspace) Abs(rel string) (resolve.FileID, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if rel == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%q is not a path in the workspace", rel)
	}
	return resolve.Canonical(filepath.Join(w.Root, clean))
}

// Analysis is a check of the tree with a set of unsaved texts.
type Analysis struct {
	W   *Workspace
	Res *check.Result
	// Pos maps an offset in a file to a 1-based line and column.
	Pos func(file string, off int) (int, int)
}

// Analyze checks the tree with the given unsaved texts, by relative path,
// and calls f with the result while holding the session. Unchanged files
// and nodes are served from the session's memo.
func (w *Workspace) Analyze(overlays map[string]string, f func(a *Analysis) error) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	ov := map[resolve.FileID]string{}
	keys := make([]string, 0, len(overlays))
	for rel, text := range overlays {
		id, err := w.Abs(rel)
		if err != nil {
			return err
		}
		ov[id] = text
		keys = append(keys, rel+"\x00"+text)
	}
	sort.Strings(keys)
	key := strings.Join(keys, "\x01")
	// The disk can change under the server, so a result is reused only
	// briefly, for the requests one edit makes.
	if w.last == nil || key != w.key || time.Since(w.at) > time.Second {
		w.sess.Loader.Overlay = ov
		w.last = w.sess.Check(w.Entry)
		w.key = key
		w.at = time.Now()
	}
	return f(&Analysis{W: w, Res: w.last, Pos: w.sess.Pos})
}
