package registry

import (
	"fmt"
	"sort"

	"github.com/helightdev/specs-v0/internal/core/diag"
)

// ProseRegion is one block of the prose view: a paragraph, heading, list
// item, quote or code block, projected from a file's `|` lines, `{{ }}`
// bodies, `=|` values, `///` docs or markup-typed strings. It's plain data,
// so a provider in another process receives it as it is.
type ProseRegion struct {
	// Hash identifies the region's content and context; results are
	// cached under it.
	Hash string `json:"hash"`
	File string `json:"file"`
	// Start and End are the block's byte range in the file.
	Start int `json:"start"`
	End   int `json:"end"`

	// Context: the node the text belongs to, its kind and its path, and
	// where the text was written: "prose" (`|` lines), "block" (`{{ }}`),
	// "value" (`=|`), "doc" (`///`), "string" (a markup-typed string) or
	// "file" (a prose file).
	Node   string `json:"node"`
	Kind   string `json:"kind,omitempty"`
	Source string `json:"source"`

	// Block is "paragraph", "heading", "item", "quote" or "code". Level is
	// a heading's level; Ordered marks an item of a numbered list. Group
	// numbers the run of text a block came from, so consecutive items of
	// one group form one list.
	Block   string `json:"block"`
	Level   int    `json:"level,omitempty"`
	Ordered bool   `json:"ordered,omitempty"`
	Group   int    `json:"group"`

	// Text is the projected text. Masked spans keep their source text and
	// are listed in Masks; a provider never reads them as words.
	Text  string      `json:"text"`
	Masks []ProseMask `json:"masks,omitempty"`
	// Map ties every character of Text to its byte range in the file.
	Map []ProseSeg `json:"map"`

	// Owner is the *resolve.Node the text belongs to, for in-process
	// providers; it isn't serialised.
	Owner any `json:"-"`
}

// ProseMask is a masked range of a region's text: inline "code", an
// "insert", a "link" to a node or a "tag" with its attributes.
type ProseMask struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Kind  string `json:"kind"`
}

// ProseSeg maps Text[At:At+Len] to the file's bytes [Src, Src+SrcLen).
// When Len equals SrcLen and Exact is set, characters map one to one;
// otherwise, as for the space that joins two lines, the whole range maps to
// the whole source range.
type ProseSeg struct {
	At     int  `json:"at"`
	Len    int  `json:"len"`
	Src    int  `json:"src"`
	SrcLen int  `json:"srcLen"`
	Exact  bool `json:"exact"`
}

// ProseFinding is what a provider returns: a range of one region's text,
// the rule it breaks, and fixes against the projection.
type ProseFinding struct {
	Region   int           `json:"region"` // index into the regions passed
	Start    int           `json:"start"`
	End      int           `json:"end"`
	Rule     string        `json:"rule"` // reported as Provider.Rule
	Severity diag.Severity `json:"severity"`
	Message  string        `json:"message"`
	Fixes    []ProseFix    `json:"fixes,omitempty"`
}

// ProseFix replaces a range of a region's text. A suggestion is one guess
// among others, which only an editor offers.
type ProseFix struct {
	Title      string `json:"title"`
	Start      int    `json:"start"`
	End        int    `json:"end"`
	New        string `json:"new"`
	Suggestion bool   `json:"suggestion,omitempty"`
}

// ProseProvider is a prose linter: it reads projected regions and returns
// findings, which the core maps back onto the source and reports through
// the lint pipeline as "Name.Rule". Check sees only regions whose results
// aren't cached, and must report on a region from that region alone.
type ProseProvider struct {
	Name    string // such as "Vale"; findings are suppressed by "Name" or "Name.Rule"
	Summary string
	Check   func(regions []*ProseRegion) []ProseFinding
	// Prepare, when set, is called once per lint run with the lint
	// package's *lint.Input, the checked tree, and replaces Check: it
	// returns a key that joins every region's cache key, such as a hash of
	// the configuration or of the tree facts the provider reads, and the
	// check for the regions not cached under it. A provider that knows the
	// tree, or reads files beside it, uses it.
	Prepare func(in any) (key string, check func(regions []*ProseRegion) []ProseFinding)
}

// AddProseProvider registers a prose provider by name.
func (r *Registry) AddProseProvider(p ProseProvider) error {
	if p.Name == "" || (p.Check == nil && p.Prepare == nil) {
		return fmt.Errorf("registry: a prose provider needs a name and a Check or Prepare function")
	}
	if r.prose == nil {
		r.prose = map[string]*ProseProvider{}
	}
	if _, dup := r.prose[p.Name]; dup {
		return fmt.Errorf("registry: prose provider %q is registered twice", p.Name)
	}
	r.prose[p.Name] = &p
	return nil
}

// MustAddProseProvider is AddProseProvider for registrations at start-up.
func (r *Registry) MustAddProseProvider(p ProseProvider) {
	if err := r.AddProseProvider(p); err != nil {
		panic(err)
	}
}

// ProseProviders returns every registered prose provider, sorted by name.
func (r *Registry) ProseProviders() []*ProseProvider {
	out := make([]*ProseProvider, 0, len(r.prose))
	for _, p := range r.prose {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
