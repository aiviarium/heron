# Done means {#done_means}

What done means for the unit, as the checklist the build is accepted by. Each item is a
`verify` on the component it exercises.

- [ ] **Fixtures:** every code block of unit 1's tabs parses, formats to itself and checks
  clean against the standard packages, unless it's marked as showing an error or listed in
  `fixtures/pending.txt` ([check](#architecture.runtime.check), `TestFixturesCheckClean`).
- [ ] **Setup:** `heron run setup` on a fresh checkout of a sample repository checks the
  toolchain, pins and installs what `heron.lock` lists, runs the generation targets, writes
  the managed ignore block, and changes nothing when it runs again
  ([setup](#architecture.runtime.setup), `scripts/e2e-setup.sh`).
- [ ] **Tasks:** `heron run` runs a task with params from the command line, asks for missing
  ones, honours `@needs` and `@undo`, and records the run ([eval](#architecture.runtime.eval),
  `scripts/e2e-tasks.sh`).
- [ ] **Extensions:** a sample TS package's foreign func, transport, lint and resource
  driver type-check against their Heron declarations and work like built-in parts
  ([extension](#architecture.runtime.extension), `scripts/e2e-interop.sh`).
- [ ] **Prose view:** a sample prose lint provider flags words in body, `|` and `///` text,
  never inside code, inserts or links, with fixes applied to the exact source range
  ([prose view](#architecture.runtime.prose_view), `scripts/e2e-prose.sh`).
