package project

import (
	"strings"
	"testing"
)

func TestParseSchemeSpec(t *testing.T) {
	valid := map[string]string{
		"2.1.3":          "2.1.3",
		"0.0.0":          "0.0.0",
		"2.1.3+build4":   "2.1.3+build4",
		"2.1.3+rc2":      "2.1.3+rc2",
		"10.20.30+beta0": "10.20.30+beta0",
		"v2.1.3+build1":  "2.1.3+build1",
	}
	for in, want := range valid {
		v, err := Parse(in, SchemeSpec)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if v.String() != want {
			t.Errorf("%s: got %s, want %s", in, v, want)
		}
		if v.HasPatch {
			t.Errorf("%s: the default scheme has no implementation patch", in)
		}
	}
	invalid := map[string]string{
		"2.1.3.4":     "has 4 numbers",
		"2.1":         "has 2 numbers",
		"2.01.3":      "no version number",
		"2.x.3":       "no version number",
		"2.1.3+":      "metadata after +",
		"2.1.3+rc":    "metadata after +",
		"2.1.3+4":     "metadata after +",
		"2.1.3+RC2":   "metadata after +",
		"2.1.3+rc02":  "metadata after +",
		"2.1.3+rc2.1": "metadata after +",
		"2.1.3-rc2":   "no version number",
		"":            "has 1 numbers",
	}
	for in, want := range invalid {
		_, err := Parse(in, SchemeSpec)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", in, err, want)
		}
	}
}

func TestParseSchemePatch(t *testing.T) {
	valid := map[string]string{
		"2.1.3.0":         "2.1.3.0",
		"2.1.3.4":         "2.1.3.4",
		"2.1.3.4+rc2":     "2.1.3.4+rc2",
		"v2.1.3.4+build7": "2.1.3.4+build7",
	}
	for in, want := range valid {
		v, err := Parse(in, SchemePatch)
		if err != nil {
			t.Errorf("%s: %v", in, err)
			continue
		}
		if v.String() != want || !v.HasPatch {
			t.Errorf("%s: got %s (patch %v), want %s", in, v, v.HasPatch, want)
		}
	}
	invalid := map[string]string{
		"2.1.3":        "has 3 numbers",
		"2.1.3+rc1":    "has 3 numbers",
		"2.1.3.4.5":    "has 5 numbers",
		"2.1.3.04":     "no version number",
		"2.1.3.4+b":    "metadata after +",
		"2.1.3.4+rc-1": "metadata after +",
	}
	for in, want := range invalid {
		_, err := Parse(in, SchemePatch)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error containing %q", in, err, want)
		}
	}
}

func TestParseScheme(t *testing.T) {
	for in, want := range map[string]Scheme{"": SchemeSpec, "SM.Sm.Sp": SchemeSpec, "SM.Sm.Sp+<tag>IB": SchemeSpec, "SM.Sm.Sp.IP": SchemePatch, "SM.Sm.Sp.IP+<tag>IB": SchemePatch} {
		if got, err := ParseScheme(in); err != nil || got != want {
			t.Errorf("%q: got %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"semver", "%major%.%minor%.%patch%", "SM.Sm"} {
		if _, err := ParseScheme(in); err == nil {
			t.Errorf("%q: want an error", in)
		}
	}
}

func TestCompare(t *testing.T) {
	order := []string{"2.1.2+build9", "2.1.3", "2.1.3+build1", "2.1.3+rc2", "2.1.3+build3", "2.2.0+build1", "3.0.0"}
	for i := 1; i < len(order); i++ {
		a, _ := Parse(order[i-1], SchemeSpec)
		b, _ := Parse(order[i], SchemeSpec)
		if Compare(a, b) >= 0 || Compare(b, a) <= 0 {
			t.Errorf("%s should come before %s", a, b)
		}
	}
	a, _ := Parse("2.1.3.4+rc9", SchemePatch)
	b, _ := Parse("2.1.3.5", SchemePatch)
	if Compare(a, b) >= 0 {
		t.Error("the implementation patch orders before the build counter")
	}
}

func parseAll(t *testing.T, scheme Scheme, vs ...string) []Version {
	var out []Version
	for _, s := range vs {
		v, err := Parse(s, scheme)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

func TestNext(t *testing.T) {
	spec := [3]int{2, 1, 3}
	cases := []struct {
		scheme   Scheme
		releases []string
		tag      string
		want     string
	}{
		// The default scheme bumps the build counter, labelled build
		// unless a tag is given, and starts again on a spec change.
		{SchemeSpec, nil, "", "2.1.3+build1"},
		{SchemeSpec, []string{"2.1.3+build1"}, "", "2.1.3+build2"},
		{SchemeSpec, []string{"2.1.3+build1", "2.1.3+rc2"}, "", "2.1.3+build3"},
		{SchemeSpec, []string{"2.1.3+build1"}, "rc", "2.1.3+rc2"},
		{SchemeSpec, []string{"2.1.2+build7"}, "", "2.1.3+build1"},
		{SchemeSpec, []string{"2.1.3"}, "", "2.1.3+build1"},
		// The patch scheme bumps IP, 0 again on a spec change, with the
		// metadata only when asked.
		{SchemePatch, nil, "", "2.1.3.0"},
		{SchemePatch, []string{"2.1.3.0", "2.1.3.1"}, "", "2.1.3.2"},
		{SchemePatch, []string{"2.1.2.9"}, "", "2.1.3.0"},
		{SchemePatch, []string{"2.1.3.4+rc1"}, "rc", "2.1.3.5+rc2"},
		{SchemePatch, []string{"2.1.2.4+rc5"}, "rc", "2.1.3.0+rc1"},
	}
	for _, tc := range cases {
		got := Next(parseAll(t, tc.scheme, tc.releases...), spec, tc.scheme, tc.tag)
		if got.String() != tc.want {
			t.Errorf("%v after %v (tag %q): got %s, want %s", tc.scheme, tc.releases, tc.tag, got, tc.want)
		}
	}
}
