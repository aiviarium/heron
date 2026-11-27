// Moving between pages in place. A link to another page of the same site
// (the same folder, so the same data.js and the same lens) is fetched and
// swapped into this document: the top bar and the frame below it are
// replaced in one step, so the browser never shows a blank document between
// two pages, in any engine. With a view transition where the browser has
// one and the reader allows motion. Everything else, and every page without
// a script, is a plain link.

type Page = () => void

let pageStart: Page = () => {}
let pageStop: Page = () => {}
const cache = new Map<string, Promise<Document | null>>()
let current = 0
/** The path of the page this document shows. */
let shown = location.pathname

/** Whether a URL is a page this document can show in place. */
function local(url: URL): boolean {
  if (location.protocol === "file:") return false
  if (url.origin !== location.origin) return false
  const dir = (p: string) => p.replace(/[^/]*$/, "")
  if (dir(url.pathname) !== dir(location.pathname)) return false
  // The same page with another anchor: the browser scrolls to it.
  if (url.pathname === shown) return false
  return /(\.html|\/)$/.test(url.pathname)
}

function fetchPage(url: URL): Promise<Document | null> {
  const key = url.pathname + url.search
  let p = cache.get(key)
  if (!p) {
    p = fetch(key, { credentials: "same-origin" })
      .then(async (r) => {
        if (!r.ok || !(r.headers.get("content-type") ?? "").includes("html")) return null
        return new DOMParser().parseFromString(await r.text(), "text/html")
      })
      .catch(() => null)
    cache.set(key, p)
    // A page fetched ahead is kept briefly: the site may be rebuilt.
    setTimeout(() => cache.delete(key), 30_000)
  }
  return p
}

/** The script or stylesheet a page loads from a path, by its URL. */
const assets = (doc: Document) =>
  [...doc.querySelectorAll<HTMLElement>("script[src], link[rel=stylesheet]")]
    .map((e) => new URL(e.getAttribute("src") ?? e.getAttribute("href") ?? "", doc.baseURI === "about:blank" ? location.href : doc.baseURI).href)
    .sort()
    .join(" ")

/** Re-creates the inline scripts of swapped-in content, so they run as
 * they do on a loaded page: the navigation's scroll, the binding pick. */
function activate(root: Element) {
  for (const old of root.querySelectorAll("script")) {
    if (old.src || (old.type && old.type !== "text/javascript" && old.type !== "module")) continue
    const s = document.createElement("script")
    s.textContent = old.textContent
    old.replaceWith(s)
  }
}

function swap(doc: Document, url: URL) {
  const topbar = document.querySelector(".topbar")
  const layout = document.querySelector(".layout")
  const nextTop = doc.querySelector(".topbar")
  const nextLayout = doc.querySelector(".layout")
  if (!topbar || !layout || !nextTop || !nextLayout) return false
  pageStop()
  document.title = doc.title
  for (const a of [...document.body.attributes]) document.body.removeAttribute(a.name)
  for (const a of [...doc.body.attributes]) document.body.setAttribute(a.name, a.value)
  // Copies: a fetched page is kept for going back to it.
  const top = document.importNode(nextTop, true)
  const frame = document.importNode(nextLayout, true)
  topbar.replaceWith(top)
  layout.replaceWith(frame)
  activate(top)
  activate(frame)
  const target = url.hash && document.getElementById(decodeURIComponent(url.hash.slice(1)))
  if (target) target.scrollIntoView()
  else window.scrollTo(0, 0)
  shown = url.pathname
  return true
}

/** Shows the page at url in place, or loads it if it can't be. */
export async function navigate(href: string, opts: { push?: boolean; scroll?: number } = {}) {
  const url = new URL(href, location.href)
  if (!local(url)) {
    location.href = url.href
    return
  }
  const id = ++current
  document.documentElement.dataset.islands = "loading"
  const doc = await fetchPage(url)
  if (id !== current) return
  if (!doc || assets(doc) !== assets(document)) {
    location.href = url.href
    return
  }
  const run = () => {
    if (!swap(doc, url)) {
      location.href = url.href
      return
    }
    if (opts.scroll !== undefined) window.scrollTo(0, opts.scroll)
    pageStart()
    document.documentElement.dataset.islands = "ready"
  }
  if (opts.push !== false) {
    history.replaceState({ ...history.state, heron: true, y: window.scrollY }, "")
  }
  const still = matchMedia("(prefers-reduced-motion: reduce)").matches
  const vt = !still && "startViewTransition" in document ? document.startViewTransition(run) : null
  if (vt) {
    // A skipped transition rejects these; the page is swapped either way.
    vt.ready.catch(() => {})
    vt.finished.catch(() => {})
    await vt.updateCallbackDone.catch(() => {})
  } else run()
  if (opts.push !== false) history.pushState({ heron: true, y: 0 }, "", url.href)
  // The new page has the reader's attention: the page itself, not the link
  // that led to it, holds focus, as it would after a load.
  const main = document.getElementById("main")
  if (main) {
    main.tabIndex = -1
    main.focus({ preventScroll: true })
  }
}

/** Takes over the site's own links, back and forward. start runs a page's
 * islands, and stop tears them down before the page is swapped out. */
export function inPlace(start: Page, stop: Page) {
  pageStart = start
  pageStop = stop
  if (location.protocol === "file:" || !("DOMParser" in window) || !history.pushState) return
  history.replaceState({ ...history.state, heron: true }, "")
  history.scrollRestoration = "manual"
  const link = (e: Event) => {
    const a = (e.target as Element | null)?.closest?.("a[href]") as HTMLAnchorElement | null
    if (!a || a.target || a.hasAttribute("download") || a.closest("[data-no-swap]")) return null
    const url = new URL(a.href, location.href)
    return local(url) ? { a, url } : null
  }
  document.addEventListener("click", (e) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return
    const l = link(e)
    if (!l) return
    e.preventDefault()
    void navigate(l.url.href)
  })
  // Fetched ahead as the reader is about to follow a link.
  let timer: number | undefined
  const ahead = (e: Event) => {
    const l = link(e)
    if (!l) return
    clearTimeout(timer)
    timer = window.setTimeout(() => void fetchPage(l.url), 65)
  }
  document.addEventListener("pointerover", ahead)
  document.addEventListener("focusin", ahead)
  document.addEventListener("pointerout", () => clearTimeout(timer))
  addEventListener("popstate", (e) => {
    if (location.pathname === shown) {
      // Back or forward between anchors of this page.
      const target = location.hash && document.getElementById(decodeURIComponent(location.hash.slice(1)))
      if (typeof e.state?.y === "number") window.scrollTo(0, e.state.y)
      else if (target) target.scrollIntoView()
      return
    }
    void navigate(location.href, { push: false, scroll: e.state?.y ?? 0 })
  })
  // Leaving the site: where the reader was, for coming back.
  addEventListener("pagehide", () => history.replaceState({ ...history.state, heron: true, y: window.scrollY }, ""))
}
