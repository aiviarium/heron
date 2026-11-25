package cli

import (
	"fmt"
	"os"
	"sort"

	"github.com/helightdev/specs-v0/internal/core/diag"
)

// autoFix is the first fix of a diagnostic that isn't a suggestion, or nil.
func autoFix(d diag.Diagnostic) *diag.Fix {
	for i := range d.Fixes {
		if !d.Fixes[i].Suggestion {
			return &d.Fixes[i]
		}
	}
	return nil
}

// applyFixes applies the first fix of each diagnostic that has one and isn't
// a suggestion, skipping a fix that overlaps one already taken, and returns
// the diagnostics left.
func applyFixes(ds diag.List) (diag.List, int, error) {
	byFile := map[string][]diag.Edit{}
	var rest diag.List
	n := 0
	for _, d := range ds {
		fx := autoFix(d)
		if fx == nil {
			rest = append(rest, d)
			continue
		}
		ok := true
		for _, e := range fx.Edits {
			for _, x := range byFile[e.File] {
				if e.Start < x.End && x.Start < e.End || e.Start == x.Start {
					ok = false
				}
			}
		}
		if !ok {
			rest = append(rest, d)
			continue
		}
		for _, e := range fx.Edits {
			byFile[e.File] = append(byFile[e.File], e)
		}
		n++
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, 0, err
		}
		src := string(b)
		edits := byFile[f]
		sort.Slice(edits, func(i, j int) bool { return edits[i].Start > edits[j].Start })
		for _, e := range edits {
			if e.Start < 0 || e.End > len(src) || e.Start > e.End {
				return nil, 0, fmt.Errorf("%s: a fix is out of range", f)
			}
			src = src[:e.Start] + e.New + src[e.End:]
		}
		st, err := os.Stat(f)
		if err != nil {
			return nil, 0, err
		}
		if err := os.WriteFile(f, []byte(src), st.Mode().Perm()); err != nil {
			return nil, 0, err
		}
	}
	return rest, n, nil
}
