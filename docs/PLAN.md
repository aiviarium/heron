# Rebuild plan

This plan covers the build of units 1 (Language and runtime) and 2
(Development tools). Units 3 (DevOps and testing) and 4 (Agents) are
deferred: nothing here implements them, and nothing in units 1 and 2 may
know they exist.

The spec is a detached system. It's authored in
[aiviarium/heron-specs](https://github.com/aiviarium/heron-specs) as the
sealed package `heron/`, versioned on its own, and never names code. This
repository imports it, `git+https://github.com/aiviarium/heron-specs//heron@v0`
pinned in `heron.lock`, and holds only the implementation: the code and the
project tree (`project/`: architecture components, the proofs that link each
spec requirement to its tests, status, the delivery plan and releases). It
can't amend the spec; a change to what the system does is a change to
heron-specs first. This file is a readable overview of how the build was
carried out and links into the spec and the project tree rather than
repeating them; where they disagree, the spec wins.

The first build was rebuilt from a design sketch whose code blocks were its
first fixtures. That sketch and the history before this repository's first
commit are kept in the specs-v0 archive; the fixtures now come from the spec
package's `example` nodes.

## Layout

```text
cmd/heron/                 wires each unit's registrations; no logic
internal/core/             unit 1
  registry/                what every unit registers: commands, generation
                           targets, lints, record kinds, prose providers,
                           funcs, transports, renderers
  syntax/                  lexer and lossless, error-recovering parser (CST)
  ast/                     typed views over the CST, generated from grammar/
  ast/internal/astgen/     the generator (go generate)
  project/                 std/project's Go side: project mode, the
                           implementation version schemes, conformance;
                           release/ is `heron release` and the
                           `conformance` target of `heron generate`
  format/                  heron fmt, from the CST
  diag/                    the diagnostics every stage reports
  resolve/                 entry points, include/import, scopes, amends, the
                           node tree; package sources (embedded std, files)
  types/                   schemas (std/schema), kinds, flags, annotations,
                           params/args, typed values
  check/                   parse, resolve and type-check with per-node memo;
                           the fixture check-clean test
  lint/                    core lints; the prose view and prose providers
  eval/                    interpreter for `do` code; no I/O (host interface)
  records/                 audit records, release output, caches as data files
  setup/                   std/repo's Go side: toolchain, heron.lock, sources
  gen/                     generation framework and `heron generate`
  cli/                     unit 1's commands: check, fmt, lint, run, generate
  fixtures/                the fixtures: the code blocks of the spec
                           package's example nodes, by node path
internal/tools/            unit 2
  lsp/ prose/ docs/ server/
std/                       standard packages as Heron source, embedded (std.go)
grammar/                   heron.ungram (node kinds) and, later, tree-sitter
web/                       pnpm workspace: unit 2's components/ shell/
                           editor/ docs/
index.heron                the repository's tree: imports the spec package,
                           includes the project tree, and its setup task
heron.lock                 pins the spec package's tag and commit
project/                   the project tree: components, proofs, status,
                           delivery, releases
fixtures/                  modes.txt, pending.txt and contexts.txt, keyed by
                           example node path, and the contexts examples check in
scripts/spec-setup.sh      heron run setup, or links $HERON_SPECS_DIR in its place
scripts/spec-docs.sh       builds the spec package as its docs site and serves it (--live: heron serve)
scripts/ci.sh              local CI: the spec package, gofmt, vet,
                           go-check-sumtype, tests, fixture checks,
                           conformance, the e2e-*.sh scripts
examples/petstore/         the Example tab's repository, as far as unit 1 goes
```

`syntax` and `eval` import nothing that performs I/O (no `os`, `net`,
`os/exec`, `io/fs` file access); a test enforces this from the import graph.

## Unit 1 phases

The unit tab's build order, each phase with what "done" means for it. A phase
starts when the one before it is done.

1. **Parser and fmt.** `grammar/heron.ungram` is the one description of node
   kinds; `syntax` produces a lossless CST with every mode switch the tab
   lists (`|` lines, `{{ }}` prose, `do` code, embedded and interpolated
   strings, tags, links, inserts); `ast` views are generated from the
   grammar; `format` and `heron fmt`.
   Done: every Heron fixture parses losslessly and without errors (except
   fixtures marked as showing an error or as a sketch), every prefix of every
   fixture re-parses losslessly, a fuzz test finds no panics or lossy parses,
   `heron fmt` is idempotent and every Heron fixture formats to itself.
2. **Resolve, types and core lints; `heron check` and `heron lint`.** Entry
   points, include guards and rounds, scoped imports, `<export>`, enter vs
   declare vs scope, amends with conflicts, locals; schemas from
   `std/schema`; the standard packages as embedded Heron source.
   Done (fixtures criterion 1): every Heron fixture of the unit-1 tabs checks
   clean against the standard packages, except those marked as showing an
   error; the core lints the DevOps tab lists exist (tag kind not in scope,
   flag/annotation not valid where it stands, unused `let`, setting an unknown
   field, op example not matching its params).
3. **Interpreter, tasks, `heron run`, then `setup`.** `eval` with a host
   interface for processes (`os/exec`, argument lists, never a shell) and
   files; `@needs`, `@undo`, prompting for missing params, run records.
   Done: criteria 2 (setup on a fresh sample repository, idempotent, writes
   `heron.lock` and the managed ignore block) and 3 (tasks).
4. **Prose view and generation framework.**
   Done: criterion 5 (a sample prose provider flags only prose words, fixes
   land on the exact range).

The spec package must check clean (and, once unit 2 has docs, render) at
every stage; heron-specs' own CI builds this repository's binary for that.

## Registered parts and how each is proven

Every seam is registered through `internal/core/registry`; every unit uses
the same call. Each gets a sample in tests before any later part relies on
it.

| Part | Registered by | Proven with |
| --- | --- | --- |
| Commands | name → handler in `registry.Commands` | a test-only command registered from a test package, run through the real CLI dispatcher |
| Generation targets | target name → adapters | a sample adapter writing one file under a temp dir via `heron generate sample` |
| Lints | name → check over the resolved tree | a Go-registered lint through `heron lint` |
| Prose lint providers | name → provider over projected regions | sample provider flagging a word; fixes mapped back to exact ranges |
| Transports | declaration path → implementation | a Go-registered transport carrying an op call |
| Funcs | declaration path → Go implementation | a Go-registered func called from a task |
| Record kinds | kind → schema, one store | a sample record kind written and read back |
| Renderers per kind (for unit 2) | kind → renderer | registered in unit 2's docs, proven there |

Unit 2's needs from unit 1, so the seams fit: positions and an incremental
reparse in `syntax` (lsp), node paths and go-to-definition data from
`resolve`, completion candidates from `types` (kinds, flags, annotations in
scope per mode), the prose view with its source map (Vale), and a `docs`
generation target plus renderer registration.

## Environment

- Go 1.24, Node 22, pnpm 10, 4 CPUs. `go-check-sumtype` is installed in
  `~/go/bin`.
- Vale v3.28.1 installs through the Go proxy, `go install
  github.com/vale-cli/vale/v3/cmd/vale@v3.28.1` (GitHub release downloads
  are blocked here); `scripts/ci.sh` installs it when it's missing, and
  `heron run setup` pins it (`vale_setup_pin`).
- tree-sitter-cli 0.25.3 comes from npm in `grammar/tree-sitter-heron`;
  `tree-sitter parse` compiles the grammar with the system C compiler.
- No GitHub CI. `scripts/ci.sh` is the whole check and is run locally.
- The spec package is installed by `scripts/spec-setup.sh` (`heron run
  setup`, from `heron.lock`) under `.heron/packages/`. For local work and CI
  against a sibling checkout, `HERON_SPECS_DIR=../heron-specs/heron
  scripts/spec-setup.sh` links that checkout in its place, and the Go tests
  and the scripts read the package from `$HERON_SPECS_DIR` when
  it's set. `go run ./internal/core/fixtures/extract -out DIR` writes the
  fixtures to a folder, as the tree-sitter test does.

## Status

`docs/STATUS.md` audits unit 1 against its Done-means items, with the
script or test that proves each and the gaps that remain.

- Phase 1 (parser and fmt) is done: 54 of 54 Heron fixtures parse
  losslessly, 53 of them without errors (the sketch `devops/01` elides a
  body and is lossless with recovery), all 54 format to themselves, every
  prefix of every fixture re-parses losslessly, formatting is idempotent
  (also on scrambled layout), and the fuzz test runs in `scripts/ci.sh`.
- Phase 2 (meaning) is under way:
  - `resolve`: canonical file identity, guarded includes (folders, globs,
    exclusions, cycles), scoped imports with `<export>` mounting, packages
    by name and major version (std embedded, package directories, local
    files and folders), declare, enter and scope, amends by path with maps
    merging, conflicts, overloads, locals visible below their line, prose
    file headings as sections.
  - `std/`: schema, types, core (the prelude), doc, spec, http, proto, ws,
    json, process, files and apis, each checking and linting clean.
  - `types`: kinds and types of every node, kind and type arguments,
    fields of kinds and types (with `args_from`, `fields_from` and
    bindings), flags, annotations, typed strings (`Duration`, `Money`,
    `Timestamp`), constructors, enums by bare variant or string, and name
    resolution in expressions, links, inserts, tags and `do` blocks.
  - `lint`: unused let, flag and annotation placement, tag kind, op
    example params and repeated imports, registered through the registry,
    with `@lint(allow = […])`; a Go-registered sample lint runs through
    `heron lint`.
  - `heron check` (with `--json` and `--watch`) and `heron lint` (with
    `--json`); results are memoized per file and per node, and a test
    counts recomputations.
  - Fixtures: 26 of the spec package's 29 Heron examples check clean; the
    other 3 are in `fixtures/pending.txt`, each with its reason, and wait
    for packages of units 3 and 4 (`std/ci`, `std/env` and prompts), as the
    project's roadmap (`project/delivery/roadmap.heron`) plans. The
    repository's tree, the spec package included, checks and lints clean in
    `scripts/ci.sh`.
  - Bodies are checked against `accept` lists: prose, code, child kinds
    and `max`.
  - Overloads are selected by arguments (the first `<overload>` whose
    parameters accept them), members of generic types are checked through
    their arguments (`items: List(T)` of `Page(Refund)`), and `Patch(T)`
    takes a map of `T`'s members, each optional.
  - `check.CheckFixtures` is the exported fixture runner; lint's test
    requires the clean fixtures to lint clean (`fixtures/lint-allowed.txt`
    would list intended findings; it doesn't exist yet).
- Phase 3 (eval) is under way: `internal/core/eval` interprets `do` code
  of funcs, macros, tasks and hooks; see "The eval API" below.
  - `heron run <task.path> [--param value…]` parses params by type (Int,
    Float, Bool, String, enums by variant, lists repeated or
    comma-separated, other types as JSON), asks for missing required ones
    on a terminal and exits 2 without one, runs `@needs` first (each once,
    in dependency order, sequentially; a cycle is an error), asks for
    `<confirm>` (or `--yes`), prints the result as data or `--json`, and
    with `--undo-on-failure` runs the `@undo` task of every finished task,
    newest first. Exit 1 is a failed task, 2 a usage error.
  - `records` stores records of registered kinds as JSON data files
    (`project/architecture/runtime/records.heron`); `heron run` records every run
    that starts under `.heron/audit/run/`. A sample record kind is written,
    listed, read back and pruned in `records`' tests.
  - `heron run setup` (`internal/core/setup`, `std/repo`): checks the
    toolchain, or installs it with `--install_toolchain`; pins the imported
    packages and the projects' git sources in `heron.lock`, checks the
    packages against their pins (`--update` moves them) and checks the
    sources out; runs `pnpm install`; writes the managed ignore block
    through `setup.IgnoreWriter`, which `gen` replaces at merge.
  - `examples/petstore` is the Example tab's repository as far as unit 1
    reaches (no stages, pipeline, scenarios, resources, agents or
    changelog); it checks, lints and formats clean in `scripts/ci.sh`.
  - `scripts/e2e-setup.sh` runs setup twice on a fresh git copy of the
    sample (with a local git repository standing in for the pinned source)
    and checks that the second run changes nothing; `scripts/e2e-tasks.sh`
    covers typed params, a prompt on a pseudo-terminal, exit 2 without
    one, `@needs` order, `@undo` and the run records. Both run in
    `scripts/ci.sh`.
  - Next: stages starting undo after a failed gate.

### The eval API

`eval` does no I/O. `eval.New(tree, checker, registry, caps)` returns a
`*Machine`; `Machine.Dir` is where relative paths start and `OnEvent`
receives the run record's events (`call`, `step`, `log`, `set`,
`process`).

- `Call(ctx, node, eval.Args{Positional, Named})` calls a func or task;
  `RunCode(ctx, node, vars)` runs a node's `do` blocks (hooks, actions);
  `Expand(ctx, macro, args)` returns a macro's source; `Close()` removes
  the run's `temp(…)` files.
- Values (`eval.Value = any`): nil, bool, int64, float64, string,
  `[]Value`, `map[string]Value`, `*Record{Type, Fields}`, `Text{Lang,
  Text}` (embeds), `Ref{Node}`, `TypeValue`. Enum variants are strings.
  `eval.Plain`/`FromPlain` convert to and from the plain data registered
  funcs and transports take.
- Errors: `*eval.Throw` is a Heron error value that `attempt(…)` catches;
  `*eval.Error` is a fault in code, with file, line and column.
- `eval.Caps` holds `Processes` (`Run(ctx, Command{Path, Args, Env, Dir,
  Stdin})`, an argument list, never a shell line), `Files`, `Env`, `Clock`
  and `Log`; `eval/host.Caps(log)` gives the real ones, through os/exec.
- **Registered funcs:** a func declared with no body is looked up by its qualified path in `registry.Func{Name,
  Call}`. A `registry.FuncImpl` is `func(ctx, *registry.FuncCall{Func,
  Args map[string]any, Dir, Step}) (any, error)`: arguments arrive by
  parameter name as plain data, defaults applied, and returning a
  `*registry.Thrown{Type, Fields}` throws a Heron error.
- Go backs std's `log`, `emit`, `run`, `read`, `write`, `temp`,
  `json.decode` and `json.encode`; `attempt` is a special form and
  `clock.now` reads `Caps.Clock`.
- For Go funcs that read the tree they run in, such as `std/repo`'s:
  `eval.MachineOf(ctx)` gives the calling machine inside a
  `registry.FuncImpl`, `Machine.Value(ctx, node)` a node's value,
  `Machine.KindArg(ctx, node, name)` a named kind argument such as a
  project's `path`, and `Machine.Report(event)` adds to the run record.
- **Ops** go through the transport their bindings name
  (`transport_binding`): the transport declared in the package of an
  annotation on the op or its api, as `registry.Transport.Send(ctx, op,
  request)`. No binding, or several transports, is an error, and so is an
  op call while the tree is evaluated (a computed value, a macro's
  expansion).

### The prose view

`lint/prose.Project(tree, checker)` returns `[]*registry.ProseRegion`, one
per paragraph, heading, list item, quote or code block of the tree's own
files (`|` lines, `{{ }}` bodies, `=|` values, `///` docs, `Prose`-typed
strings and prose files), each with `Node` (path), `Kind`, `Source`,
`Block`, `Group`, `Text`, `Masks` (code, insert, link, tag) and `Map`, the
source map. `prose.SourceRange` maps a text range to bytes and says
whether it's exact; `prose.RenderHTML` renders regions as one HTML
document (masks as `<code>`) and `HTML.Locate` maps its offsets back, for
Vale. A `registry.ProseProvider{Name, Check}` sees only regions whose hash
isn't cached and returns `ProseFinding`s against the text; `lint.Run`
maps them to diagnostics coded `Name.Rule`, suppressed by `@lint(allow)`
with either name, and keeps fixes whose range maps exactly as
`diag.Fix`. `heron lint --fix` applies first fixes. The sample provider is
`lint/prose/words`, proven by `scripts/e2e-prose.sh` with
`lint/prose/words/heronwords`.

Checker fixes from the spec port: std/spec accepts each condition kind
once per rule, transition or quality; `instead` outside a negative
strength is an error; and `example` and `open` stay out of
sections. The spec's examples are the fixtures themselves, so there's no
second check that links them.

### Generation

`heron generate [targets…]` (`internal/core/gen`) runs the adapters of
each target and, without targets, all of them plus the managed ignore
block; the built-in `ts` target writes `.heron/types.ts`.

## Unit 2: the language server library (`internal/tools/lsp`)

`internal/tools/lsp` is a library; `heron lsp` serves it over stdio and
`heron serve` serves one session per `/lsp` websocket.

```go
// Conn carries whole JSON-RPC messages, one per call.
type Conn interface {
	ReadMessage() ([]byte, error) // io.EOF ends the session
	WriteMessage([]byte) error    // called from one goroutine at a time
}
func StreamConn(rw io.ReadWriter) Conn // Content-Length framing, as on stdio

type Options struct {
	Registry *registry.Registry // lints and prose providers; required
	Dir      string             // root used when the client names no workspace
	Debounce time.Duration      // wait after an edit before rechecking; 0 means 150ms
	Log      io.Writer          // optional trace of errors
}
func NewSession(c Conn, o Options) *Session
func (s *Session) Run(ctx context.Context) error // until `exit`, EOF or ctx is done
func Serve(ctx context.Context, rw io.ReadWriter, o Options) error // StreamConn + Run
```

A websocket handler wraps each text frame as one message (no
Content-Length headers, as `vscode-ws-jsonrpc` and `monaco-languageclient`
send them) and calls `NewSession(conn, opts).Run(ctx)`; sessions share the
registry and nothing else.

- Files: one identity per file (`resolve.Canonical`), whatever URI
  spelling reaches it; an open document is the loader's overlay for that
  identity. Diagnostics are published to the URI the client opened it
  with, or the canonical `file://` URI.
- Embedded standard files are `heron:/std/<path>` URIs (definition into
  std); the custom request `heron/source {uri}` → `{text}` returns their
  text, so Monaco can open them read-only.
- Capabilities: diagnostics (each `source` is "heron check", "heron lint"
  or the prose provider, such as "Vale"; `code` is the stable code),
  completion, hover, definition, references, rename, formatting, semantic
  tokens (full), code actions (lint fixes, Vale fixes, vocabulary accept and
  reject), document symbols, folding ranges.

### Unit 2 language tooling: status

- `heron lsp` (`internal/tools/lsp`): diagnostics from `heron check`, the
  lints and the prose providers, each with its `source`; completion,
  hover, definition (into std as `heron:/std/…`, and from import and
  include strings), references, rename by path, formatting, semantic
  tokens, symbols, folding and code actions. Tests open every file of
  examples/petstore and the spec package through five URI spellings in three
  orders with zero diagnostics, through edits, closes and reopens; an error
  appears and clears, and a break in an import reaches its importer, open
  or on disk. `scripts/e2e-lsp.sh` drives the binary over stdio.
- Unit 1 seams added: `check.Result.Complete`/`Ensure`,
  `resolve.Tree.Targets`, `types.Checker.Fields`, `lint.Input.As`,
  `registry.ProseProvider.Prepare`, `registry.SetupStep` with heron.lock's
  `tools` section.
- `grammar/tree-sitter-heron`: grammar, external scanner, corpus,
  highlights, folds and textobjects queries; `test-fixtures.sh` parses
  every heron fixture, the fixtures' context and the repository's trees.
- `internal/tools/prose`: Vale over the HTML projection (`lint { vale {
  config, min_level, skip } }` from `std/lint`, or the nearest .vale.ini),
  fixes, vocabulary commands, caching by region with a configuration key,
  the setup step; the spec-aware lints of `std/lint` as the `Spec`
  provider. The sample has a .vale.ini with a local style and vocabulary.
## Unit 2: design tokens and the docs renderer

### Design tokens (`web/tokens`, `@heron/tokens`)

`web/tokens/tokens.json` is the one declaration of colours, sizes, radii,
spacing, type and motion; `go generate ./web/tokens` writes `tokens.css`
and `src/index.ts`, and `TestGeneratedFilesAreCurrent` (run in
`scripts/ci.sh`) keeps them current. Go embeds the CSS through
`tokens.CSS()` (package `github.com/aiviarium/heron/web/tokens`); TS
imports `@heron/tokens` (values, `cssVar`, `valueVar`) and
`@heron/tokens/tokens.css`.

- **Naming:** a colour is `--h-<name>`, any other token
  `--h-<group>-<name>`: `--h-size-control`, `--h-radius-card`,
  `--h-space-4`, `--h-font-size-ui`, `--h-motion-base`.
- **Themes:** dark on `:root` (dark first); light on `[data-theme="light"]`
  and, with no explicit `data-theme`, under `prefers-color-scheme: light`.
  Set `data-theme` on `<html>` to force one.
- **Surfaces:** `bg-page`, `bg-raised` (bars, cards), `bg-soft` (table
  heads, code titles, fields), `bg-highlight` (the selected item), plus
  `bg-topbar` (translucent, with backdrop blur), `bg-button` and
  `bg-button-hover`. All untinted.
- **Borders:** `border-faint`, `border`, `border-strong`, always 1 px.
  Only overlays use `shadow-overlay`.
- **Text:** `text`, `text-secondary`, `text-faint` (all ≥4.5:1 on every
  surface in both themes, checked by `TestContrast`) and `text-decor`
  (decoration only, never read). `text-on-primary` is the label of the
  one primary button, whose fill is `text`.
- **Project colour:** `accent` for links, references, 1 px borders, small
  marks and the focus ring (`accent-line` for quieter lines); never a
  fill.
- **Status and kind:** `status-ok`, `status-warn`, `status-error`,
  `status-info`, `type`; `method-get|post|put|patch|delete` alias them.
  Text or 1 px outline only. ≥4.5:1 on page, raised and soft.
- **Syntax:** `syntax-keyword|string|number|type|annotation|comment|punct|name|error`,
  aliases of the colours above.
- **Scales:** `size-control-sm|control|control-lg` (24/28/32),
  `size-row` 28, `size-topbar` 44, `size-sidebar`, `size-aside`,
  `size-measure` 72ch; `radius-mark|control|card|overlay` (4/6/8/10);
  `space-1…16` on the 4 px grid; `font-sans`, `font-mono`,
  `font-size-ui|prose|code|label` (13/15/12.5/11), `font-size-h1|h2|h3`,
  weights and tracking; `motion-fast|base|slow` (120/160/200 ms) and
  `motion-ease`.

### The docs renderer (`internal/tools/docs`), its library API

The generated site, the spec browser and the editor's live preview all
render through one package. Build a site from a checked tree, then ask it
for pages, nodes, files or fragments:

```go
import "github.com/aiviarium/heron/internal/tools/docs"

s := docs.New(res.Tree, res.Checker, docs.Options{
	Registry: r,            // renderers registered by packages and later units
	Href:     func(file string) string { return "/docs/" + file }, // optional link scheme
	Assets:   "/docs/_assets/", // where docs.css, docs.js and data.js are served
})
f, _ := s.RenderNode(node)        // *docs.Fragment for one node: its page's content if it has a page
f, _ := s.RenderFile(fileID)      // every top-level node of one file
f, _ := s.PageFragment(page)      // a page's content without the layout
html, _ := s.RenderPage(page)     // a whole HTML document, layout included
files, _ := s.Files()             // the static site: pages, _assets/data.js and the assets
s.PageOf(node), s.OwnPage(node), s.PageByFile("store.place_order.html"), s.URL(node)
docs.Assets()                     // []docs.Asset{Path, Content, Type}: docs.css, docs.js
s.DataJS()                        // the search index, hover cards and island strings
```

- `Fragment{HTML, Aside, Outline, Islands}`: `HTML` is the main column,
  `Aside` the right column (an op's examples, Try it), `Outline` the
  headings, `Islands` the islands it uses. To make a fragment interactive,
  load `docs.css` once, set `window.__HERON__` from `DataJS()` (or load
  `data.js`), then load `docs.js`; it enhances every `[data-island]` in the
  document on DOMContentLoaded. In a live preview that swaps HTML, reload
  the script or call it again after inserting a fragment (it is an IIFE).
- **Renderers per kind** are the seam `registry.SetRenderer(registry.Renderer{
  Kind: "spec.rule", Name: …, Render: func(ctx any) error })`, added to unit
  1's registry. `ctx` is a `*docs.Ctx` positioned on the node: `W`/`Wf`
  write HTML, `Text` writes spec text, `Label` a catalogue string,
  `Prose(n)`, `Children()`, `Render(n)`, `Link(n, text)`, `Heading(…)`,
  `Island(name)`, `Aside(html)`. `docs.Register(r)` registers the built-in
  renderers (`docs.BuiltinKinds()`) and the `docs` generation target; a
  kind with no renderer falls back to its title, prose, fields and
  children. A renderer registered for a markup tag's kind renders the tag
  too, with `ctx.Tag` (`Kind`, `Body`, `Attrs`) set and `ctx.Node` nil.
- Pages: one per top-level section and its subsections (deeper sections
  render inline), per documented scope, per api, op and type, plus the
  glossary (`_glossary.html`) and diagnostics (`_diagnostics.html`); a
  project that implements a spec adds `_conformance.html`, `_releases.html`
  and, unless `implements` names the spec's site with `docs`, the spec
  read-only on `_spec.html`. Files
  are flat, named by node path (`orders.ordering.html`), so every link is
  relative and the site works from `file://`.
- `heron generate docs` writes `.heron/docs/` under the working directory
  (`--entry` picks the tree); released builds live under
  `.heron/docs/v/<version>/` and `versions.json` lists them for the switch.

### Styling and extending the docs (`std/docs`)

A tree imports `std/docs@1` and writes the Styling tab's block; `s.Config`
holds what it set (the spec package's `language/std/docs.heron` and `tools/docs.heron`):

```text
docs {
  theme = "npm:@acme/heron-theme@2"     // or a folder, "./theme"
  layout("landing") { sections = [guide] }   // docs | api | changelog | landing
  slot("header") { status(level = 1) {{ Beta }} }  // header | sidebar | before | after
  slot("after", of = spec.rule) { | … }     // around every node of a kind
}
```

- **Themes:** a package with `heron-theme.json` (`tokens`, a token file in
  web/tokens' format; `fonts` `[{family, src, weight, style}]`; `logo`;
  `css`). `docs.LoadTheme` composes `_assets/theme/theme.css` in
  `@layer theme`; the built-in `docs.css` is `@layer heron`. `s.Assets()`
  is the built-in assets plus the theme's files, for a server to serve;
  `s.ThemeErr` says why a theme didn't load. Sample:
  `web/samples/docs-theme`.
- **Layouts** set `Page.Layout` on a section's page: `api` moves it into the
  reference, `changelog` renders child sections as entries, `landing`
  drops the sidebar and shows a tile per child page.
- **Slots:** page slots render in `topbar`, `sidebar` and around the
  content; kind slots wrap each node in `Ctx.render`, and a renderer places
  other names with `ctx.Slot(name)`.
- **Components** of a package without a renderer show as the generic
  view. Sample: `web/samples/docs-site`, generated and driven by
  `scripts/e2e-docs.sh`.

### The string catalogue (`web/strings`)

Every string the tools show that isn't from a spec is in
`web/strings/strings.json`, keyed `<tool>.<group>.<name>` (`docs.*` for the
renderer, `docs.island.*` for the islands, `app.*` for the app). Go reads it
through `uistrings.Get/Format` (package
`github.com/aiviarium/heron/web/strings`); TS imports the JSON. A
missing key panics, and the docs test fails on visible text that's in
neither the spec nor the catalogue.

## Unit 2: the server, the shell and the editor

- **`heron serve`** (`internal/tools/server`): serves the embedded
  frontend (unknown paths get `index.html`), the live spec browser under
  `/docs/` (the docs renderer on the tree on disk), one lsp session per
  `/api/v1/lsp` socket (`server.LanguageServer`, whole messages per frame)
  and the API; `cmd/heron` holds the shell `server.Register` returns.
- **The API** is the spec package's `tools/server_api/` (`spec_server`, its
  types and its errors), read from the spec package. `apigen` writes
  `api_gen.go` (types, `Service`, `Routes`) and `web/shell/src/api.gen.ts`.
  ci.sh checks both current.
- **The frontend** (pnpm workspace): `@heron/components` (UI SDK composed:
  palette, file tree, buttons), `@heron/editor` (Monaco set up for Heron,
  its own reconnecting LSP client, one model per file, the preview),
  `@heron/shell` (the editor screen, the slots for panels,
  gutter providers, commands and renderers per kind, the ⌘K palette and
  shortcuts). The app's strings are `app.*` in `web/strings`.
- **Acceptance tests**: `scripts/e2e-app.sh` runs `web/shell/e2e/*.cjs`
  with Playwright against `heron serve` on fresh copies of the sample:
  first run, file switching, typing, indentation and completion, Done
  means #1 (`lsp`), the preview half of #2 (`preview`) and visible text.
  `node web/shell/e2e/screenshots.cjs` (with `HERON` set) writes
  `web/screenshots/app-*.png`. `scripts/play.sh` serves a temporary copy
  of the sample and prints its URL.
