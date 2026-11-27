// Package version is which heron this binary is: the product version, the
// version of the Heron spec it implements, and its build's commit and
// time. scripts/build.sh stamps Commit and Built with -ldflags -X; a plain
// `go build` in a checkout takes them from the VCS stamp Go records.
package version

import (
	"runtime"
	"runtime/debug"
	"strings"
)

// Version is the product version: the spec version it implements, then
// the build counter after +. TestVersionMatchesProject keeps it equal to
// the project tree's version.
var Version = "0.6.0+build1"

// Commit and Built are the commit the binary was built from and when, in
// UTC as RFC 3339; empty unless stamped or recorded by Go.
var (
	Commit = ""
	Built  = ""
)

// Spec is the version of the Heron spec this build implements: the
// product version up to its build metadata.
func Spec() string {
	v, _, _ := strings.Cut(Version, "+")
	return v
}

// Info is the build's facts, each "unknown" where the binary doesn't know
// it.
type Info struct {
	Version, Spec, Commit, Built, Platform string
}

// Read returns the build's facts.
func Read() Info {
	in := Info{Version: Version, Spec: Spec(), Commit: Commit, Built: Built, Platform: runtime.GOOS + "/" + runtime.GOARCH}
	if bi, ok := debug.ReadBuildInfo(); ok {
		dirty := false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if in.Commit == "" {
					in.Commit = s.Value
				}
			case "vcs.time":
				if in.Built == "" {
					in.Built = s.Value
				}
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if dirty && Commit == "" && in.Commit != "" {
			in.Commit += "-dirty"
		}
	}
	if in.Commit == "" {
		in.Commit = "unknown"
	}
	if in.Built == "" {
		in.Built = "unknown"
	}
	return in
}

// Lines are the facts as `heron version` prints them, one per line.
func (in Info) Lines() string {
	return "heron " + in.Version + "\nspec " + in.Spec + "\ncommit " + in.Commit + "\nbuilt " + in.Built + "\nplatform " + in.Platform + "\n"
}
