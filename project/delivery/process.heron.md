# Process {#process}

How a change moves from spec to code: ready, building, done.

1. **Ready.** The behaviour is stated in [the spec](#heron), at the version this tree
   [implements](#heron_impl). An implementer builds what the spec states and decides no design:
   syntax, the surface of a standard package, a behaviour or the scope. When building
   needs a behaviour the spec doesn't state, that's a finding for the spec's authors,
   as [the spec's guide](#heron.guide.changing) says, and the change waits for a spec version that states it.
2. **Building.** Each Go or TS package is one `component` under
   [the architecture](#architecture), whose path is the package's folder and which names the
   system parts it realizes. A requirement the code doesn't meet yet is listed as
   `status<planned>` in [the status](#status).
3. **Done.** The change links the test that proves each requirement it builds, as a
   `proves(…)` in [the traceability](#traceability), and takes the requirement off the status list.
   A requirement that's built and still owed a test is `status<untested>`.

After every change the tree stays green: `heron check`, `heron lint` and
`heron fmt --check` pass on the project tree, `heron generate conformance` reports no
requirement missing that the change touched, and the project site renders with
`heron generate docs`. A new spec version changes `at` and `version` in
[the implements](#heron_impl), and the build counter starts again.
