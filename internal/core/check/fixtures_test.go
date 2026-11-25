package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiviarium/heron/internal/core/diag"
)

func repoRoot() string { return filepath.Join("..", "..", "..") }

// Done means, criterion 1: every Heron code block of unit 1's tabs checks
// clean against the standard packages, except blocks that show an error and
// the pending ones, which must still fail so the list can't go stale.
func TestFixturesCheckClean(t *testing.T) {
	clean := 0
	runs := CheckFixtures(t, repoRoot())
	for _, r := range runs {
		switch {
		case len(r.Errs) == 0 && r.Pending != "":
			t.Errorf("%s checks clean now: remove it from fixtures/pending.txt", r.Block.Key())
		case len(r.Errs) == 0:
			clean++
		case r.Pending == "":
			for _, d := range r.Errs {
				t.Errorf("%s: %s", r.Block.Key(), diag.Format(d, r.Session.Pos))
			}
		default:
			if os.Getenv("HERON_PENDING") != "" {
				for _, d := range r.Errs {
					t.Logf("pending %s: %s", r.Block.Key(), diag.Format(d, r.Session.Pos))
				}
			}
		}
	}
	t.Logf("%d of %d unit-1 Heron blocks check clean", clean, len(runs))
}
