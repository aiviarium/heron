# Status

Units 1 and 2 against their "Done means", with the remaining open questions
at the end.

The spec is the sealed package `heron/` of
[aiviarium/heron-specs](https://github.com/aiviarium/heron-specs), imported
here at 0.3.0 and pinned in `heron.lock`: what the code must do is stated
there, by domain (`language/`, `cli/`, `tools/`, `product/`). How this
repository realizes it is the project tree's (`project/`): the components,
the proofs that link each requirement to its tests, the status of the rest,
the delivery plan and the releases. `heron generate conformance` reports
every requirement of the spec as proven, planned, untested or missing, with
the requirements on people and agents as practices of their own, and
`scripts/ci.sh` fails while any is missing. This file is a readable summary
of where the build stands and links into both rather than repeating them;
where they disagree, the spec wins.

## Unit 1

Unit 1 ("Language and runtime") against the five items of its "Done means"
(the project tree's `delivery/unit_1/`). Every check named here runs in
`scripts/ci.sh`, which also passes from a fresh `git clone` of the
repository into an empty directory, with the spec package from `heron run
setup` or from the sibling checkout `HERON_SPECS_DIR` names, so no other
untracked or ignored file is needed.

| # | Item | Proven by | Status |
| --- | --- | --- | --- |
| 1 | Fixtures | `TestFixturesAreTheSpecExamples`, `TestFixturesParse`, `TestFixturePrefixes`, `FuzzParse`, `TestFixturesFormatToThemselves`, `TestFixturesCheckClean`, `TestCleanFixturesLintClean` | 26 of the spec's 29 Heron examples check clean; 3 pending |
| 2 | Setup | `scripts/e2e-setup.sh`, `internal/core/setup` tests | met for the sample, with gaps below |
| 3 | Tasks | `scripts/e2e-tasks.sh`, `internal/core/cli` and `records` tests | met |
| 4 | Extensions | `scripts/e2e-interop.sh`, `internal/core/extension` tests, `web/runtime`'s vitest suite | met |
| 5 | Prose view | `scripts/e2e-prose.sh`, `internal/core/lint` prose tests | met |

### 1. Fixtures

The fixtures are the spec package's examples: every fenced code block of an
`example` node, keyed by the node's path (`internal/core/fixtures`). Nothing
is copied into this repository, so changing an example in heron-specs
changes what the implementation is tested against. Of the 32 blocks, 29 are
Heron (one of them a prose file), one is the surface grammar in EBNF and two
are TS (`fixtures/modes.txt`). Every Heron example parses losslessly, every
prefix re-parses losslessly, all format to themselves, and formatting is
idempotent. 26 check clean, each in the context `fixtures/contexts.txt`
gives it (`fixtures/context/`). `fixtures/pending.txt` lists the other 3
with their reasons, and the test fails when a listed example starts
checking clean:

- `language.data.configuration.data_configuration` and
  `language.data.configuration.data_layering` use `std/ci` and `std/env`
  of unit 3 (DevOps);
- `language.markup.long_form.markup_prompt` is a prompt of unit 4 (Agents).

The project's roadmap (`project/delivery/roadmap.heron`) plans them: they
check clean once those units declare what they use, and unit 1 is done
without them until then.

Gaps: the spec's TS examples (Interop) aren't type-checked as fixtures,
only the sample package's TS is.

### 2. Setup

`heron run setup` on a fresh git copy of `examples/petstore` checks the
toolchain, pins the imported standard packages and the projects' git
sources in `heron.lock`, checks the packages against their pins (`--update`
moves them), checks the sources out, runs `pnpm install` and writes the
managed ignore block; a second run changes nothing.

Gaps:
- The toolchain is checked, and installed only with `--install_toolchain`;
  the e2e script checks the failure that names the opt-in, not an install.
- Packages from a registry, npm or git can't be installed: there is no
  registry, and npm resolution through `package.json`'s `heron` field isn't
  built (`package_installs`, below).
- Setup doesn't run the generation targets (`setup_runs_generate`, below).
- The sample doesn't commit its lock, because the embedded standard
  packages' checksums move with every edit to `std/`.

### 3. Tasks

`heron run` parses typed params from the command line, asks for missing
ones on a terminal (exit 2 without one), runs `@needs` once each in
dependency order, asks for `<confirm>`, runs `@undo` newest first with
`--undo-on-failure`, and records every run under `.heron/audit/run/` with
its tasks, steps, processes, logs, calls, foreign calls (func, args, result
or error, duration) and resource lifecycle.

Gaps: stages that start undo after a failed gate belong to DevOps; runs
can't be replayed from their recorded foreign results yet.

### 4. Extensions

`web/samples/inventory` provides a foreign func, a transport, a lint, a
resource driver and a generator in TS. `heron generate ts` writes the types
`tsc` checks them against, and a drifting signature is a compile error.
Each is used like a built-in from `heron run`, `heron lint` (with `--fix`)
and `heron generate`:

- The transport carries the `inventory.shelf` api's ops in `heron run`,
  bound by the package's `@in_memory` annotation
  (`transport_binding`).
- `inventory.describe` calls an op and a Heron func back through
  `ctx.call` (`registry.Run.Call`, `eval.Machine.CallPath`).
- `inventory.slugify`, a `<pure>` func, computes a value while the tree is
  evaluated, cached by a hash of its inputs; an impure foreign call there is
  an error (`TestPureForeignFuncsAtEvaluationTime`).
- `ctx.resource` instances are set up on first use in a run and torn down
  newest first when it ends (`registry.Run`, `TestRunScopesResourcesAndCalls`,
  `TestRunResourceLifecycle`).

Gaps: declared network hosts aren't filtered one by one
(`per_host_network_limits`); the pure-func cache lives for one run, not in
`records`; no `@heron/testing`, `heron dev` or npm package resolution;
transports are only TS ones, as `std/http`, `std/proto` and `std/ws` have no
built-in transport, so an op bound only by `@http` can't be called.

### 5. Prose view

`lint/prose` projects `|` lines, `{{ }}` bodies, `=|` values, `///` docs,
`Prose` strings and prose files with code, inserts, links and tag
attributes masked and a source map back to bytes. The sample provider
`lint/prose/words` flags words through `heron lint` and never inside masks,
and `heron lint --fix` applies its fixes to the exact range
(`scripts/e2e-prose.sh`). Regions are cached by hash and suppressed with
`@lint(allow)`.

Vale is unit 2's provider, below.

### The binary

The Implementation tab says `cmd/heron` "installs each unit's
registrations and holds no logic of its own". `cmd/heron/main.go` makes a
registry, calls the units' `Register` functions, hands the arguments to
`cli.Main` and closes the registry; commands, dispatch and the sandbox
helper live in unit packages. Nothing needed moving.

#### Unit 1 alone

`main.go` installs unit 1's registrations; `unit2.go`, built unless the
`unit1` tag is set, appends unit 2's. `go build -tags unit1 ./cmd/heron`
gives the language and runtime alone, with `check`, `fmt`, `lint`, `run`
and `generate` and no `lsp`, `serve` or docs target. `scripts/ci.sh` builds
it and, with it, formats, checks and lints the repository's tree (the spec
package and the project tree) and `examples/petstore` and runs `e2e-setup.sh`, `e2e-tasks.sh` and
`e2e-interop.sh` (`HERON_TAGS=unit1`; setup then pins no Vale).
`e2e-prose.sh` builds its own unit-1 binary. CI also fails if a package
under `internal/core` or `std`, or its tests, imports `internal/tools`, so
unit 1's Go tests are unit 1's alone.

What "renders as the documentation site at every stage" means at the
unit-1 stage: the docs generator is unit 2's, so at stage 1 nothing renders.
The build reads the requirement as: the spec package checks and lints clean
with the unit-1 binary, so its `std/doc` sections, terms and links resolve
and the tree unit 2 renders is already valid; rendering itself (`heron
generate docs` on the package, every link and island, `scripts/e2e-docs.sh`)
is proven from stage 1–2 on.

Gap: the standard packages are embedded as one set, so the unit-1 binary
still carries unit 2's `std/lint` and `std/docs` sources. They check, and
the spec package can import `std/lint`, but without unit 2 no prose provider runs, so
`heron lint` at stage 1 is the core lints only.

### Recommendations on unit 1's open questions

These are recommendations only; behaviour is unchanged.

**`package_installs`: should setup run from `heron.lock` alone, before the
tree checks?** Yes, in two phases. Install first, from what needs no
checking: the lock, which is a data file, and, on the first run without
one, the `import` directives, which are syntactic and resolve without
types. That phase fetches and verifies every pinned package and extension
(registry, npm through the project's package manager, git), then the tree
is checked and the `setup` task runs as it does today, pinning anything new.
Without the split a tree that imports a missing package can never install
it, which the Core format tab rules out ("`heron.lock`, a data file written
by `heron run setup`, pins the exact version and checksum of every package
and extension, and is committed"). A lock entry that the imports no longer
name should be reported, not installed.

**`setup_runs_generate`: should setup run every generation target?** Yes.
The Interop tab has the TS types "generated on setup and after every
change", and the Implementation tab has `heron generate` write "setup
files and the managed ignore block", so after setup a fresh checkout should
be where `heron generate` leaves it. `repo.generate` should run every
registered target, as `heron generate` without targets does, after
`pnpm install` so TS adapters find their runtime, writing all sections of
the ignore block. A failing adapter should fail setup, and a second run
must still change nothing, which the adapters' write-only-on-change rule
gives.

**`record_location`: where do records go?** Unit 1 keeps the store and
the default `.heron/audit/`, and shouldn't declare a configuration kind of
its own: the only configuration the tabs show is the DevOps tab's
`audit { path, keep }`. The registry should take a provider of
`records.Config`, which DevOps registers to read `audit` from the tree, and
every reader opens the store through it, so `heron run`, CI and agents
agree on one location without unit 1 knowing the `audit` node exists.

## Unit 2

Unit 2 ("Development tools") against the three items of its "Done means"
(the project tree's `delivery/unit_2/`). The checks run in
`scripts/ci.sh` with Vale required (`HERON_REQUIRE_VALE=1`, Vale v3.28.1
installed from the Go proxy).

| # | Item | Proven by | Status |
| --- | --- | --- | --- |
| 1 | Language server | `scripts/e2e-lsp.sh` (a scripted client over stdio), `web/shell/e2e/lsp.cjs` (Monaco through `heron serve`), `internal/tools/lsp` tests | met |
| 2 | Preview and docs | `web/shell/e2e/preview.cjs`, `first-run.cjs`, `scripts/e2e-docs.sh`, `internal/tools/docs` tests | met, with gaps below |
| 3 | Prose | `scripts/e2e-lsp.sh`, `internal/tools/prose` tests, `heron lint .` in CI | met, with gaps below |

### 1. Language server

`heron lsp` gives diagnostics from the checker, the lints and the prose
providers, each with its source, plus completion, hover, go to definition
(into std and from import and include strings), references, rename by path,
formatting, semantic tokens, symbols, folding and code actions.
`e2e-lsp.sh` drives the binary over stdio on a copy of the sample; `lsp.cjs`
does the same five things in Monaco against `heron serve`'s `/api/v1/lsp`
socket. The Go tests open every file of `examples/petstore` and of the spec package
through five URI spellings in three orders with zero diagnostics.
The tree-sitter grammar parses every fixture (`test-fixtures.sh`).

Gaps: only Monaco is tested as an editor; the generated VS Code
extension, if one is wanted, isn't built.

### 2. Preview and docs

`preview.cjs` edits an op's path in the editor and sees the preview change
within a second (the test fails over 1000 ms), checks that the preview and
the docs page render the same text, and that a task run from the gutter
shows its latest record beside the node. `first-run.cjs` opens `/` in an
empty profile and follows the file tree, the outline, the palette, the
preview, the problems and a gutter run, failing on any console error,
failed request or page error; `switching.cjs`, `typing.cjs`, `indent.cjs`
and `visible-text.cjs` cover the rest of the editor screen.
`e2e-docs.sh` runs `heron generate docs` on the sample and on the spec package and
checks every link and island in Chromium.

Gaps:
- The only records in a 1–2 build are task runs, so "the latest records
  beside each node" is proven for tasks; test results arrive with unit 3
  through `Shell.Mount`.
- Release notes and the package graph wait on `docs_release_notes` and
  `docs_package_graph`.
- Monaco's own widget text ("Quick Fix…", the find widget) isn't in the
  catalogue (`third_party_widget_text`).

### 3. Prose

With the sample's `.vale.ini`, its local style and vocabulary, `e2e-lsp.sh`
sees Vale alerts on the right words of a Heron file, never inside code or
links, with Vale's fix as a quick fix and the accept and reject vocabulary
actions; the spec-aware lints of `std/lint` flag an unlinked term in the
sample. `heron run setup` pins Vale in `heron.lock` and syncs its packages
(`e2e-setup.sh`). The spec package imports `std/lint` and has its own
`.vale.ini`; heron-specs' CI and `heron lint .` here pass with both
providers and nothing silenced.

Gaps:
- Vale alerts are tested through the stdio client, not in Monaco.
- The catalogue is checked by a Go test for the tab's banned words and
  full stops, not yet by a Vale style for interface text.

### Unit 2's polish from this review

The editor's preview of `projects.heron` showed "Petstore ." because the
root project's path `"."` was printed after its name; a project now shows
its path from the tree's root and nothing at the root. The "required" badge
is coloured text with no outline. A declaration nested in a block, such as a
project's tasks, kept its indentation after the first line in the preview
and the docs; it's now dedented.

## Open questions

The questions still open about what the system does are the spec's, kept in
heron-specs as `open(…)` nodes on the nodes they're about. Questions about
how this repository builds it belong to the project tree. A question is
resolved by writing its answer into the spec as rules, or into the project
tree, and removing the `open`.
