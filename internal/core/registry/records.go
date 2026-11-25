package registry

import (
	"fmt"
	"sort"
)

// RecordKind is a kind of record a unit writes, such as a task run, a test
// run or an agent session. Every reader sees records of every kind through
// one store, package records.
type RecordKind struct {
	Name    string // stable, such as "run"; also the record's folder name
	Summary string
	// Version is the shape's version, written into every record so readers
	// can tell old records apart.
	Version int
	// Validate checks a record's data before it is written; nil accepts
	// any data. Data is plain, as eval.Plain gives it.
	Validate func(data map[string]any) error
}

// AddRecordKind registers a record kind; a name registered twice is an
// error.
func (r *Registry) AddRecordKind(k RecordKind) error {
	if k.Name == "" {
		return fmt.Errorf("registry: a record kind needs a name")
	}
	if r.records == nil {
		r.records = map[string]*RecordKind{}
	}
	if _, dup := r.records[k.Name]; dup {
		return fmt.Errorf("registry: record kind %q is registered twice", k.Name)
	}
	r.records[k.Name] = &k
	return nil
}

// MustAddRecordKind is AddRecordKind for registrations at start-up.
func (r *Registry) MustAddRecordKind(k RecordKind) {
	if err := r.AddRecordKind(k); err != nil {
		panic(err)
	}
}

// RecordKind returns the record kind registered under a name, or nil.
func (r *Registry) RecordKind(name string) *RecordKind { return r.records[name] }

// RecordKinds returns every registered record kind, sorted by name.
func (r *Registry) RecordKinds() []*RecordKind {
	out := make([]*RecordKind, 0, len(r.records))
	for _, k := range r.records {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
