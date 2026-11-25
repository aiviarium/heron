package prose

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/registry"
)

// ValeVersion is the Vale release setup installs when heron.lock pins none.
const ValeVersion = "v3.28.1"

// ValeModule is the Go module Vale installs from, through the Go proxy.
const ValeModule = "github.com/vale-cli/vale/v3/cmd/vale"

// SetupStep pins Vale in heron.lock and runs `vale sync`, for a repository
// with a .vale.ini at its root, so every machine and CI job lints with the
// same Vale and the same styles.
func SetupStep() registry.SetupStep {
	return registry.SetupStep{Name: "vale", Summary: "pins Vale and syncs its styles", Run: setupVale}
}

func setupVale(ctx context.Context, root string, old *registry.ToolPin, update bool) (*registry.ToolPin, any, error) {
	config := filepath.Join(root, ".vale.ini")
	if _, err := os.Stat(config); err != nil {
		return nil, nil, nil
	}
	want := ValeVersion
	if old != nil && old.Version != "" {
		want = old.Version
	}
	bin := Binary()
	if bin == "" {
		return nil, nil, fmt.Errorf("Vale isn't installed; install it with `go install %s@%s`", ValeModule, want)
	}
	version := binaryVersion(bin)
	sum, err := fileSum(bin)
	if err != nil {
		return nil, nil, err
	}
	if old != nil && old.Version != version && !update {
		return nil, nil, fmt.Errorf("heron.lock pins Vale %s, but %s is %s; install it with `go install %s@%s`, or run setup with --update to move the pin",
			old.Version, bin, version, ValeModule, old.Version)
	}
	cmd := exec.CommandContext(ctx, bin, "--config="+config, "sync")
	cmd.Dir = root
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("vale sync: %v: %s", err, firstLine(strings.TrimSpace(out.String())))
	}
	state := "pinned " + version
	if old != nil && old.Version == version {
		state = "ok " + version
	} else if old != nil {
		state = fmt.Sprintf("moved %s to %s", old.Version, version)
	}
	return &registry.ToolPin{Version: version, Checksum: sum}, map[string]any{"state": state, "synced": true}, nil
}

// binaryVersion reads Vale's module version from the binary, as `go
// install` records it, or asks `vale --version`.
func binaryVersion(bin string) string {
	if info, err := buildinfo.ReadFile(bin); err == nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	out, err := exec.Command(bin, "--version").Output()
	if err != nil {
		return "unknown"
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return "unknown"
	}
	v := f[len(f)-1]
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

func fileSum(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
