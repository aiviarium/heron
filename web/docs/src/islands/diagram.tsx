import { t } from "../data"

/**
 * Pan and zoom on a diagram the generator laid out, highlighting a node's
 * neighbours on hover. Nodes and edges are links, so a click opens them.
 */
export function Diagram(props: { root: HTMLElement }) {
  const svg = props.root.querySelector<SVGSVGElement>("svg.graph")
  if (!svg) return null
  // Plain state: Solid 2 applies signal writes on the next flush, and the
  // transform must follow the pointer at once.
  let current = { x: 0, y: 0, k: 1 }
  const view = () => current
  const setView = (v: typeof current) => { current = v }
  const apply = () => {
    const v = view()
    svg.style.transform = `translate(${v.x}px, ${v.y}px) scale(${v.k})`
    props.root.classList.toggle("zoomable", v.k !== 1 || v.x !== 0 || v.y !== 0)
  }
  const zoom = (f: number, cx = props.root.clientWidth / 2, cy = props.root.clientHeight / 2) => {
    const v = view()
    const k = Math.min(4, Math.max(0.4, v.k * f))
    setView({ k, x: cx - ((cx - v.x) * k) / v.k, y: cy - ((cy - v.y) * k) / v.k })
    apply()
  }
  props.root.addEventListener(
    "wheel",
    (e) => {
      if (!e.ctrlKey && !e.metaKey) return
      e.preventDefault()
      const r = props.root.getBoundingClientRect()
      zoom(e.deltaY < 0 ? 1.1 : 1 / 1.1, e.clientX - r.left, e.clientY - r.top)
    },
    { passive: false },
  )
  let drag: { x: number; y: number; vx: number; vy: number; moved: boolean } | undefined
  props.root.addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return
    const v = view()
    drag = { x: e.clientX, y: e.clientY, vx: v.x, vy: v.y, moved: false }
  })
  window.addEventListener("pointermove", (e) => {
    if (!drag) return
    const dx = e.clientX - drag.x
    const dy = e.clientY - drag.y
    if (!drag.moved && Math.hypot(dx, dy) < 4) return
    drag.moved = true
    props.root.classList.add("panning")
    setView({ ...view(), x: drag.vx + dx, y: drag.vy + dy })
    apply()
  })
  window.addEventListener("pointerup", () => {
    if (drag?.moved) {
      // A pan isn't a click on the node under the pointer.
      props.root.addEventListener("click", (e) => e.preventDefault(), { capture: true, once: true })
    }
    drag = undefined
    props.root.classList.remove("panning")
  })

  // Highlight a node with the edges and nodes next to it.
  const graph = svg.closest("svg")!
  for (const node of svg.querySelectorAll<SVGGElement>(".state")) {
    const id = node.dataset.node
    const on = () => {
      graph.classList.add("dim")
      node.classList.add("hl")
      for (const l of svg.querySelectorAll<SVGGElement>(".link")) {
        if (l.dataset.from === id || l.dataset.to === id) {
          l.classList.add("hl")
          const other = l.dataset.from === id ? l.dataset.to : l.dataset.from
          svg.querySelector(`.state[data-node="${CSS.escape(other ?? "")}"]`)?.classList.add("hl")
        }
      }
    }
    const off = () => {
      graph.classList.remove("dim")
      for (const x of svg.querySelectorAll(".hl")) x.classList.remove("hl")
    }
    node.addEventListener("pointerenter", on)
    node.addEventListener("pointerleave", off)
    node.addEventListener("focusin", on)
    node.addEventListener("focusout", off)
  }

  const icon = (d: string) => (
    <svg viewBox="0 0 16 16" width="13" height="13" aria-hidden="true"><path d={d} stroke="currentColor" stroke-width="1.4" stroke-linecap="round" fill="none" /></svg>
  )
  return (
    <div class="zoom">
      <button type="button" class="icon-btn" aria-label={t("zoom_in")} onClick={() => zoom(1.25)}>{icon("M8 3.5v9M3.5 8h9")}</button>
      <button type="button" class="icon-btn" aria-label={t("zoom_out")} onClick={() => zoom(0.8)}>{icon("M3.5 8h9")}</button>
      <button type="button" class="icon-btn" aria-label={t("zoom_reset")} onClick={() => { setView({ x: 0, y: 0, k: 1 }); apply() }}>{icon("M3 8a5 5 0 1 0 1.5-3.5M3 3v2.5h2.5")}</button>
    </div>
  )
}
