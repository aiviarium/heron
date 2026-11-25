# Heron

[![CI](https://github.com/aiviarium/heron/actions/workflows/ci.yml/badge.svg)](https://github.com/aiviarium/heron/actions/workflows/ci.yml)
[![Pages](https://github.com/aiviarium/heron/actions/workflows/pages.yml/badge.svg)](https://github.com/aiviarium/heron/actions/workflows/pages.yml)

**Docs:** [aiviarium.github.io/heron](https://aiviarium.github.io/heron/), the spec it
implements in its three lenses (for users at the root,
[/dev/](https://aiviarium.github.io/heron/dev/) for spec writers and
[/explore/](https://aiviarium.github.io/heron/explore/) for the whole tree), and the
project site (architecture, conformance, releases) at
[/project/](https://aiviarium.github.io/heron/project/).

The implementation of Heron: the `heron` binary, its standard packages and
its frontend. What Heron does is specified apart from this repository, in
[aiviarium/heron-specs](https://github.com/aiviarium/heron-specs).

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

and `heron.lock` pins the tag and the commit it resolved to. Before
anything checks, install it:

```sh
scripts/spec-setup.sh            # heron run setup: installs the pinned spec
                                 # under .heron/packages/, from git
scripts/spec-setup.sh --update   # moves the pin to the newest v0 tag
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
spec 0.2.0, so Heron is 0.2.0. The implementation's own builds of one spec
version follow a `+`, as build metadata, so they never claim a different
spec: `0.2.0+build1`, `0.2.0+build2`, and so on, the counter starting again
when the spec version changes. Where a release needs an ordered
implementation patch, the project's `versioning` can use the 4-part scheme
`SM.Sm.Sp.IP` instead, as `0.2.0.1`, with build metadata still after `+`.
The project tree's `implements(heron, at = "0.2.0")` names the spec version
and `version` the project version; releases are tagged `v<version>`.

## Checking it

```sh
scripts/ci.sh                                   # the whole check, run before every push
HERON_SPECS_DIR=../heron-specs/heron scripts/ci.sh
scripts/demo.sh                                 # heron serve on a copy of examples/petstore
scripts/spec-docs.sh                            # the spec package as its docs site
scripts/pages.sh /tmp/site                      # the published site: project at /, spec at spec/
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
end-to-end scripts. `docs/PLAN.md` describes how the build was carried out
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
