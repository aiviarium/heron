package lsp

import (
	"testing"

	"github.com/aiviarium/heron/internal/core/check"
)

// A render that fails answers its error, and no markup.
func TestPreviewRenderFails(t *testing.T) {
	s := NewSession(nil, Options{})
	html, err := renderPreview(s, &check.Result{}, "x.heron", nil)
	if err == nil || html != "" {
		t.Errorf("a failed render: %q %v", html, err)
	}
}
