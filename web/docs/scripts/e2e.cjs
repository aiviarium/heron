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
const sidebarOf = (f) => (fs.readFileSync(path.join(dir, f), "utf8").match(/<nav class="sidebar"[\s\S]*?<\/nav>/) || [""])[0]
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

  // No flash between pages: in each theme, chosen against the system's or
  // following it, the first frame a page paints already has the page's
  // theme, the frame doesn't move, and the browser keeps the chrome in
  // place with a view transition.
  const linkTo = pages.includes("index.html") && sidebarOf("index.html") ? "index.html" : pages.find((f) => (sidebarOf(f).match(/<a href="[^"#:]+"/g) || []).length > 3) ?? pages[0]
  for (const [scheme, chosen] of [["dark", null], ["light", null], ["light", "dark"], ["dark", "light"]]) {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: scheme, reducedMotion: "no-preference" })
    await ctx.addInitScript(() => {
      const m = (window.__frames = [])
      addEventListener("pagereveal", (e) => { window.__vt = !!e.viewTransition })
      const f = () => {
        if (document.body) m.push({ bg: getComputedStyle(document.body).backgroundColor, x: document.querySelector("main")?.getBoundingClientRect().left })
        if (m.length < 5) requestAnimationFrame(f)
      }
      requestAnimationFrame(f)
    })
    const p = await ctx.newPage()
    await p.goto(served(linkTo))
    if (chosen) await p.evaluate((c) => localStorage.setItem("heron-theme", c), chosen)
    await p.reload()
    for (let i = 0; i < 3; i++) {
      await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
      const from = await p.evaluate(() => document.querySelector("main").getBoundingClientRect().left)
      const next = await p.evaluate((i) => {
        const as = [...document.querySelectorAll(".sidebar a[href]")].filter((a) => a.offsetParent && !a.getAttribute("aria-current") && !/^([a-z]+:|#)/.test(a.getAttribute("href")))
        const a = as[(i * 5 + 2) % as.length]
        if (a) a.dataset.e2e = "next"
        return !!a
      }, i)
      if (!next) break
      await Promise.all([p.waitForNavigation(), p.click("a[data-e2e=next]")])
      await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
      await p.waitForTimeout(50)
      const r = await p.evaluate(() => ({ first: window.__frames[0], bg: getComputedStyle(document.body).backgroundColor, x: document.querySelector("main").getBoundingClientRect().left, vt: window.__vt, page: document.body.dataset.page }))
      const want = chosen ?? scheme
      const dark = (c) => c.match(/\d+/g).slice(0, 3).reduce((a, b) => a + +b, 0) < 384
      if (!r.first || r.first.bg !== r.bg) fail.push(`theme flash: ${r.page} paints ${r.first?.bg} first, then ${r.bg} (${scheme} system, ${chosen ?? "no"} choice)`)
      if (dark(r.bg) !== (want === "dark")) fail.push(`theme: ${r.page} is ${r.bg} in the ${want} theme`)
      if (Math.abs(r.x - from) > 1) fail.push(`frame shift: the page moves ${r.x - from} px going to ${r.page}`)
      if (!r.vt) fail.push(`view transition: none going to ${r.page}`)
    }
    await ctx.close()
  }

  // The navigation lists pages alone: no page's navigation links to an
  // anchor, and what's on a page is in its outline, nested two levels.
  for (const f of pages) if (sidebarOf(f).includes('href="#')) fail.push(`nav: ${f} lists an anchor of a page`)
  const nested = pages.find((f) => /<nav class="outline"[\s\S]*?<li class="l3">/.test(fs.readFileSync(path.join(dir, f), "utf8")))
  if (nested) {
    await readyServed(nested)
    const n = await page.evaluate(() => ({ anchors: document.querySelectorAll('.sidebar a[href^="#"]').length, l3: document.querySelectorAll(".outline li.l3 a").length }))
    if (n.anchors || !n.l3) fail.push(`nav: on ${nested} the navigation has ${n.anchors} anchors and the outline ${n.l3} nested entries`)
  } else console.log("(no nested outline on this site)")

  // The navigation keeps its scroll from page to page, and with nothing
  // kept it scrolls the current page's entry into view.
  // A page with a long navigation, and the link furthest down it to a page
  // whose navigation is as long, so the position has room to hold.
  const items = (f) => (sidebarOf(f).match(/<li>/g) || []).length
  const long = pages.filter((f) => items(f) > 40).sort((a, b) => items(b) - items(a))[0]
  if (long) {
    await readyServed(long)
    const hrefs = await page.evaluate(() => [...document.querySelectorAll(".sidebar a[href]")].map((a, i) => (a.dataset.i = String(i), a.offsetParent && !a.getAttribute("aria-current") ? a.getAttribute("href") : null)))
    // Down the list but not at its end, and put at the top of the
    // navigation: kept, the position differs from scrolling the entry into
    // view, which would leave it at the bottom.
    const picks = []
    hrefs.forEach((h, i) => {
      const f = h && path.normalize(path.join(path.dirname(long), h))
      if (f && pages.includes(f) && items(f) >= items(long) - 5) picks.push(i)
    })
    const pick = picks.length ? picks[Math.floor(picks.length * 0.6)] : -1
    const target = await page.evaluate((i) => {
      const nav = document.querySelector(".sidebar")
      const a = nav.querySelector(`a[data-i="${i}"]`)
      if (!a) return { top: 0, scrollable: false }
      a.scrollIntoView({ block: "start" })
      nav.scrollTop -= 120
      a.dataset.e2e = "far"
      return { top: nav.scrollTop, y: a.getBoundingClientRect().top, scrollable: nav.scrollHeight > nav.clientHeight }
    }, pick)
    if (target.scrollable && target.top > 0) {
      await Promise.all([page.waitForNavigation(), page.click("a[data-e2e=far]")])
      // Before any island runs: the inline script alone puts it back.
      const early = await page.evaluate(() => document.querySelector(".sidebar").scrollTop)
      await page.waitForFunction(() => document.documentElement.dataset.islands === "ready")
      // The entry followed stays where it was on screen, unless the next
      // page's navigation can't scroll that far.
      const next = await page.evaluate(() => {
        const n = document.querySelector(".sidebar")
        const a = n.querySelector('[aria-current="page"]')
        return { top: n.scrollTop, max: n.scrollHeight - n.clientHeight, y: a ? a.getBoundingClientRect().top : null }
      })
      const clamped = next.top <= 0 || next.top >= next.max - 1
      if (next.y === null || (!clamped && Math.abs(next.y - target.y) > 2)) fail.push(`nav scroll: the entry followed was at ${target.y} px, on the next page it's at ${next.y} px`)
      if (Math.abs(early - next.top) > 1) fail.push(`nav scroll: put back late, at ${early} before the islands and ${next.top} after`)
      // Nothing kept: the current page's entry is scrolled into view.
      await page.evaluate(() => sessionStorage.clear())
      await page.reload()
      const seen = await page.evaluate(() => {
        const nav = document.querySelector(".sidebar").getBoundingClientRect()
        const a = document.querySelector('.sidebar [aria-current="page"]')
        if (!a) return true
        const r = a.getBoundingClientRect()
        return r.top >= nav.top && r.bottom <= nav.bottom
      })
      if (!seen) fail.push("nav scroll: with nothing kept, the current page's entry isn't in view")
    }
  } else console.log("(no long navigation on this site)")
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
