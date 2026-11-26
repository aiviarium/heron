# Roadmap {#roadmap}

Units 3 and 4, deferred: what they'll add and the
[extension points](#heron.language.interop.declaring.extension_point) they'll use.

Units 3 and 4 aren't implemented in the first build. Their tabs are exported and their
code blocks extracted as fixtures, and units 1 and 2 expose every extension point
they'll register through, so adding them later changes neither.

Ten Heron blocks of unit 1's tabs use what units 3 and 4 own: `std/ci` and `std/env` of
DevOps on the Data and Example tabs, `std/scenarios` of Testing and `std/agents` of
Agents on the Example tab, and a prompt on the Markup tab. `fixtures/pending.txt` lists
each of them with the package it waits for, and the check component's
[pending blocks](#architecture.runtime.check.pending_blocks) invariant keeps the list from going stale.

**Planned:** once units 3 and 4 declare `std/ci`, `std/env`, `std/scenarios`, `std/agents`
and prompts, each listed block checks clean and comes off the list, and unit 1's done
criterion counts it from then on. Until then unit 1 is done without them.
