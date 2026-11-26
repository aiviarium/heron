package server

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/aiviarium/heron/internal/core/records"
	"github.com/aiviarium/heron/internal/tools/server/api"
)

// scanEvery is how often the hub looks at the disk while someone listens.
const scanEvery = 700 * time.Millisecond

// hub watches the workspace while clients listen to `watch`: it scans the
// files every scanEvery, or at once when the server itself changed one, and
// sends each listener the files created, changed or removed since the last
// scan, a `problems` event when a file of the tree changed, and a `results`
// event when a run recorded something.
type hub struct {
	ws *Workspace

	mu      sync.Mutex
	subs    map[chan api.WorkspaceEvent]bool
	running bool
	kick    chan struct{}
}

func newHub(w *Workspace) *hub {
	return &hub{ws: w, subs: map[chan api.WorkspaceEvent]bool{}, kick: make(chan struct{}, 1)}
}

// poke asks for a scan now, after the server wrote a file.
func (h *hub) poke() {
	if h == nil {
		return
	}
	select {
	case h.kick <- struct{}{}:
	default:
	}
}

func (h *hub) subscribe() (chan api.WorkspaceEvent, func()) {
	ch := make(chan api.WorkspaceEvent, 256)
	h.mu.Lock()
	h.subs[ch] = true
	if !h.running {
		h.running = true
		prev, runs := h.snapshot()
		go h.loop(prev, runs)
	}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *hub) send(e api.WorkspaceEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- e:
		default: // a listener that doesn't read loses events, not the server
		}
	}
}

type stamp struct {
	size int64
	mod  time.Time
	dir  bool
}

// snapshot is every file and folder of the workspace, as list_files sees
// them, and the newest time of the run records.
func (h *hub) snapshot() (map[string]stamp, time.Time) {
	out := map[string]stamp{}
	root := h.ws.Root
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		if d.IsDir() && skipped[d.Name()] {
			return filepath.SkipDir
		}
		if info, err := d.Info(); err == nil {
			out[h.ws.Rel(resolveID(p))] = stamp{size: info.Size(), mod: info.ModTime(), dir: d.IsDir()}
		}
		return nil
	})
	var newest time.Time
	dir := records.Open(root, records.Config{}, h.ws.Reg).Dir(records.RunKind)
	if ents, err := os.ReadDir(dir); err == nil {
		for _, e := range ents {
			if info, err := e.Info(); err == nil && info.ModTime().After(newest) {
				newest = info.ModTime()
			}
		}
	}
	return out, newest
}

func (h *hub) loop(prev map[string]stamp, prevRuns time.Time) {
	t := time.NewTicker(scanEvery)
	defer t.Stop()
	for {
		select {
		case <-t.C:
		case <-h.kick:
		}
		h.mu.Lock()
		if len(h.subs) == 0 {
			h.running = false
			h.mu.Unlock()
			return
		}
		h.mu.Unlock()
		cur, runs := h.snapshot()
		var evs []api.WorkspaceEvent
		for p, st := range cur {
			old, ok := prev[p]
			switch {
			case !ok:
				evs = append(evs, fileEvent(p, "created"))
			case !st.dir && (old.size != st.size || !old.mod.Equal(st.mod)):
				evs = append(evs, fileEvent(p, "changed"))
			}
		}
		for p := range prev {
			if _, ok := cur[p]; !ok {
				evs = append(evs, fileEvent(p, "removed"))
			}
		}
		sort.Slice(evs, func(i, j int) bool { return *evs[i].Path < *evs[j].Path })
		for _, e := range evs {
			h.send(e)
		}
		if len(evs) > 0 {
			h.ws.Invalidate()
			h.send(api.WorkspaceEvent{Type: "problems"})
		}
		if runs.After(prevRuns) {
			h.send(api.WorkspaceEvent{Type: "results"})
		}
		prev, prevRuns = cur, runs
	}
}

func fileEvent(p, change string) api.WorkspaceEvent {
	return api.WorkspaceEvent{Type: "file", Path: &p, Change: &change}
}

func (s *service) Watch(ctx context.Context, send func(*api.WorkspaceEvent) error) error {
	ch, done := s.ws.events.subscribe()
	defer done()
	// The stream opens with `ready`, from when every change is sent.
	if err := send(&api.WorkspaceEvent{Type: "ready"}); err != nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case e := <-ch:
			if err := send(&e); err != nil {
				return nil
			}
		}
	}
}
