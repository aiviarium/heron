//go:build !linux

package extension

import (
	"errors"
	"os/exec"
)

// Namespaces exist only on Linux; elsewhere limits are declared but not
// enforced, and the caller warns.
func sandboxed(cmd *exec.Cmd, writable []string, network bool) (bool, error) {
	return false, nil
}

func sandboxUnavailable(err error) bool { return errors.Is(err, errors.ErrUnsupported) }
