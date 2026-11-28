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

  // Links and anchors: every page loaded, several at once (HERON_E2E_PAGES,
  // 4 by default), each in a page of its own failing on its script errors.
  const ids = {}
  const links = {}
  const queue = [...pages]
  const crawl = async () => {
    const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
    page.on("pageerror", (e) => fail.push(`script error on ${page.url()}: ${e.message}`))
    page.on("console", (m) => m.type() === "error" && fail.push(`console error on ${page.url()}: ${m.text()}`))
    for (let f; (f = queue.shift()) !== undefined; ) {
      await page.goto(url(f))
      await page.waitForFunction(() => document.documentElement.dataset.islands === "ready", null, { timeout: 10000 }).catch(() => fail.push(`${f}: islands never started`))
      ids[f] = new Set(await page.$$eval("[id]", (els) => els.map((e) => e.id)))
      links[f] = await page.$$eval("a[href]", (as) => as.map((a) => a.getAttribute("href")))
    }
    await page.close()
  }
  await Promise.all(Array.from({ length: Math.max(1, Number(process.env.HERON_E2E_PAGES) || 4) }, crawl))
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
      // Cached as GitHub Pages caches a site: without it Chromium refetches
      // the stylesheet on every navigation and now and then skips the
      // view transition.
      res.writeHead(200, { "content-type": types[path.extname(file)] ?? "application/octet-stream", "cache-control": "public, max-age=600" }).end(body)
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

  // No flash between pages, in every engine there is: in each theme,
  // chosen against the system's or following it, a link to another page of
  // the site swaps the page in place (no new document, so no blank canvas
  // between two pages), every frame across the swap has the page's theme,
  // the frame doesn't move, and the swap runs in a view transition where
  // the engine has one.
  // Follows a link and waits until its page is shown: in place, when the
  // page counts one more page shown (data-navs), or loaded whole.
  const follow = async (p, selector) => {
    const before = await p.evaluate(() => Number(document.documentElement.dataset.navs) || 0)
    await p.click(selector)
    await p.waitForFunction((n) => (Number(document.documentElement.dataset.navs) || 0) > n || !document.documentElement.dataset.navs && n > 0, before, { timeout: 15000 }).catch(() => {})
    await p.waitForFunction(() => document.documentElement.dataset.islands === "ready", null, { timeout: 15000 })
  }
  const linkTo = pages.includes("index.html") && sidebarOf("index.html") ? "index.html" : pages.find((f) => (sidebarOf(f).match(/<a href="[^"#:]+"/g) || []).length > 3) ?? pages[0]
  const engines = [["chromium", browser]]
  if (process.env.HERON_E2E_FIREFOX !== "0") {
    // Firefox when it's installed (CI installs it); WebKit likewise.
    for (const name of ["firefox", "webkit"]) {
      try {
        engines.push([name, await require("playwright")[name].launch()])
      } catch {
        console.log(`(no ${name} here: the in-place check runs in the engines there are)`)
      }
    }
  }
  // The engines at once, each through its own contexts.
  await Promise.all(engines.map(async ([engine, b]) => {
    for (const [scheme, chosen] of [["dark", null], ["light", null], ["light", "dark"], ["dark", "light"]]) {
      const ctx = await b.newContext({ viewport: { width: 1440, height: 900 }, colorScheme: scheme, reducedMotion: "no-preference" })
      const p = await ctx.newPage()
      p.on("pageerror", (e) => fail.push(`${engine}: script error on ${p.url()}: ${e.message}`))
      await p.goto(served(linkTo))
      if (chosen) await p.evaluate((c) => localStorage.setItem("heron-theme", c), chosen)
      await p.reload()
      await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
      // Marks this document, and counts the view transitions it starts.
      await p.evaluate(() => {
        window.__doc = true
        window.__vts = 0
        const vt = document.startViewTransition?.bind(document)
        if (vt) document.startViewTransition = (cb) => { window.__vts++; return vt(cb) }
      })
      for (let i = 0; i < 3; i++) {
        await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
        // A landing page has no navigation and a frame of its own.
        const from = await p.evaluate(() => (document.querySelector(".sidebar") ? document.querySelector("main").getBoundingClientRect().left : null))
        const next = await p.evaluate((i) => {
          const as = [...document.querySelectorAll(".sidebar a[href]")].filter((a) => (a.checkVisibility ? a.checkVisibility() : a.offsetParent) && !a.getAttribute("aria-current") && !/^([a-z]+:|#)/.test(a.getAttribute("href")))
          const a = as[(i * 5 + 2) % as.length]
          if (!a) return null
          a.dataset.e2e = "next"
          // Every frame from the click until the next page is ready.
          const m = (window.__frames = [])
          const f = () => {
            m.push({ html: getComputedStyle(document.documentElement).backgroundColor, body: getComputedStyle(document.body).backgroundColor })
            if (document.documentElement.dataset.islands !== "ready" || m.length < 3) requestAnimationFrame(f)
          }
          requestAnimationFrame(f)
          return a.getAttribute("href")
        }, i)
        if (!next) break
        const vtsBefore = await p.evaluate(() => window.__vts)
        await follow(p, "a[data-e2e=next]")
        if (!p.url().split("#")[0].endsWith("/" + next.replace(/#.*/, ""))) fail.push(`${engine}: following the link to ${next} shows ${p.url()}`)
        await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
        await p.waitForTimeout(300)
        const r = await p.evaluate(() => ({ inPlace: window.__doc === true, frames: window.__frames ?? [], vts: window.__vts, canVt: "startViewTransition" in document, bg: getComputedStyle(document.body).backgroundColor, x: document.querySelector(".sidebar") ? document.querySelector("main").getBoundingClientRect().left : null, page: document.body.dataset.page }))
        const want = chosen ?? scheme
        const dark = (c) => c.match(/\d+/g).slice(0, 3).reduce((a, b) => a + +b, 0) < 384
        if (!r.inPlace) fail.push(`${engine}: ${r.page} loaded a new document rather than being shown in place`)
        for (const f of r.frames) {
          if (dark(f.html) !== (want === "dark") || dark(f.body) !== (want === "dark")) {
            fail.push(`${engine}: theme flash going to ${r.page}: a frame paints ${f.html} / ${f.body} in the ${want} theme`)
            break
          }
        }
        if (dark(r.bg) !== (want === "dark")) fail.push(`${engine}: theme: ${r.page} is ${r.bg} in the ${want} theme`)
        if (from !== null && r.x !== null && Math.abs(r.x - from) > 1) fail.push(`${engine}: frame shift: the page moves ${r.x - from} px going to ${r.page}`)
        if (r.canVt && r.inPlace && r.vts <= vtsBefore) fail.push(`${engine}: view transition: none going to ${r.page}`)
      }
      await ctx.close()
    }
    if (b !== browser) await b.close()
  }))

  // Back and forward between pages shown in place: the page the URL names.
  {
    const p = await browser.newPage({ viewport: { width: 1440, height: 900 } })
    await p.goto(served(linkTo))
    await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
    const titles = [await p.title()]
    const hrefs = []
    for (let i = 0; i < 2; i++) {
      // The next link from the page now shown, which may be another area's
      // or a landing page without the navigation.
      const h = await p.evaluate(() => [...document.querySelectorAll(".sidebar a[href]")].find((a) => (a.checkVisibility ? a.checkVisibility() : a.offsetParent) && !a.getAttribute("aria-current") && !/^([a-z]+:|#)/.test(a.getAttribute("href")))?.getAttribute("href"))
      if (!h) break
      hrefs.push(h)
      await follow(p, `.sidebar a[href="${h}"]:visible >> nth=0`)
      await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
      titles.push(await p.title())
    }
    if (hrefs.length === 2) {
      await p.goBack()
      await p.waitForFunction((t) => document.title === t, titles[1], { timeout: 5000 }).catch(() => fail.push(`back: shows "${titles.at(-1)}", not "${titles[1]}"`))
      await p.goForward()
      await p.waitForFunction((t) => document.title === t, titles[2], { timeout: 5000 }).catch(() => fail.push(`forward: doesn't show "${titles[2]}"`))
    }
    // A link not followed yet is fetched ahead as the pointer comes to rest
    // on it.
    await p.goto(served(linkTo))
    await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
    const ahead = await p.evaluate(() => [...document.querySelectorAll(".sidebar a[href]")].filter((a) => (a.checkVisibility ? a.checkVisibility() : a.offsetParent) && !a.getAttribute("aria-current") && !/^([a-z]+:|#)/.test(a.getAttribute("href")))[3]?.getAttribute("href"))
    if (ahead) {
      const fetched = p.waitForRequest((r) => r.url().endsWith("/" + ahead), { timeout: 3000 }).then(() => true, () => false)
      await p.hover(`.sidebar a[href="${ahead}"]:visible >> nth=0`)
      if (!(await fetched)) fail.push(`prefetch: hovering the link to ${ahead} doesn't fetch it`)
    }
    await p.close()
  }

  // The frame uses the window: the navigation at its left edge and the
  // right column at its right edge, and on a window wider than the frame,
  // equal room on both sides.
  {
    const p = await browser.newPage({ viewport: { width: 1873, height: 1000 } })
    const framed = pages.find((f) => /<nav class="sidebar"/.test(fs.readFileSync(path.join(dir, f), "utf8")) && /<nav class="outline"/.test(fs.readFileSync(path.join(dir, f), "utf8")))
    if (framed) {
      for (const w of [1873, 2560]) {
        await p.setViewportSize({ width: w, height: 1000 })
        await p.goto(served(framed))
        const r = await p.evaluate(() => {
          const side = document.querySelector(".sidebar").getBoundingClientRect()
          const aside = document.querySelector(".aside").getBoundingClientRect()
          return { left: side.left, right: document.documentElement.clientWidth - aside.right }
        })
        if (w === 1873 && (r.left > 48 || r.right > 48)) fail.push(`frame: at ${w} px the navigation starts ${Math.round(r.left)} px in and the right column ends ${Math.round(r.right)} px short of the edge`)
        if (Math.abs(r.left - r.right) > 48) fail.push(`frame: at ${w} px ${Math.round(r.left)} px of room on the left and ${Math.round(r.right)} px on the right`)
      }
    }
    // One content width, centred between the navigation and the right
    // column: the same width and x on a prose page, an op page and a type
    // page, whatever each holds.
    const byLayout = {}
    for (const f of pages) {
      const src = fs.readFileSync(path.join(dir, f), "utf8")
      const lay = (src.match(/<body data-layout="([a-z]+)"/) || [])[1]
      if (lay && !byLayout[lay] && /<nav class="sidebar"/.test(src) && ["section", "home", "op", "type", "api"].includes(lay)) byLayout[lay] = f
    }
    const sample = Object.values(byLayout)
    if (sample.length > 1) {
      for (const w of [1440, 1873, 2560]) {
        await p.setViewportSize({ width: w, height: 1000 })
        const seen = []
        for (const f of sample) {
          await p.goto(served(f))
          seen.push({ f, ...(await p.evaluate(() => {
            const m = document.querySelector("main").getBoundingClientRect()
            const side = document.querySelector(".sidebar").getBoundingClientRect()
            const aside = document.querySelector(".aside").getBoundingClientRect()
            // The navigation's column ends where its box does, less the
            // margin its scroll area reaches into the gap with.
            const sideEnd = side.right + parseFloat(getComputedStyle(document.querySelector(".sidebar")).marginRight)
            return { x: Math.round(m.left), w: Math.round(m.width), l: Math.round(m.left - sideEnd), r: Math.round(aside.left - m.right) }
          })) })
        }
        for (const s of seen) {
          if (Math.abs(s.x - seen[0].x) > 1 || Math.abs(s.w - seen[0].w) > 1) fail.push(`frame: at ${w} px ${s.f} has its content at x ${s.x}, ${s.w} px wide, and ${seen[0].f} at x ${seen[0].x}, ${seen[0].w} px wide`)
          if (Math.abs(s.l - s.r) > 2) fail.push(`frame: at ${w} px ${s.f}'s content isn't centred: ${s.l} px of room on its left and ${s.r} on its right`)
        }
      }
    }
    // The top bar's controls read as controls at rest, and the version
    // switch is a select the keyboard opens and moves through.
    await p.setViewportSize({ width: 1440, height: 900 })
    await p.goto(served(linkTo))
    await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
    const c = await p.evaluate(() => {
      const bad = []
      const tab = document.querySelector(".tabs a[aria-current]")
      if (tab && /rgba\(0, 0, 0, 0\)|transparent/.test(getComputedStyle(tab, "::after").backgroundColor)) bad.push("the current tab has no mark")
      for (const [sel, what] of [[".lens-trigger", "the lens switcher"], [".search-btn", "search"], ["[data-island=theme]", "the theme switch"], [".version .select-trigger", "the version switch"]]) {
        const e = document.querySelector(sel)
        if (sel === ".lens-trigger" && !document.querySelector(".lens-switch")) continue
        if (!e) { bad.push(`${what} isn't there`); continue }
        if (getComputedStyle(e).borderTopWidth === "0px") bad.push(`${what} has no visible edge at rest`)
      }
      return bad
    })
    for (const m of c) fail.push(`top bar: ${m}`)
    // The lens switch: a dropdown right before the version switch, whose
    // options the keyboard opens.
    if (await p.$(".lens-switch")) {
      const order = await p.evaluate(() => {
        const l = document.querySelector(".lens-switch"), v = document.querySelector(".version")
        return l.nextElementSibling === v
      })
      if (!order) fail.push("lens: the lens switch isn't right before the version switch")
      await p.focus(".lens-trigger")
      await p.waitForFunction(() => document.activeElement?.matches(".lens-trigger"), null, { timeout: 5000 }).catch(() => {})
      await p.keyboard.press("ArrowDown")
      // The trigger's own list: a closed list can stay in the page, hidden.
      const lensList = "#" + (await p.getAttribute(".lens-trigger", "aria-controls")) + " .select-item"
      const opts = await p.waitForSelector(lensList, { state: "visible", timeout: 5000 }).then(() => p.$$eval(lensList, (els) => els.length), () => 0)
      if (opts < 2) fail.push("lens: the keyboard doesn't open the lens switch's options")
      await p.keyboard.press("Escape")
    }
    if (await p.$(".version .select-trigger")) {
      await p.focus(".version .select-trigger")
      await p.waitForFunction(() => document.activeElement?.matches(".version .select-trigger"), null, { timeout: 5000 }).catch(() => {})
      await p.keyboard.press("ArrowDown")
      // Its own list: the lens switch's, closed, can still be in the page.
      const list = "#" + (await p.getAttribute(".version .select-trigger", "aria-controls")) + " .select-item"
      const open = await p.waitForSelector(list, { state: "visible", timeout: 5000 }).then(() => true, () => false)
      if (!open) fail.push("version: the keyboard doesn't open the version switch")
      await p.keyboard.press("Escape")
    }
    await p.close()
  }

  // The navigation is a tree: a branch's chevron opens it in place without
  // leaving the page, it stays open on reload, and the keyboard moves
  // through it as a tree view does.
  {
    const branchy = pages.find((f) => /<details class="nav-branch" data-key="[^"]*"><summary>/.test(sidebarOf(f)))
    if (branchy) {
      const p = await browser.newPage({ viewport: { width: 1440, height: 900 } })
      await p.goto(served(branchy))
      await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
      const key = await p.evaluate(() => {
        const d = document.querySelector(".sidebar .nav-branch:not([open])")
        d.scrollIntoView({ block: "center" })
        return d.dataset.key
      })
      const box = await p.locator(`.sidebar .nav-branch[data-key="${key}"] > summary`).boundingBox()
      const before = p.url()
      // The chevron, at the summary's right end.
      await p.mouse.click(box.x + box.width - 8, box.y + box.height / 2)
      await p.waitForTimeout(150)
      const opened = await p.evaluate((k) => document.querySelector(`.sidebar .nav-branch[data-key="${k}"]`).open, key)
      if (!opened || p.url() !== before) fail.push(`nav tree: the chevron of ${key} ${opened ? "left the page" : "doesn't open it"}`)
      await p.reload()
      await p.waitForFunction(() => document.documentElement.dataset.islands === "ready")
      if (!(await p.evaluate((k) => document.querySelector(`.sidebar .nav-branch[data-key="${k}"]`).open, key))) fail.push(`nav tree: ${key} doesn't stay open on reload`)
      // The keyboard: one tab stop, the arrows, a closed branch opened with
      // the right arrow.
      const k = await p.evaluate(() => {
        const tree = document.querySelector(".sidebar [role=tree]")
        const stops = [...document.querySelectorAll(".sidebar [role=treeitem]")].filter((a) => a.tabIndex === 0)
        return { tree: !!tree, stops: stops.length }
      })
      if (!k.tree || k.stops !== 1) fail.push(`nav tree: ${k.tree ? `${k.stops} tab stops` : "no tree role"}`)
      // The down arrow from the first entry in view: the tab stop may be the
      // current page, which can be the last entry in view, with nowhere to go.
      const pair = await p.evaluate(() => {
        const vis = [...document.querySelectorAll(".sidebar [role=treeitem]")].filter((a) => (a.checkVisibility ? a.checkVisibility() : a.offsetParent !== null))
        if (vis.length < 2) return null
        vis[0].focus()
        return [vis[0].getAttribute("href"), vis[1].getAttribute("href")]
      })
      if (pair) {
        await p.keyboard.press("ArrowDown")
        const now = await p.evaluate(() => document.activeElement.getAttribute("href"))
        if (now !== pair[1]) fail.push(`nav tree: the down arrow from ${pair[0]} goes to ${now}, not ${pair[1]}`)
      }
      const closed = await p.evaluate(() => {
        const d = [...document.querySelectorAll(".sidebar .nav-branch:not([open])")].find((x) => (x.checkVisibility ? x.checkVisibility() : x.offsetParent !== null))
        const a = d && d.querySelector(":scope > summary > a")
        if (a) a.focus()
        return a ? d.dataset.key : null
      })
      if (closed) {
        await p.keyboard.press("ArrowRight")
        const now = await p.evaluate((k) => ({ open: document.querySelector(`.sidebar .nav-branch[data-key="${k}"]`).open, exp: document.activeElement.getAttribute("aria-expanded") }), closed)
        if (!now.open || now.exp !== "true") fail.push("nav tree: the right arrow doesn't open a closed branch")
        await p.keyboard.press("ArrowLeft")
        if (await p.evaluate((k) => document.querySelector(`.sidebar .nav-branch[data-key="${k}"]`).open, closed)) fail.push("nav tree: the left arrow doesn't close an open branch")
      }
      await p.close()
    } else console.log("(no navigation branches on this site)")
  }

  // The outline follows the page: its entries in the order the page shows
  // their anchors.
  {
    let checked = 0
    for (const f of pages) {
      const src = fs.readFileSync(path.join(dir, f), "utf8")
      const ol = (src.match(/<nav class="outline"[\s\S]*?<\/nav>/) || [""])[0]
      const ids = [...ol.matchAll(/href="#([^"]+)"/g)].map((m) => m[1])
      if (ids.length < 3) continue
      const main = src.slice(src.indexOf('id="main"'))
      const at = ids.map((id) => main.indexOf(`id="${id}"`))
      for (let i = 1; i < at.length; i++) {
        if (at[i] >= 0 && at[i - 1] >= 0 && at[i] < at[i - 1]) {
          fail.push(`outline: on ${f} ${ids[i]} comes before ${ids[i - 1]} on the page, after it in the outline`)
          break
        }
      }
      if (++checked >= 40) break
    }
  }

  // Each rule and each lifecycle transition a page shows carries its mark
  // in the gutter, the link to it.
  for (const want of [/<div class="rule(?: quality)?" id=/, /<div class="rule transition" id=/]) {
    const ruled = pages.find((f) => want.test(fs.readFileSync(path.join(dir, f), "utf8")))
    if (ruled) {
      const src = fs.readFileSync(path.join(dir, ruled), "utf8")
      const rules = [...src.matchAll(/<div class="rule(?: quality| transition)?" id="([^"]+)"><p class="sentence">(<a class="rule-link[^"]*" href="[^"#]*#([^"]+)")?/g)]
      // A rule shown away from its place links to its home instead.
      const bare = rules.filter((m) => !m[2] || (m[3] !== m[1] && !m[1].startsWith(m[3])))
      if (bare.length) fail.push(`gutter: on ${ruled} ${bare.length} of ${rules.length} rules lack the mark that links to them, such as ${bare[0][1]}`)
    }
  }

  // Lens marks: a quiet row of tags at the start of what's marked, on one
  // line, never floated apart.
  {
    const marked = pages.find((f) => /<div class="marked">\s*<span class="lens-mark">[^<]*<\/span>\s*<span class="lens-mark">/.test(fs.readFileSync(path.join(dir, f), "utf8")))
    if (marked) {
      const p = await browser.newPage({ viewport: { width: 1440, height: 900 } })
      await p.goto(served(marked))
      const bad = await p.evaluate(() => {
        for (const m of document.querySelectorAll(".marked")) {
          const tags = [...m.querySelectorAll(":scope > .lens-mark")]
          if (tags.length < 2) continue
          const tops = new Set(tags.map((t) => Math.round(t.getBoundingClientRect().top)))
          const floated = tags.some((t) => getComputedStyle(t).float !== "none")
          if (tops.size > 1 || floated) return true
        }
        return false
      })
      if (bad) fail.push(`lens marks: on ${marked} a block's lens tags aren't one row at its start`)
      await p.close()
    }
  }

  // Quiet scroll areas: no scrollbar at rest on the page's own scroll areas,
  // nothing that always scrolls; and a how-to's step numbers joined by a line.
  {
    const p = await browser.newPage({ viewport: { width: 1440, height: 900 } })
    await p.goto(served(linkTo))
    const q = await p.evaluate(() => {
      const bad = []
      for (const e of document.querySelectorAll(".sidebar, .aside, pre, .table-wrap")) {
        const s = getComputedStyle(e)
        if (s.overflowX === "scroll" || s.overflowY === "scroll") bad.push(`${e.className || e.tagName} always scrolls`)
        if (!/transparent|rgba\(0, 0, 0, 0\)/.test(s.scrollbarColor)) bad.push(`${e.className || e.tagName} shows its scrollbar at rest (${s.scrollbarColor})`)
      }
      return [...new Set(bad)]
    })
    for (const m of q) fail.push(`scrollbars: ${m}`)
    await p.close()
    const stepped = pages.find((f) => fs.readFileSync(path.join(dir, f), "utf8").includes('<ol class="steps"'))
    if (stepped) {
      const p2 = await browser.newPage({ viewport: { width: 1440, height: 900 } })
      await p2.goto(served(stepped))
      const line = await p2.evaluate(() => {
        const steps = document.querySelectorAll(".steps > .step")
        if (steps.length < 2) return true
        const s = getComputedStyle(steps[0], "::after")
        return s.content !== "none" && s.width === "1px" && parseFloat(s.height) > 8
      })
      if (!line) fail.push(`steps: on ${stepped} the step numbers aren't joined by a line`)
      await p2.close()
    }
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
    const hrefs = await page.evaluate(() => [...document.querySelectorAll(".sidebar a[href]")].map((a, i) => (a.dataset.i = String(i), (a.checkVisibility ? a.checkVisibility() : a.offsetParent) && !a.getAttribute("aria-current") ? a.getAttribute("href") : null)))
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
    // What the theme sets is checked: its accent, its font, its logo.
    const themeCSS = fs.readFileSync(path.join(dir, "_assets/theme/theme.css"), "utf8")
    const accent = await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue("--h-accent").trim())
    const builtIn = ["#4fb6f2", "#0b6fa8"]
    if (/--h-accent:/.test(themeCSS) && (!accent || builtIn.includes(accent.toLowerCase()))) fail.push(`theme: the theme's accent doesn't apply (${accent})`)
    const logo = await page.$eval(".logo img.mark", (i) => i.complete && i.naturalWidth > 0).catch(() => false)
    if (!logo) fail.push("theme: the logo doesn't load")
    const font = !/@font-face/.test(themeCSS) || await page.evaluate(async () => {
      const family = getComputedStyle(document.querySelector(".page-title")).fontFamily.split(",")[0].replace(/"/g, "").trim()
      await document.fonts.load(`600 16px "${family}"`)
      return [...document.fonts].some((f) => f.family.replace(/"/g, "") === family && f.status === "loaded")
    })
    if (!font) fail.push("theme: the theme's font doesn't load")
  }

  // Without a theme's logo, the top bar shows Heron's icon.
  if (!(await has('link[href$="theme/theme.css"]'))) {
    const mark = await page.$eval(".logo img.mark", (i) => i.complete && i.naturalWidth > 0).catch(() => false)
    if (!mark) fail.push("mark: Heron's icon doesn't load in the top bar")
  }

  // Tabs over part of a page, as a kind's example or a type's views: the
  // arrow keys move between them, and an anchor in a hidden tab opens it.
  const tabbed = pages.find((f) => /role="tabpanel" id="[^"]*" aria-labelledby/.test(fs.readFileSync(path.join(dir, f), "utf8")))
  if (tabbed) {
    await ready(tabbed)
    const moved = await page.evaluate(() => {
      const list = document.querySelector('.view-tabs [role="tablist"]')
      const tabs = [...list.querySelectorAll('[role="tab"]')]
      tabs[0].focus()
      tabs[0].dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", bubbles: true }))
      const panel = document.getElementById(tabs[1].getAttribute("aria-controls"))
      return document.activeElement === tabs[1] && tabs[1].getAttribute("aria-selected") === "true" && !panel.hidden
    })
    if (!moved) fail.push(`${tabbed}: the right arrow doesn't move to the next tab`)
  }
  const flatRow = /<table class="fields flat"><thead>[\s\S]*?<\/thead><tbody><tr id="([^"]+)"/
  const flat = pages.find((f) => flatRow.test(fs.readFileSync(path.join(dir, f), "utf8")))
  if (flat) {
    const id = fs.readFileSync(path.join(dir, flat), "utf8").match(flatRow)[1]
    await page.goto(url(flat) + "#" + encodeURIComponent(id))
    await page.waitForFunction(() => document.documentElement.dataset.islands === "ready")
    if (!(await page.evaluate((id) => document.getElementById(id).checkVisibility(), id))) fail.push(`${flat}: an anchor in the Flat tab doesn't open it`)
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
