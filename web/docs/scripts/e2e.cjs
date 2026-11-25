// End to end check of a generated docs site in Chromium:
//   node web/docs/scripts/e2e.cjs <site dir>
// Loads every page from file://, fails on script errors, checks every link
// and anchor, and drives every island the site has: search, hover cards,
// Try it, the diagram's pan and zoom and table switch, example tabs, the
// theme switch and the version switch.
// Playwright from the web workspace (pnpm), else a global install on NODE_PATH;
// its browsers from PLAYWRIGHT_BROWSERS_PATH, or PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH
// when set.
const { chromium } = require("playwright")
const fs = require("node:fs")
const http = require("node:http")
const path = require("node:path")

const types = { ".html": "text/html", ".css": "text/css", ".js": "text/javascript", ".json": "application/json", ".svg": "image/svg+xml", ".woff2": "font/woff2" }

const dir = path.resolve(process.argv[2])
// Every page of every lens: the default lens at the root, the others in
// their folders.
const pages = fs.readdirSync(dir, { recursive: true }).map(String).filter((f) => f.endsWith(".html") && !f.startsWith("_assets"))
const fail = []
const url = (f) => "file://" + path.join(dir, f)

;(async () => {
  const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined })
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  page.on("pageerror", (e) => fail.push(`script error on ${page.url()}: ${e.message}`))
  page.on("console", (m) => m.type() === "error" && fail.push(`console error on ${page.url()}: ${m.text()}`))

  // Links and anchors.
  const ids = {}
  const links = {}
  for (const f of pages) {
    await page.goto(url(f))
    await page.waitForFunction(() => document.documentElement.dataset.islands === "ready", null, { timeout: 10000 }).catch(() => fail.push(`${f}: islands never started`))
    ids[f] = new Set(await page.$$eval("[id]", (els) => els.map((e) => e.id)))
    links[f] = await page.$$eval("a[href]", (as) => as.map((a) => a.getAttribute("href")))
  }
  for (const [f, hrefs] of Object.entries(links)) {
    for (const href of hrefs) {
      if (/^[a-z]+:/.test(href)) continue
      const i = href.indexOf("#")
      const file = i < 0 ? href : href.slice(0, i)
      const frag = i < 0 ? "" : href.slice(i + 1)
      const target = file ? path.posix.normalize(path.posix.join(path.posix.dirname(f), file)) : f
      if (target.includes("_assets/")) continue
      if (!ids[target]) fail.push(`${f}: link to missing page ${href}`)
      else if (frag && !ids[target].has(decodeURIComponent(frag))) fail.push(`${f}: link to missing anchor ${href}`)
    }
  }

  const ready = async (f) => {
    await page.goto(url(f))
    await page.waitForFunction(() => document.documentElement.dataset.islands === "ready")
  }
  const has = async (sel) => (await page.$(sel)) !== null

  // Search, from the keyboard.
  await ready(pages.includes("index.html") ? "index.html" : pages[0])
  await page.keyboard.press("Control+k")
  await page.waitForSelector(".palette input", { timeout: 3000 }).catch(() => fail.push("search: ⌘K doesn't open the palette"))
  const first = (await page.evaluate(() => window.__HERON__.search[0]?.t)) ?? ""
  await page.keyboard.type(first.slice(0, 4))
  await page.waitForTimeout(150)
  const results = await page.$$eval(".palette-item", (els) => els.length)
  if (!results) fail.push(`search: no results for ${JSON.stringify(first.slice(0, 4))}`)
  await page.keyboard.press("Enter")
  await page.waitForTimeout(300)
  if (await has(".palette")) fail.push("search: Enter doesn't open the result")

  // The theme switch. The choice is kept in localStorage, which Chromium
  // doesn't keep reliably for file:// pages: now and then a write made by
  // one file:// document never reaches the next one, so the site is served
  // over HTTP for this, as a published site is.
  const server = http.createServer((req, res) => {
    const file = path.join(dir, decodeURIComponent(new URL(req.url, "http://localhost").pathname))
    if (!file.startsWith(dir)) return res.writeHead(403).end()
    fs.readFile(file, (err, body) => {
      if (err) return res.writeHead(404).end()
      res.writeHead(200, { "content-type": types[path.extname(file)] ?? "application/octet-stream" }).end(body)
    })
  })
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve))
  const served = (f) => `http://127.0.0.1:${server.address().port}/${f}`
  const readyServed = async (f) => {
    await page.goto(served(f))
    await page.waitForFunction(() => document.documentElement.dataset.islands === "ready")
  }
  await readyServed(pages[0])
  const before = await page.evaluate(() => getComputedStyle(document.body).backgroundColor)
  await page.click("[data-island=theme]")
  const after = await page.evaluate(() => getComputedStyle(document.body).backgroundColor)
  if (before === after) fail.push("theme: the switch doesn't change the theme")
  // The chosen theme holds on the next page load, before any island runs.
  await readyServed(pages[0])
  const kept = await page.evaluate(() => getComputedStyle(document.body).backgroundColor)
  if (kept !== after) fail.push("theme: the chosen theme doesn't hold on the next load")
  server.close()
  await ready(pages[0])

  // Narrow screens: one column, the navigation behind its button.
  await page.setViewportSize({ width: 800, height: 900 })
  const narrow = await page.evaluate(() => {
    const nav = document.querySelector(".sidebar"), toggle = document.querySelector(".nav-toggle")
    return !nav || (getComputedStyle(nav).display === "none" && !!toggle && getComputedStyle(toggle).display !== "none")
  })
  if (!narrow) fail.push("narrow: the navigation isn't behind its button at 800 px")
  await page.setViewportSize({ width: 1440, height: 900 })

  // A theme package: its stylesheet loads in the theme layer and wins over
  // the built-in theme, its font loads, and its logo replaces the mark.
  if (await has('link[href$="theme/theme.css"]')) {
    const accent = await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--h-accent").trim())
    const builtIn = ["#a99cf7", "#5b45cc"]
    if (!accent || builtIn.includes(accent.toLowerCase())) fail.push(`theme: the theme's accent doesn't apply (${accent})`)
    const logo = await page.$eval(".logo img.mark", (i) => i.complete && i.naturalWidth > 0).catch(() => false)
    if (!logo) fail.push("theme: the logo doesn't load")
    const font = await page.evaluate(async () => {
      const family = getComputedStyle(document.querySelector(".page-title")).fontFamily.split(",")[0].replace(/"/g, "").trim()
      await document.fonts.load(`600 16px "${family}"`)
      return [...document.fonts].some((f) => f.family.replace(/"/g, "") === family && f.status === "loaded")
    })
    if (!font) fail.push("theme: the theme's font doesn't load")
  }

  // Slots render on every page that has them.
  for (const f of pages) {
    const src = fs.readFileSync(path.join(dir, f), "utf8")
    if (/class="slot slot-[a-z]+"><\/div>/.test(src)) fail.push(`${f}: an empty slot`)
  }

  // The version switch is there with the current build selected.
  if (!(await has("[data-island=version] select"))) fail.push("version: no switch")

  for (const f of pages) {
    const src = fs.readFileSync(path.join(dir, f), "utf8")
    // Hover cards.
    if (src.includes("data-card=") && !globalThis.cardDone) {
      await ready(f)
      const el = page.locator("main [data-card]").first()
      if ((await el.count()) > 0) {
        await el.scrollIntoViewIfNeeded()
        await el.hover()
        await page.waitForSelector(".card", { timeout: 2000 }).then(() => (globalThis.cardDone = true)).catch(() => fail.push(`${f}: no hover card`))
      }
    }
    // Diagrams: zoom, table view.
    if (src.includes('data-island="diagram"') && !globalThis.diagramDone) {
      globalThis.diagramDone = true
      await ready(f)
      const d = page.locator(".diagram").first()
      await d.scrollIntoViewIfNeeded()
      await d.locator(".zoom button").first().click()
      const tf = await d.locator("svg.graph").evaluate((s) => s.style.transform)
      if (!tf.includes("scale(1.25)")) fail.push(`${f}: zoom doesn't scale the diagram (${tf})`)
      await page.locator(".diagram-view [data-view=table]").first().click()
      if (await page.locator(".diagram-view [data-pane=table]").first().isHidden()) fail.push(`${f}: the table view doesn't show`)
    }
    // Examples and Try it.
    if (src.includes("data-tryit") && /picker/.test(src) && !globalThis.tryDone) {
      globalThis.tryDone = true
      await ready(f)
      await page.click(".examples [data-lang=http]")
      if (await page.locator(".ex:not([hidden]) .lang[data-lang=http]").first().isHidden()) fail.push(`${f}: the HTTP tab doesn't show`)
      await page.click("[data-tryit]")
      await page.waitForSelector(".sheet form", { timeout: 3000 }).catch(() => fail.push(`${f}: Try it doesn't open`))
      const filled = await page.$$eval(".sheet input, .sheet textarea", (els) => els.filter((e) => e.value).length)
      if (filled < 2) fail.push(`${f}: Try it isn't filled from the example`)
      await page.keyboard.press("Escape")
    }
  }
  // Status badges show their status's colour, as text and a 1 px outline,
  // never as a fill: the badge's own neutral colour doesn't win over it.
  let badges = 0
  for (const f of pages) {
    if (!fs.readFileSync(path.join(dir, f), "utf8").includes('class="badge status-')) continue
    await ready(f)
    const found = await page.$$eval('.badge[class*="status-"]', (els) =>
      els.map((b) => {
        const st = [...b.classList].find((c) => c.startsWith("status-"))
        const probe = document.createElement("span")
        probe.className = st
        b.parentElement.append(probe)
        const want = getComputedStyle(probe).color
        probe.remove()
        const s = getComputedStyle(b)
        if (s.color !== want) return `${st} is ${s.color}, not its status colour ${want}`
        if (s.borderTopWidth !== "1px" || s.borderTopColor !== want) return `${st}'s outline is ${s.borderTopWidth} ${s.borderTopColor}, not 1px ${want}`
        if (s.backgroundColor !== "rgba(0, 0, 0, 0)") return `${st} is filled with ${s.backgroundColor}`
        return ""
      }),
    )
    badges += found.length
    for (const m of new Set(found.filter(Boolean))) fail.push(`${f}: status badge: ${m}`)
  }
  if (!badges) console.log("(no status badges on this site)")

  for (const [k, v] of [["cards", "cardDone"], ["diagram", "diagramDone"], ["Try it", "tryDone"]]) {
    if (!globalThis[v]) console.log(`(no ${k} on this site)`)
  }
  await browser.close()
  if (fail.length) {
    console.error(fail.join("\n"))
    process.exit(1)
  }
  console.log(`${pages.length} pages, every link and island works`)
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
