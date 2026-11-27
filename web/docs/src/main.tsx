// The docs' islands. Pages are plain HTML the Go renderer wrote; each
// island enhances the part marked with data-island, so a page reads fully
// before any of this runs.
import "./theme.css"
import "./rpc.css"
import { render } from "@solidjs/web"
import { t } from "./data"
import { Cards } from "./islands/cards"
import { Diagram } from "./islands/diagram"
import { Search } from "./islands/search"
import { TryIt } from "./islands/tryit"
import { bindings } from "./islands/bindings"
import { inPlace } from "./navigate"

const $$ = <T extends Element = HTMLElement>(sel: string, root: ParentNode = document) => [...root.querySelectorAll<T>(sel)]

/** What a page's islands leave behind, undone before the next page. */
let teardown: (() => void)[] = []

function mount(component: () => unknown, perPage = true) {
  const host = document.createElement("div")
  document.body.append(host)
  const dispose = render(component as () => never, host)
  if (perPage) teardown.push(() => { dispose(); host.remove() })
}

/** The theme switch: dark and light, remembered. */
function theme() {
  for (const b of $$("[data-island=theme]")) {
    b.addEventListener("click", () => {
      const html = document.documentElement
      const dark = html.dataset.theme ? html.dataset.theme === "dark" : !matchMedia("(prefers-color-scheme: light)").matches
      html.dataset.theme = dark ? "light" : "dark"
      try { localStorage.setItem("heron-theme", html.dataset.theme) } catch { /* private mode */ }
    })
  }
}

/** The lens switch: a plain link to the same page in another lens, which
 * keeps the anchor the reader is at when it leads to the same page. */
function lenses() {
  for (const a of $$<HTMLAnchorElement>(".lens-switch a")) {
    a.addEventListener("click", () => {
      const hash = location.hash
      if (!hash || a.hash) return
      const here = document.body.dataset.page
      if (here && a.pathname.endsWith("/" + here)) a.hash = hash
    })
  }
}

/** The navigation on narrow screens: a sheet over the page, opened at the
 * current page's entry, closed with Escape. */
function nav() {
  for (const b of $$("[data-island=nav]")) {
    const set = (open: boolean) => {
      document.body.classList.toggle("nav-open", open)
      b.setAttribute("aria-expanded", String(open))
      if (open) document.querySelector('.sidebar [aria-current="page"]')?.scrollIntoView({ block: "center" })
    }
    b.addEventListener("click", () => set(!document.body.classList.contains("nav-open")))
  }
}

/** Escape closes the navigation sheet, on every page. */
function navEscape() {
  document.addEventListener("keydown", (e) => {
    const b = document.querySelector<HTMLElement>("[data-island=nav]")
    if (e.key !== "Escape" || !b || !document.body.classList.contains("nav-open")) return
    document.body.classList.remove("nav-open")
    b.setAttribute("aria-expanded", "false")
    b.focus()
  })
}

/** Copy buttons: their data-copy, or the code of their block or panel. */
function copy() {
  for (const b of $$("[data-island=copy]")) {
    b.addEventListener("click", async () => {
      let text = b.dataset.copy
      if (text === undefined) {
        const scope = b.closest("figure, .panel")
        const code = scope && $$("pre code", scope).find((c) => c.offsetParent !== null)
        text = code?.textContent ?? ""
      }
      try {
        await navigator.clipboard.writeText(text)
        const was = b.getAttribute("aria-label")
        b.classList.add("copied")
        b.setAttribute("aria-label", t("copied"))
        setTimeout(() => {
          b.classList.remove("copied")
          if (was !== null) b.setAttribute("aria-label", was)
        }, 1200)
      } catch { /* no clipboard */ }
    })
  }
}

/** Tab lists: roving focus with the arrow keys, one selected tab. */
function tablist(list: HTMLElement, select: (tab: HTMLElement, i: number) => void) {
  const tabs = $$("[role=tab]", list)
  const choose = (i: number, focus: boolean) => {
    tabs.forEach((x, j) => {
      x.setAttribute("aria-selected", String(i === j))
      x.tabIndex = i === j ? 0 : -1
    })
    if (focus) tabs[i].focus()
    select(tabs[i], i)
  }
  tabs.forEach((tab, i) => {
    tab.tabIndex = tab.getAttribute("aria-selected") === "true" ? 0 : -1
    tab.addEventListener("click", () => choose(i, false))
    tab.addEventListener("keydown", (e) => {
      const n = tabs.length
      if (e.key === "ArrowRight") choose((i + 1) % n, true)
      else if (e.key === "ArrowLeft") choose((i - 1 + n) % n, true)
      else if (e.key === "Home") choose(0, true)
      else if (e.key === "End") choose(n - 1, true)
      else return
      e.preventDefault()
    })
  })
}

function views() {
  // A diagram or its table.
  for (const v of $$("[data-island=diagram-view]")) {
    tablist(v.querySelector(".seg")!, (tab) => {
      for (const p of $$("[data-pane]", v)) p.hidden = p.dataset.pane !== tab.dataset.view
    })
  }
  // A union's variants.
  for (const u of $$("[data-island=tabs]")) {
    const panels = $$(":scope > [role=tabpanel]", u)
    tablist(u.querySelector(".seg")!, (_, i) => panels.forEach((p, j) => (p.hidden = i !== j)))
  }
  // An op's examples: language tabs and the example picker.
  for (const ex of $$("[data-island=examples]")) {
    const panel = ex.closest(".aside") ?? ex
    tablist(ex.querySelector(".seg")!, (tab) => {
      for (const l of $$(".lang", ex)) l.hidden = l.dataset.lang !== tab.dataset.lang
      expand()
    })
    ex.querySelector<HTMLSelectElement>(".picker")?.addEventListener("change", (e) => {
      const i = (e.target as HTMLSelectElement).value
      for (const x of $$(".ex", panel)) x.hidden = x.dataset.example !== i
      expand()
    })
  }
}

/** The version switch: the same page in another released build. */
function versions() {
  for (const sel of $$<HTMLSelectElement>("[data-island=version] select")) {
    sel.addEventListener("change", () => {
      const page = document.body.dataset.page ?? "index.html"
      const here = location.href.replace(/[^/]*([?#].*)?$/, "")
      const root = here.replace(/v\/[^/]+\/$/, "")
      location.href = sel.value ? `${root}v/${encodeURIComponent(sel.value)}/${page}` : `${root}${page}`
    })
  }
}

/** The navigation keeps its place from page to page of one lens's site
 * and area: its scroll position and the entry followed, with where that
 * entry was on screen, saved here as a page is left and put back by the inline
 * script after the navigation before the next page paints. */
function navScroll() {
  const nav = document.querySelector<HTMLElement>(".sidebar")
  const logo = document.querySelector<HTMLAnchorElement>(".logo")
  if (!nav || !logo) return
  // One place per site area: each area has its own navigation.
  const area = document.querySelector<HTMLAnchorElement>(".tabs [aria-current]")
  const key = "heron-nav:" + logo.href + (area ? " " + area.href : "")
  let followed: { h: string; y: number } | null = null
  const save = () => {
    try {
      sessionStorage.setItem(key, JSON.stringify({ t: nav.scrollTop, ...followed }))
    } catch { /* storage blocked */ }
  }
  nav.addEventListener("click", (e) => {
    const a = (e.target as Element).closest<HTMLAnchorElement>("a[href]")
    if (!a) return
    followed = { h: a.getAttribute("href")!, y: a.getBoundingClientRect().top - nav.getBoundingClientRect().top }
    save()
  })
  addEventListener("pagehide", save)
  // Back from the bfcache: what's saved is this page's again.
  const shown = (e: PageTransitionEvent) => { if (e.persisted) followed = null }
  addEventListener("pageshow", shown)
  teardown.push(() => {
    save()
    removeEventListener("pagehide", save)
    removeEventListener("pageshow", shown)
  })
}

/** The outline follows the heading in view. */
function outline() {
  const links = $$<HTMLAnchorElement>(".outline a")
  if (!links.length || !("IntersectionObserver" in window)) return
  const byId = new Map(links.map((a) => [decodeURIComponent(a.hash.slice(1)), a]))
  const io = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue
        for (const a of links) a.classList.remove("active")
        byId.get(e.target.id)?.classList.add("active")
      }
    },
    { rootMargin: "-60px 0px -70% 0px" },
  )
  for (const id of byId.keys()) {
    const h = document.getElementById(id)
    if (h) io.observe(h)
  }
  teardown.push(() => io.disconnect())
}

/** A page's islands: run on load and on every page shown in place. */
function page() {
  theme()
  lenses()
  nav()
  copy()
  views()
  versions()
  navScroll()
  outline()
  bindings()
  expand()
  if (document.querySelector("[data-card]")) mount(() => <Cards />)
  for (const root of $$("[data-island=examples]")) mount(() => <TryIt root={root} />)
  for (const root of $$(".diagram[data-island=diagram]")) {
    const dispose = render(() => <Diagram root={root} />, root)
    teardown.push(dispose)
  }
}

/** A long example or response in a panel shows its start and a button
 * for the rest. */
function expand() {
  for (const pre of $$("[data-island=examples] .panel pre, .response-panel .panel pre, .aside .panel pre")) {
    if (pre.dataset.expand || pre.scrollHeight <= 300) continue
    pre.dataset.expand = "1"
    pre.classList.add("clipped")
    const b = document.createElement("button")
    b.type = "button"
    b.className = "expand"
    b.textContent = t("show_all")
    b.setAttribute("aria-expanded", "false")
    b.addEventListener("click", () => {
      const open = pre.classList.toggle("clipped") === false
      b.textContent = t(open ? "show_less" : "show_all")
      b.setAttribute("aria-expanded", String(open))
    })
    pre.after(b)
  }
}

function leave() {
  for (const f of teardown) f()
  teardown = []
}

/** What stays for the whole visit: search, the keyboard, moving in place. */
function start() {
  navEscape()
  if (document.querySelector("[data-island=search]")) mount(() => <Search />, false)
  page()
  inPlace(page, leave)
  document.documentElement.dataset.islands = "ready"
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start)
else start()
