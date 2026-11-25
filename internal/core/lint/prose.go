package lint

import (
	"sync"

	"github.com/aiviarium/heron/internal/core/check"
	"github.com/aiviarium/heron/internal/core/diag"
	"github.com/aiviarium/heron/internal/core/lint/prose"
	"github.com/aiviarium/heron/internal/core/registry"
	"github.com/aiviarium/heron/internal/core/resolve"
)

// ProseCache keeps each prose provider's findings per region, by the
// region's content hash, so an edit re-lints only the regions it changed.
type ProseCache struct {
	mu sync.Mutex
	m  map[string][]registry.ProseFinding // provider + "\x00" + hash → findings with Region 0
	// Linted counts the regions sent to providers, for tests.
	Linted int
}

// NewProseCache returns an empty cache.
func NewProseCache() *ProseCache { return &ProseCache{m: map[string][]registry.ProseFinding{}} }

var defaultProse = NewProseCache()

// RunWith runs every registered lint and prose provider, keeping prose
// results in the given cache.
func RunWith(r *registry.Registry, res *check.Result, cache *ProseCache) diag.List {
	var out diag.List
	res.Complete() // lints read the checker's facts about every node
	for _, l := range r.Lints() {
		l.Run(&Input{Tree: res.Tree, Checker: res.Checker, name: l.Name, out: &out})
	}
	if ps := r.ProseProviders(); len(ps) > 0 {
		regions := prose.Project(res.Tree, res.Checker)
		for _, p := range ps {
			key, check := "", p.Check
			if p.Prepare != nil {
				key, check = p.Prepare(&Input{Tree: res.Tree, Checker: res.Checker, name: p.Name, out: &out})
			}
			if check == nil {
				continue
			}
			runProvider(p.Name+"\x00"+key, p, check, regions, res, cache, &out)
		}
	}
	return out.Sort()
}

func runProvider(prefix string, p *registry.ProseProvider, check func([]*registry.ProseRegion) []registry.ProseFinding, regions []*prose.Region, res *check.Result, cache *ProseCache, out *diag.List) {
	cache.mu.Lock()
	var missing []*prose.Region
	seen := map[string]bool{}
	for _, r := range regions {
		k := prefix + "\x00" + r.Hash
		if _, ok := cache.m[k]; !ok && !seen[k] {
			seen[k] = true
			missing = append(missing, r)
		}
	}
	cache.Linted += len(missing)
	cache.mu.Unlock()

	var found []registry.ProseFinding
	if len(missing) > 0 {
		found = check(missing)
	}

	cache.mu.Lock()
	for _, r := range missing {
		cache.m[prefix+"\x00"+r.Hash] = []registry.ProseFinding{}
	}
	for _, f := range found {
		if f.Region < 0 || f.Region >= len(missing) {
			continue
		}
		k := prefix + "\x00" + missing[f.Region].Hash
		f.Region = 0
		cache.m[k] = append(cache.m[k], f)
	}
	results := make([][]registry.ProseFinding, len(regions))
	for i, r := range regions {
		results[i] = cache.m[prefix+"\x00"+r.Hash]
	}
	cache.mu.Unlock()

	for i, r := range regions {
		for _, f := range results[i] {
			code := p.Name + "." + f.Rule
			owner, _ := r.Owner.(*resolve.Node)
			if allowed(res.Checker, owner, code) || allowed(res.Checker, owner, p.Name) {
				continue
			}
			if f.Start < 0 || f.End > len(r.Text) || f.Start > f.End {
				continue
			}
			s, e, _ := prose.SourceRange(r, f.Start, f.End)
			d := diag.Diagnostic{File: r.File, Start: s, End: e, Severity: f.Severity, Code: code, Message: f.Message}
			for _, fx := range f.Fixes {
				if fx.Start < 0 || fx.End > len(r.Text) || fx.Start > fx.End || prose.Masked(r, fx.Start, fx.End) {
					continue
				}
				fs, fe, exact := prose.SourceRange(r, fx.Start, fx.End)
				if !exact {
					continue
				}
				d.Fixes = append(d.Fixes, diag.Fix{Title: fx.Title, Edits: []diag.Edit{{File: r.File, Start: fs, End: fe, New: fx.New}}, Suggestion: fx.Suggestion})
			}
			out.Add(d)
		}
	}
}
