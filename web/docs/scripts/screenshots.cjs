// Screenshots of the generated docs, in dark and light, for review:
//   node web/docs/scripts/screenshots.cjs <petstore site> <spec site> <out dir> [<themed site>]
// The themed site (web/samples/docs-site) is shot in dark only. Uses the
// preinstalled Chromium through Playwright.
const { chromium } = require("playwright")
const path = require("node:path")

const [petstore, spec, out, themed] = process.argv.slice(2)
const shots = [
  { name: "op", site: petstore, page: "store.place_order.html" },
  { name: "type", site: petstore, page: "Pet.html" },
  { name: "lifecycle", site: petstore, page: "orders.html", selector: "#orders\\.pet_lifecycle" },
  { name: "section", site: spec, page: "apis.apis_and_ops.html" },
  { name: "search", site: petstore, page: "store.place_order.html", search: "order" },
]
if (themed) shots.push({ name: "themed", site: themed, page: "guide.setup.html", themes: ["dark"] })

;(async () => {
  const browser = await chromium.launch()
  for (const theme of ["dark", "light"]) {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2, colorScheme: theme })
    const page = await ctx.newPage()
    for (const s of shots) {
      if (s.themes && !s.themes.includes(theme)) continue
      await page.goto("file://" + path.resolve(s.site, s.page))
      await page.waitForFunction(() => document.documentElement.dataset.islands === "ready")
      if (s.search) {
        await page.keyboard.press("Control+k")
        await page.keyboard.type(s.search)
        await page.waitForTimeout(250)
      }
      const file = path.join(out, `docs-${s.name}-${theme}.png`)
      if (s.selector) {
        const el = page.locator(s.selector)
        await el.scrollIntoViewIfNeeded()
        await el.screenshot({ path: file })
      } else {
        await page.screenshot({ path: file })
      }
      console.log(file)
    }
    await ctx.close()
  }
  await browser.close()
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
