# The first build {#first_build}

The first build is an experiment: an AI implementer builds the whole system once, so
every part of the design can be tried and verified before it's built for real. The
build order is the order of work, not a cut line: units 3 and 4 are
[deferred](#delivery.roadmap), not out of scope.

- **Breadth before polish.** Each part works end to end against the examples before any
  part is optimized, so gaps show up across the system early.
- **The spec is Heron.** The design was ported into Heron as [the spec package](#heron), and
  from then on that package is the source of truth. CI checks, lints and renders it with the
  binary, as the binary's invariants [spec compiles](#architecture.runtime.binary.spec_compiles) and
  [spec renders](#architecture.runtime.binary.spec_renders) say.
- **The spec grows with the units.** When a unit is built, its tab is ported into the
  spec package and the spec uses the unit's own kinds from then on.
- **Gaps are findings.** Where the spec can't say something cleanly, or only as plain
  prose, it's reported to the language's authors as a finding against the language.
- **Disagreeing tabs.** When two design tabs disagree, the more specific tab wins over the
  general one, and the newer text over the older.
