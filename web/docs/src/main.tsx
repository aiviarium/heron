// The docs' islands. Pages are plain HTML the Go renderer wrote; each
// island enhances the part marked with data-island, so a page reads fully
// before any of this runs.
import "./theme.css"
import { render } from "@solidjs/web"
import { t } from "./data"
import { Cards } from "./islands/cards"
import { Diagram } from "./islands/diagram"
import { Search } from "./islands/search"
import { TryIt } from "./islands/tryit"

const $$ = <T extends Element = HTMLElement>(sel: string, root: ParentNode = document) => [...root.querySelectorAll<T>(sel)]

function mount(component: () => unknown) {
  const host = document.createElement("div")
  document.body.append(host)
  render(component as () => never, host)
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

/** The navigation on narrow screens. */
function nav() {
  for (const b of $$("[data-island=nav]")) {
    b.addEventListener("click", () => {
      const open = document.body.classList.toggle("nav-open")
      b.setAttribute("aria-expanded", String(open))
    })
  }
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
        b.classList.add("copied")
        b.setAttribute("aria-label", t("copied"))
        setTimeout(() => b.classList.remove("copied"), 1200)
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
    })
    ex.querySelector<HTMLSelectElement>(".picker")?.addEventListener("change", (e) => {
      const i = (e.target as HTMLSelectElement).value
      for (const x of $$(".ex", panel)) x.hidden = x.dataset.example !== i
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

/** The navigation keeps its scroll position from page to page of one
 * lens's site: saved here as a page is left, put back by the inline script
 * after the navigation before the next page paints. */
function navScroll() {
  const nav = document.querySelector<HTMLElement>(".sidebar")
  const logo = document.querySelector<HTMLAnchorElement>(".logo")
  if (!nav || !logo) return
  const save = () => {
    try { sessionStorage.setItem("heron-nav:" + logo.href, String(nav.scrollTop)) } catch { /* storage blocked */ }
  }
  nav.addEventListener("click", (e) => { if ((e.target as Element).closest("a")) save() })
  addEventListener("pagehide", save)
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
}

function start() {
  theme()
  lenses()
  nav()
  copy()
  views()
  versions()
  navScroll()
  outline()
  const trigger = document.querySelector<HTMLElement>("[data-island=search]")
  if (trigger) mount(() => <Search trigger={trigger} />)
  if (document.querySelector("[data-card]")) mount(() => <Cards />)
  for (const root of $$("[data-island=examples]")) mount(() => <TryIt root={root} />)
  for (const root of $$(".diagram[data-island=diagram]")) render(() => <Diagram root={root} />, root)
  document.documentElement.dataset.islands = "ready"
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", start)
else start()
