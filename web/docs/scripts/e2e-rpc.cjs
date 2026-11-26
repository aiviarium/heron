// End to end check of the RPC views of a generated docs site, the parcels
// example's, in Chromium:
//   node web/docs/scripts/e2e-rpc.cjs <site dir> [screenshot dir]
// Serves the site and a mock of the parcels service that speaks Connect
// (unary and a server stream, JSON) and JSON-RPC over HTTP, then drives the
// binding switch, which keeps the reader's pick from page to page, and Try
// it on each binding a browser can call. With a screenshot dir, also
// writes rpc-*.png of the service, a unary and a bidirectional method, a
// message's type page, the switch and Try it, in light and dark.
const { chromium } = require("playwright")
const fs = require("node:fs")
const http = require("node:http")
const path = require("node:path")

const dir = path.resolve(process.argv[2])
const shots = process.argv[3] ? path.resolve(process.argv[3]) : ""
const fail = []
const types = { ".html": "text/html", ".css": "text/css", ".js": "text/javascript", ".json": "application/json", ".svg": "image/svg+xml", ".woff2": "font/woff2" }

const listen = (handler) => new Promise((res) => { const s = http.createServer(handler).listen(0, "127.0.0.1", () => res(s)) })

// The site, as files.
const site = (req, res) => {
  let p = decodeURIComponent(req.url.split("?")[0])
  if (p.endsWith("/")) p += "index.html"
  const f = path.join(dir, p)
  if (!f.startsWith(dir) || !fs.existsSync(f)) { res.writeHead(404); return res.end() }
  res.writeHead(200, { "Content-Type": types[path.extname(f)] ?? "application/octet-stream" })
  fs.createReadStream(f).pipe(res)
}

// The parcels service: what its examples say it answers.
const parcel = { id: "p_1042", status: "PARCEL_STATUS_IN_TRANSIT", weightGrams: 1200, destination: { locker: { code: "LEJ-17" } } }
const events = ["Leipzig depot", "Leipzig hub", "Locker LEJ-17"].map((place, i) => ({ parcelEvent: { status: ["PARCEL_STATUS_LABELLED", "PARCEL_STATUS_IN_TRANSIT", "PARCEL_STATUS_DELIVERED"][i], place } }))
const frame = (flags, msg) => {
  const json = Buffer.from(JSON.stringify(msg))
  const head = Buffer.alloc(5)
  head[0] = flags
  head.writeUInt32BE(json.length, 1)
  return Buffer.concat([head, json])
}
const service = (req, res) => {
  const cors = { "Access-Control-Allow-Origin": "*", "Access-Control-Allow-Headers": "*", "Access-Control-Allow-Methods": "POST, GET" }
  if (req.method === "OPTIONS") { res.writeHead(204, cors); return res.end() }
  const chunks = []
  req.on("data", (c) => chunks.push(c))
  req.on("end", async () => {
    const body = Buffer.concat(chunks)
    if (req.url === "/acme.parcels.v1.ParcelsService/GetParcel") {
      const r = JSON.parse(body.toString() || "{}")
      if (r.id !== "p_1042") {
        res.writeHead(404, { ...cors, "Content-Type": "application/json" })
        return res.end(JSON.stringify({ code: "not_found", message: "No parcel has this id." }))
      }
      res.writeHead(200, { ...cors, "Content-Type": "application/json" })
      return res.end(JSON.stringify({ parcel }))
    }
    if (req.url === "/acme.parcels.v1.ParcelsService/WatchParcel") {
      res.writeHead(200, { ...cors, "Content-Type": "application/connect+json" })
      for (const e of events) {
        res.write(frame(0, e))
        await new Promise((r) => setTimeout(r, 30))
      }
      return res.end(frame(2, {}))
    }
    if (req.url === "/") {
      const r = JSON.parse(body.toString())
      res.writeHead(200, { ...cors, "Content-Type": "application/json" })
      return res.end(JSON.stringify(r.method === "parcels.get" ? { jsonrpc: "2.0", id: r.id, result: { id: "p_1042", status: "IN_TRANSIT", weight_grams: 1200 } } : { jsonrpc: "2.0", id: r.id, error: { code: -32601, message: "no method " + r.method } }))
    }
    res.writeHead(404, cors)
    res.end()
  })
}

;(async () => {
  const web = await listen(site)
  const api = await listen(service)
  const base = `http://127.0.0.1:${web.address().port}/`
  const server = `http://127.0.0.1:${api.address().port}`
  const browser = await chromium.launch({ executablePath: process.env.PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH || undefined })

  for (const theme of ["light", "dark"]) {
    const ctx = await browser.newContext({ viewport: { width: 1440, height: 1000 }, deviceScaleFactor: 2, colorScheme: theme })
    const page = await ctx.newPage()
    page.on("pageerror", (e) => fail.push(`script error on ${page.url()}: ${e.message}`))
    // The switch's selected tab, the parts shown, the outline and the
    // navigation's methods all name the same binding.
    const agree = async (where) => {
      const bad = await page.evaluate(() => {
        const sel = [...document.querySelectorAll(".binding-switch [aria-selected=true]")].map((t) => t.dataset.bindingTab)
        if (!sel.length) return ""
        const b = sel[0]
        if (sel.some((x) => x !== b)) return `two tabs selected: ${sel}`
        for (const p of document.querySelectorAll("[data-binding]")) {
          const shown = !p.hidden
          if (shown !== (p.dataset.binding === b)) return `${b} is selected, and a ${p.dataset.binding} part is ${shown ? "shown" : "hidden"}`
        }
        for (const m of document.querySelectorAll(`.nav .m[data-m-${b}]`)) {
          if (m.textContent !== m.getAttribute(`data-m-${b}`)) return `the navigation shows ${m.textContent} for ${b}`
        }
        return ""
      })
      if (bad) fail.push(`${where}: ${bad}`)
    }
    const open = async (f) => {
      await page.goto(base + f)
      await page.waitForFunction(() => document.documentElement.dataset.islands === "ready")
      await agree(f)
    }
    const shot = async (name, opts = {}) => {
      if (shots) await page.screenshot({ path: path.join(shots, `rpc-${name}-${theme}.png`), ...opts })
    }
    const pick = async (b) => {
      await page.click(`.binding-switch [data-binding-tab=${b}]`)
      if (await page.locator(`.binding[data-binding=${b}]`).first().isHidden()) fail.push(`the ${b} binding doesn't show`)
      await agree(`${page.url()} after picking ${b}`)
    }

    await open("parcels.html")
    await shot("service")
    await pick("grpc")
    await shot("service-grpc")

    // The switch: the pick holds on the next page.
    await open("parcels.get_parcel.html")
    if (await page.locator(".binding[data-binding=grpc]").first().isHidden()) fail.push("the binding picked on the service page isn't kept")
    await shot("unary-grpc")
    // Before the page painted: the navigation shows the gRPC kind of an op
    // that HTTP also carries, and the outline the gRPC headings.
    if ((await page.locator('.nav a[href="parcels.get_parcel.html"] .m').textContent()) !== "RPC") fail.push("the navigation doesn't follow the binding picked")
    if (await page.locator('.outline [data-binding=grpc]').first().isHidden()) fail.push("the outline doesn't follow the binding picked")
    await pick("connect")
    await shot("binding-switch")
    if (await page.locator(".examples[data-binding=connect]").isHidden()) fail.push("the Connect examples don't show")

    // Try it over Connect, unary.
    await page.click(".examples[data-binding=connect] [data-tryit]")
    await page.waitForSelector(".sheet form")
    await page.locator("#tryit-server:visible").fill(server)
    await page.locator(".sheet button[type=submit]:visible").click()
    await page.waitForSelector(".sheet .result:visible", { timeout: 5000 }).catch(() => fail.push("Connect: no response"))
    const unary = await page.locator(".sheet .result:visible").textContent().catch(() => "")
    if (!unary.includes("LEJ-17") || !unary.includes("200")) fail.push(`Connect unary answered ${unary}`)
    await shot("tryit-connect")
    await page.keyboard.press("Escape")
    await page.waitForSelector(".sheet", { state: "detached" })

    // Try it over JSON-RPC.
    await pick("jsonrpc")
    await page.click(".examples[data-binding=jsonrpc] [data-tryit]")
    await page.waitForSelector(".sheet form")
    await page.locator("#tryit-server:visible").fill(server)
    await page.locator(".sheet button[type=submit]:visible").click()
    await page.waitForSelector(".sheet .result:visible", { timeout: 5000 }).catch(() => fail.push("JSON-RPC: no response"))
    const jr = await page.locator(".sheet .result:visible").textContent().catch(() => "")
    if (!jr.includes('"result"') || !jr.includes("p_1042")) fail.push(`JSON-RPC answered ${jr}`)
    await shot("tryit-jsonrpc")
    await page.keyboard.press("Escape")
    await page.waitForSelector(".sheet", { state: "detached" })
    await pick("grpc")
    if (await page.locator(".examples[data-binding=grpc] [data-tryit]").count()) fail.push("gRPC offers Try it")

    // A server stream over Connect: each message as it arrives.
    await open("parcels.watch_parcel.html")
    await pick("connect")
    await page.click(".examples[data-binding=connect] [data-tryit]")
    await page.waitForSelector(".sheet form")
    await page.locator("#tryit-server:visible").fill(server)
    await page.locator(".sheet button[type=submit]:visible").click()
    await page.waitForFunction(() => (document.querySelector(".sheet .result")?.textContent ?? "").includes("Locker LEJ-17"), null, { timeout: 5000 }).catch(() => fail.push("Connect stream: not every message arrived"))
    await shot("tryit-stream")
    await page.keyboard.press("Escape")
    await page.waitForSelector(".sheet", { state: "detached" })

    // A bidirectional method: its messages in order, no Try it.
    await open("parcels.navigate.html")
    await pick("grpc")
    if (await page.locator("[data-tryit]:visible").count()) fail.push("a bidirectional method offers Try it")
    if ((await page.locator(".examples[data-binding=grpc] .rpc-seq li").count()) < 4) fail.push("a bidirectional example doesn't show its messages in order")
    await shot("bidi")

    // A message's type page: its .proto definition and JSON form.
    await open("Parcel.html")
    await page.click(".type-forms .seg button:nth-child(2)")
    if (!(await page.locator(".type-forms [role=tabpanel]:not([hidden])").textContent()).includes("reserved 4;")) fail.push("the Protobuf tab doesn't show the .proto definition")
    await shot("message-type")
    await ctx.close()
  }
  await browser.close()
  web.close()
  api.close()
  if (fail.length) {
    console.error([...new Set(fail)].join("\n"))
    process.exit(1)
  }
  console.log("the binding switch, Connect and JSON-RPC Try it, streams and message pages work")
})().catch((e) => {
  console.error(e)
  process.exit(1)
})
