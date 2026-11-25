import { createHoverCardController, HoverCard, HoverCardContent } from "@ui-sdk/solid/hover-card"
import { createSignal, Show } from "solid-js"
import { type Card, data } from "../data"

/**
 * Hover cards for every term and type reference on the page: one card,
 * opened against whichever link the pointer or keyboard focus is on.
 */
export function Cards() {
  const card = createHoverCardController<string>()
  const [current, setCurrent] = createSignal<Card | undefined>()
  let timer: number | undefined
  const show = (el: HTMLElement) => {
    const c = data().cards[el.dataset.card ?? ""]
    if (!c) return
    clearTimeout(timer)
    timer = window.setTimeout(() => {
      setCurrent(c)
      card.open({ anchor: el, payload: el.dataset.card })
    }, 300)
  }
  const hide = () => {
    clearTimeout(timer)
    timer = window.setTimeout(() => card.close(), 150)
  }
  for (const el of document.querySelectorAll<HTMLElement>("[data-card]")) {
    el.addEventListener("pointerenter", () => show(el))
    el.addEventListener("pointerleave", hide)
    el.addEventListener("focus", () => show(el))
    el.addEventListener("blur", hide)
  }
  return (
    <HoverCard controller={card}>
      <HoverCardContent class="card" side="bottom" align="start" sideOffset={6} onPointerEnter={() => clearTimeout(timer)} onPointerLeave={hide}>
        <Show when={current()}>
          {(c) => (
            <>
              <div class="card-head">
                <b>{c().t}</b>
                <span class="kind">{c().k}</span>
              </div>
              <div innerHTML={c().h} />
            </>
          )}
        </Show>
      </HoverCardContent>
    </HoverCard>
  )
}
