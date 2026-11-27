# Heron

[![CI](https://github.com/aiviarium/heron/actions/workflows/ci.yml/badge.svg)](https://github.com/aiviarium/heron/actions/workflows/ci.yml)
[![Pages](https://github.com/aiviarium/heron/actions/workflows/pages.yml/badge.svg)](https://github.com/aiviarium/heron/actions/workflows/pages.yml)

**Docs:** [aiviarium.github.io/heron](https://aiviarium.github.io/heron/), the spec it
implements in its three lenses (for users at the root,
[/dev/](https://aiviarium.github.io/heron/dev/) for spec writers and
[/explore/](https://aiviarium.github.io/heron/explore/) for the whole tree), its
agent export for agents at [/agents/](https://aiviarium.github.io/heron/agents/INDEX.md)
(with [/llms.txt](https://aiviarium.github.io/heron/llms.txt)), and the
project site (architecture, conformance, releases) at
[/project/](https://aiviarium.github.io/heron/project/).

The implementation of Heron: the `heron` binary, its standard packages and
its frontend. What Heron does is specified apart from this repository, in
[aiviarium/heron-specs](https://github.com/aiviarium/heron-specs).

## Build from source and try it

You need Git, Go 1.24.7 or newer, and Node.js 20 or newer, whose corepack
runs the pnpm version `web/package.json` pins (10.28.0); nothing else is
installed globally.

```sh
git clone https://github.com/aiviarium/heron.git
cd heron
scripts/build.sh            # checks the prerequisites, installs the web
                            # dependencies, builds the editor's frontend into
                            # the binary and writes bin/heron (or: make build)
bin/heron version           # heron 0.6.0+build1, the spec version, commit, build time
```

Put it on your `PATH`, or install it:

```sh
export PATH="$PWD/bin:$PATH"     # for this shell
make install                     # or: copies bin/heron to ~/.local/bin (PREFIX=… to change)
```

`go install ./cmd/heron` also works, without `scripts/build.sh`: it builds
the same commands, but `heron serve` then embeds the frontend only if
`scripts/build.sh` built it before, and `heron version` reports the commit
and build time Go recorded, or `unknown`.

Try it on an example:

```sh
cd examples/petstore
heron check .                    # the tree checks clean: no output, exit 0
heron lint .                     # the lints and the spec-aware prose checks
heron generate docs              # the documentation site, in .heron/docs/
heron generate agents            # the spec as Markdown for agents, in .heron/agents/INDEX.md
heron serve                      # the editor at http://127.0.0.1:7340/, the docs at /docs/,
                                 # the agent export at /agents/; Ctrl-C stops it
cd ../..
```

Or let one script do all of that on a scratch copy, so the example stays
as it is:

```sh
scripts/play.sh                  # petstore; or: scripts/play.sh parcels, make play EXAMPLE=parcels
```

Run the tests:

```sh
go test ./...                    # the Go tests (set up the spec first: scripts/spec-setup.sh)
scripts/ci.sh                    # the whole check, as CI runs it (or: make test)
```

The spec of Heron itself makes a bigger playground: `heron generate --entry
.heron/packages/github.com/aiviarium/heron-specs/heron docs agents`, after
`scripts/spec-setup.sh`, writes its site and its agent export. As advanced
use, `HERON_SPECS_DIR` points the build at a checkout of the spec you're
changing, as [Getting the spec](#getting-the-spec) says.

## Two repositories

| Repository | Holds | Versioned by |
| --- | --- | --- |
| `aiviarium/heron-specs` | The spec: the sealed package `heron/` (the language, the product, the command line, the tools' behaviour), authored in Heron. It never refers to code. | its package version and its `vX.Y.Z` tags |
| `aiviarium/heron` (this one) | The code, and the project tree around the spec: architecture components, the proofs that link each spec requirement to its tests, status, the delivery plan, releases, and these docs. | its project version, which starts with the spec version |

This repository imports the spec as a package and never writes spec
content: the package is sealed, so nothing here can amend, scope or set
into its nodes, and a change to what the system does is made in
heron-specs first. The fixtures the parser, formatter and checker are
tested against are the spec's `example` nodes, and the extension protocol
is generated from the spec's `language/interop/protocol_messages.heron`.

## Getting the spec

The root `index.heron` imports the package by its git address,

```heron
import = ["std/repo@1", "git+https://github.com/aiviarium/heron-specs//heron@v0"]
```

and `heron.lock` pins it at a commit. No version of the spec is released
yet, so the pin names the branch `main` and the exact commit and checksum;
setup installs that commit. Before anything checks, install it:

```sh
scripts/spec-setup.sh            # heron run setup: installs the pinned spec
                                 # under .heron/packages/, from git
scripts/spec-setup.sh --update   # once versions are tagged, moves the pin to the newest v0 tag
```

To work against an unreleased spec, point `HERON_SPECS_DIR` at a checkout
of the package, normally the sibling `../heron-specs/heron`:

```sh
export HERON_SPECS_DIR=../heron-specs/heron
scripts/spec-setup.sh            # links the checkout at .heron/packages/
```

With it set, the link stands in for the installed package for `heron
check` and the editor, and the Go tests, `protogen` and the scripts read
the package from `$HERON_SPECS_DIR` directly. Unset it and run
`scripts/spec-setup.sh` again to go back to the pinned version.

## Versions

The product version is the spec version: this build implements the Heron
spec 0.6.0, so Heron is 0.6.0. The implementation's own builds of one spec
version follow a `+`, as build metadata, so they never claim a different
spec: `0.6.0+build1`, `0.6.0+build2`, and so on, the counter starting again
when the spec version changes. Where a release needs an ordered
implementation patch, the project's `versioning` can use the 4-part scheme
`SM.Sm.Sp.IP` instead, as `0.6.0.1`, with build metadata still after `+`.
The project tree's `implements(heron, at = "0.6.0")` names the spec version
and `version` the project version; releases are tagged `v<version>`.

## Checking it

```sh
scripts/ci.sh                                   # the whole check, run before every push
HERON_SPECS_DIR=../heron-specs/heron scripts/ci.sh
scripts/ci.sh --list                            # its lanes; scripts/ci.sh docs e2e runs those alone
scripts/play.sh                                 # heron serve on a copy of examples/petstore
scripts/spec-docs.sh                            # the spec package as its docs site
scripts/pages.sh /tmp/site                      # the published site: spec at /, agents/, project at project/
```

`scripts/ci.sh` is the check, and it passes from a fresh clone. GitHub
Actions runs the same script on every push and pull request to `main`
(`.github/workflows/ci.yml`), with heron-specs checked out at the commit
`heron.lock` pins and linked in through `HERON_SPECS_DIR`; a manual run with
`spec = install` takes the lock-based `heron run setup` path instead. The
script sets up the spec package first, then runs gofmt, vet, the
generated-code checks, the Go, web and tree-sitter tests, the fixtures, the
checks and lints of the repository's tree and the examples, conformance
(every spec requirement proven, planned or untested; none missing) and the
end-to-end scripts. It runs them as lanes, all at once, each one's output in
a log shown if it fails, and ends with a table of where the time went;
`--serial` runs them one after another. On GitHub each lane is a job of its
own, and the `ci` job passes when every lane did. `docs/PLAN.md` describes how the build was carried out
and `docs/STATUS.md` where it stands.

## The published site

`.github/workflows/pages.yml` publishes, on every push to `main`, the spec
package's own site with every lens its docs block builds, the user lens at
[aiviarium.github.io/heron](https://aiviarium.github.io/heron/) and the others under
[`/dev/`](https://aiviarium.github.io/heron/dev/) and
[`/explore/`](https://aiviarium.github.io/heron/explore/), where the project tree's
`implements { docs = ... }` links spec nodes to; and the project site under
[`/project/`](https://aiviarium.github.io/heron/project/). `scripts/pages.sh <dir>`
builds the same site locally. The repository's **Settings > Pages > Source**
must be set to **GitHub Actions** for the deployment to run.
