package types

import "github.com/aiviarium/heron/internal/core/resolve"

// AcceptEntry is one `accept(…)` of a kind's body, as the schema
// reference shows it: the kind or the category it takes, or the mode or
// text it reads, and its bound.
type AcceptEntry struct {
	Kind     *resolve.Node
	Category *resolve.Node
	Mode     string // "prose", "code" or "structure", or ""
	Text     string // the type of the text, as Verbatim
	Max      int    // 0 for no bound
}

// AcceptEntries are the entries of a kind's body in the order written, or
// those of the kind it extends where it has none of its own.
func (c *Checker) AcceptEntries(k *resolve.Node) []AcceptEntry {
	var out []AcceptEntry
	for _, a := range c.acceptList(k) {
		out = append(out, AcceptEntry{Kind: a.kind, Category: a.category, Mode: a.mode, Text: a.text, Max: a.max})
	}
	return out
}
