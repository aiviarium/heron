// Package records stores audit records, release output and caches as data
// files in the configured locations. Record kinds register through the
// registry, so every unit's records are written and read through one store.
//
// A record is one JSON file, <location>/<kind>/<id>.json, holding an
// envelope with the kind, the kind's version, the record's id and time, and
// its data. Ids sort by time, so a folder listing is the history.
package records

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiviarium/heron/internal/core/registry"
)

// DefaultPath is where records go when nothing configures a location,
// relative to the project's root, as in the DevOps tab's
// `audit { path = ".heron/audit/" }`.
const DefaultPath = ".heron/audit"

// Config is where records are kept.
type Config struct {
	// Path is the location every kind's records go under; relative paths
	// start at the project's root.
	Path string
	// Paths overrides the location for single kinds, by kind name.
	Paths map[string]string
	// Keep is how long records are kept by Prune; zero keeps them all.
	Keep time.Duration
}

// Store reads and writes records of the kinds registered in a registry.
type Store struct {
	root string // the project's root
	cfg  Config
	reg  *registry.Registry
	// Now is the clock records are stamped with.
	Now func() time.Time
}

// Open returns the store for a project root.
func Open(root string, cfg Config, r *registry.Registry) *Store {
	if cfg.Path == "" {
		cfg.Path = DefaultPath
	}
	return &Store{root: root, cfg: cfg, reg: r, Now: time.Now}
}

// Record is one stored record.
type Record struct {
	Kind    string         `json:"kind"`
	Version int            `json:"version"`
	ID      string         `json:"id"`
	Time    time.Time      `json:"time"`
	Data    map[string]any `json:"data"`
	// Path is the file the record is stored in; it isn't written.
	Path string `json:"-"`
}

// Dir returns the folder a kind's records are stored in.
func (s *Store) Dir(kind string) string {
	p := s.cfg.Path
	if o, ok := s.cfg.Paths[kind]; ok {
		p = o
	} else {
		p = filepath.Join(p, kind)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(s.root, p)
	}
	return p
}

func (s *Store) kind(name string) (*registry.RecordKind, error) {
	k := s.reg.RecordKind(name)
	if k == nil {
		return nil, fmt.Errorf("records: no record kind %q is registered", name)
	}
	return k, nil
}

// Write validates data against its kind and stores it as a new record.
func (s *Store) Write(kind string, data map[string]any) (*Record, error) {
	k, err := s.kind(kind)
	if err != nil {
		return nil, err
	}
	if k.Validate != nil {
		if err := k.Validate(data); err != nil {
			return nil, fmt.Errorf("records: a %s record is invalid: %w", kind, err)
		}
	}
	now := s.Now().UTC()
	rec := &Record{Kind: kind, Version: k.Version, ID: newID(now), Time: now, Data: data}
	b, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("records: a %s record isn't data: %w", kind, err)
	}
	dir := s.Dir(kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	rec.Path = filepath.Join(dir, rec.ID+".json")
	tmp := rec.Path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, rec.Path); err != nil {
		return nil, err
	}
	return rec, nil
}

// List returns a kind's records, newest first. A folder that doesn't exist
// yet holds no records.
func (s *Store) List(kind string) ([]*Record, error) {
	if _, err := s.kind(kind); err != nil {
		return nil, err
	}
	dir := s.Dir(kind)
	ents, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*Record
	for _, e := range ents {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		r, err := readFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

// Read returns one record of a kind by its id.
func (s *Store) Read(kind, id string) (*Record, error) {
	if _, err := s.kind(kind); err != nil {
		return nil, err
	}
	if strings.ContainsAny(id, `/\`) {
		return nil, fmt.Errorf("records: bad id %q", id)
	}
	return readFile(filepath.Join(s.Dir(kind), id+".json"))
}

// Prune removes records older than the configured Keep, of every
// registered kind, and returns how many it removed.
func (s *Store) Prune() (int, error) {
	if s.cfg.Keep <= 0 {
		return 0, nil
	}
	cut := s.Now().UTC().Add(-s.cfg.Keep)
	n := 0
	for _, k := range s.reg.RecordKinds() {
		rs, err := s.List(k.Name)
		if err != nil {
			return n, err
		}
		for _, r := range rs {
			if r.Time.Before(cut) {
				if err := os.Remove(r.Path); err != nil {
					return n, err
				}
				n++
			}
		}
	}
	return n, nil
}

func readFile(path string) (*Record, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Record
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("records: %s: %w", path, err)
	}
	r.Path = path
	return &r, nil
}

// newID is the time to the millisecond and a random suffix, so ids sort by
// time and two runs in the same millisecond don't collide.
func newID(t time.Time) string {
	var b [3]byte
	_, _ = rand.Read(b[:])
	return t.Format("20060102T150405.000Z") + "-" + hex.EncodeToString(b[:])
}
