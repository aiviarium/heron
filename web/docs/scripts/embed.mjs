// Copies the built theme and islands into the Go package that embeds them,
// so `heron generate docs` needs no Node.
import { copyFileSync, mkdirSync } from "node:fs"
import { dirname, join } from "node:path"
import { fileURLToPath } from "node:url"

const here = dirname(fileURLToPath(import.meta.url))
const out = join(here, "..", "..", "..", "internal", "tools", "docs", "assets")
mkdirSync(out, { recursive: true })
for (const f of ["docs.js", "docs.css"]) {
  copyFileSync(join(here, "..", "dist", f), join(out, f))
}
