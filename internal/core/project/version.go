package project

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/helightdev/specs-v0/internal/core/diag"
	"github.com/helightdev/specs-v0/internal/core/resolve"
)

// Scheme is the version scheme of a project that implements a spec. Both
// start with the implemented spec version, SM.Sm.Sp, copied exactly; only
// what follows belongs to the implementation.
type Scheme int

const (
	// SchemeSpec, the default, is SM.Sm.Sp+<tag>IB: the spec version and
	// optional build metadata, a label such as build, rc or beta and the
	// implementation build counter, as 2.1.3+build4.
	SchemeSpec Scheme = iota
	// SchemePatch is SM.Sm.Sp.IP+<tag>IB, opted into with
	// `versioning.{ scheme = "SM.Sm.Sp.IP" }`: IP is an implementation
	// patch that implementers bump, 0 again on a spec change.
	SchemePatch
)

// Scheme names as a versioning config writes them.
const (
	SchemeSpecName  = "SM.Sm.Sp"
	SchemePatchName = "SM.Sm.Sp.IP"
)

func (s Scheme) String() string {
	if s == SchemePatch {
		return SchemePatchName + "+<tag>IB"
	}
	return SchemeSpecName + "+<tag>IB"
}

// ParseScheme reads a versioning config's scheme, with or without the
// metadata suffix; "" is the default.
func ParseScheme(s string) (Scheme, error) {
	switch strings.TrimSuffix(strings.TrimSpace(s), "+<tag>IB") {
	case "", SchemeSpecName:
		return SchemeSpec, nil
	case SchemePatchName:
		return SchemePatch, nil
	}
	return SchemeSpec, fmt.Errorf("%q is no implementation version scheme: a project that implements a spec uses %q (the default) or %q", s, SchemeSpecName, SchemePatchName)
}

// Version is a version of a project that implements a spec.
type Version struct {
	// Spec is the implemented spec version: major, minor, patch.
	Spec [3]int
	// Patch is the implementation patch, IP, in SchemePatch; HasPatch says
	// the version has one.
	Patch    int
	HasPatch bool
	// Tag and Build are the metadata after "+", as "rc" and 2 in +rc2;
	// Tag is "" when there is none.
	Tag   string
	Build int
}

var (
	numRe  = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)
	metaRe = regexp.MustCompile(`^([a-z]+)(0|[1-9][0-9]*)$`)
)

// Parse reads a version in a scheme, as "2.1.3+build4" in SchemeSpec or
// "2.1.3.4+rc2" in SchemePatch. A leading "v", as in a git tag, is
// accepted.
func Parse(s string, scheme Scheme) (Version, error) {
	var v Version
	in := strings.TrimPrefix(s, "v")
	core, meta, hasMeta := strings.Cut(in, "+")
	parts := strings.Split(core, ".")
	want := 3
	if scheme == SchemePatch {
		want = 4
	}
	if len(parts) != want {
		return v, fmt.Errorf("%q has %d numbers before the metadata; the %s scheme has %d", s, len(parts), scheme, want)
	}
	for i, p := range parts {
		if !numRe.MatchString(p) {
			return v, fmt.Errorf("%q: %q is no version number (digits, no leading zero)", s, p)
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return v, fmt.Errorf("%q: %v", s, err)
		}
		if i < 3 {
			v.Spec[i] = n
		} else {
			v.Patch, v.HasPatch = n, true
		}
	}
	if hasMeta {
		m := metaRe.FindStringSubmatch(meta)
		if m == nil {
			return v, fmt.Errorf("%q: the metadata after + is a lower-case label and the build number, as +build4 or +rc2", s)
		}
		v.Tag = m[1]
		v.Build, _ = strconv.Atoi(m[2])
	}
	return v, nil
}

// ParseSpecVersion reads a spec version, SM.Sm.Sp.
func ParseSpecVersion(s string) ([3]int, error) {
	v, err := Parse(s, SchemeSpec)
	if err != nil {
		return [3]int{}, err
	}
	if v.Tag != "" {
		return [3]int{}, fmt.Errorf("%q: a spec version is SM.Sm.Sp, without metadata", s)
	}
	return v.Spec, nil
}

// SpecString is the version's spec part, as "2.1.3".
func (v Version) SpecString() string {
	return fmt.Sprintf("%d.%d.%d", v.Spec[0], v.Spec[1], v.Spec[2])
}

func (v Version) String() string {
	s := v.SpecString()
	if v.HasPatch {
		s += "." + strconv.Itoa(v.Patch)
	}
	if v.Tag != "" {
		s += "+" + v.Tag + strconv.Itoa(v.Build)
	}
	return s
}

// GitTag is the git tag that records a release of the version, as
// "v2.1.3+build4".
func (v Version) GitTag() string { return "v" + v.String() }

// Compare orders versions by the spec version, then the implementation
// patch, then the build counter. Semver gives `+` metadata no precedence,
// so the counter is what orders two builds of one spec version.
func Compare(a, b Version) int {
	for i := 0; i < 3; i++ {
		if a.Spec[i] != b.Spec[i] {
			return cmpInt(a.Spec[i], b.Spec[i])
		}
	}
	if a.Patch != b.Patch {
		return cmpInt(a.Patch, b.Patch)
	}
	return cmpInt(a.Build, b.Build)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Next computes the version of the next release from the releases so far
// and the implemented spec version. The build counter counts the releases
// of one spec version and starts again on a spec change. In SchemeSpec the
// next release is the spec version with the counter bumped, labelled tag
// ("build" when empty). In SchemePatch the implementation patch is bumped,
// or 0 on a spec change, and the metadata is added only when tag is given.
func Next(releases []Version, spec [3]int, scheme Scheme, tag string) Version {
	v := Version{Spec: spec}
	same := false
	maxPatch, maxBuild := 0, 0
	for _, r := range releases {
		if r.Spec != spec {
			continue
		}
		if !same || r.Patch > maxPatch {
			maxPatch = r.Patch
		}
		if r.Build > maxBuild {
			maxBuild = r.Build
		}
		same = true
	}
	switch scheme {
	case SchemePatch:
		v.HasPatch = true
		if same {
			v.Patch = maxPatch + 1
		}
		if tag != "" {
			v.Tag, v.Build = tag, maxBuild+1
		}
	default:
		if tag == "" {
			tag = "build"
		}
		v.Tag, v.Build = tag, maxBuild+1
	}
	return v
}

// schemeOf reads the scheme an implements node's versioning config sets.
func schemeOf(n *resolve.Node) Scheme {
	s, _, _ := schemeName(n)
	sc, _ := ParseScheme(s)
	return sc
}

func schemeName(n *resolve.Node) (string, resolve.Site, bool) {
	v := n.Child("versioning")
	if v == nil {
		return "", resolve.Site{}, false
	}
	sc := v.Child("scheme")
	if sc == nil || sc.Value == nil {
		return "", resolve.Site{}, false
	}
	s, _ := resolve.StringValue(sc.Value.Syntax())
	return s, sc.ValueSite, true
}

// Release is a release node with its parsed version.
type Release struct {
	Node    *resolve.Node
	Version Version
	Err     error
}

// ReleaseList returns the project's releases parsed in its scheme, newest
// first; those that don't parse come last with their error.
func (p *Project) ReleaseList() []Release {
	var out []Release
	for _, n := range p.Releases {
		s, _ := ArgString(KindArgs(n), 0, "version")
		v, err := Parse(s, p.Scheme)
		out = append(out, Release{Node: n, Version: v, Err: err})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].Err == nil) != (out[j].Err == nil) {
			return out[i].Err == nil
		}
		return Compare(out[i].Version, out[j].Version) > 0
	})
	return out
}

// NextVersion is the version the next release takes.
func (p *Project) NextVersion(tag string) (Version, error) {
	spec, err := ParseSpecVersion(p.At)
	if err != nil {
		return Version{}, err
	}
	var vs []Version
	for _, r := range p.ReleaseList() {
		if r.Err == nil {
			vs = append(vs, r.Version)
		}
	}
	return Next(vs, spec, p.Scheme, tag), nil
}

// checkVersions reports a scheme no project uses, an `at` that isn't the
// spec package's version, a project version or release that doesn't
// follow the scheme, a project version whose first three parts aren't
// the implemented spec version, and releases repeated or newer than the
// project version.
func (p *Project) checkVersions() diag.List {
	var ds diag.List
	if s, site, ok := schemeName(p.Node); ok {
		if _, err := ParseScheme(s); err != nil {
			errAt(&ds, site, "version", "%v", err)
		}
	}
	spec, err := ParseSpecVersion(p.At)
	switch {
	case p.At == "":
		errAt(&ds, p.Node.Decl, "version", "implements names the spec version it implements, as at = %q", p.SpecVersion())
	case err != nil:
		errAt(&ds, p.Node.Decl, "version", "at: %v", err)
	case p.SpecVersion() != "" && p.At != p.SpecVersion():
		errAt(&ds, p.Node.Decl, "version", "implements %s at %s, but the spec package is version %s: at copies the spec version exactly", p.SpecName(), p.At, p.SpecVersion())
	}
	var current *Version
	if p.Version != "" {
		v, verr := Parse(p.Version, p.Scheme)
		switch {
		case verr != nil:
			errAt(&ds, p.VersionSite, "version", "the project version: %v", verr)
		case err == nil && v.Spec != spec:
			errAt(&ds, p.VersionSite, "version", "the project version %s starts with %s, but the project implements %s at %s: its first three parts are the implemented spec version", p.Version, v.SpecString(), p.SpecName(), p.At)
		default:
			current = &v
		}
	}
	seen := map[string]bool{}
	for _, r := range p.ReleaseList() {
		s, _ := ArgString(KindArgs(r.Node), 0, "version")
		if r.Err != nil {
			errAt(&ds, r.Node.Decl, "version", "release: %v", r.Err)
			continue
		}
		key := r.Version.String()
		if seen[key] {
			errAt(&ds, r.Node.Decl, "version", "release %s is recorded twice", s)
		}
		seen[key] = true
		if current != nil && Compare(r.Version, *current) > 0 {
			errAt(&ds, r.Node.Decl, "version", "release %s is newer than the project version %s", s, p.Version)
		}
	}
	return ds
}
