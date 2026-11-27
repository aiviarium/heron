import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@ui-sdk/solid/select"
import { For } from "solid-js"

/**
 * The lens switch as the version switch's select, in place of the page's
 * dropdown of plain links, which stays the control without a script.
 * Choosing a lens follows its link: the same page in that lens, at the same
 * anchor when it's the same page.
 */
export function Lens(props: { root: HTMLElement }) {
  const links = [...props.root.querySelectorAll<HTMLAnchorElement>(".lens-menu a[href]")]
  if (!links.length) return null
  const items = links.map((a) => ({ value: a.getAttribute("href")!, label: a.textContent ?? "" }))
  const current = links.find((a) => a.getAttribute("aria-current"))?.getAttribute("href") ?? items[0].value
  const caption = props.root.querySelector(".caption")?.textContent ?? ""
  const label = props.root.querySelector("summary")?.getAttribute("aria-label") ?? ""
  props.root.classList.add("ready")
  const go = (href: string) => {
    const a = links.find((x) => x.getAttribute("href") === href)
    if (!a) return
    const here = document.body.dataset.page
    const url = new URL(a.href)
    // The same page in another lens keeps the reader's place on it.
    if (location.hash && !url.hash && here && url.pathname.endsWith("/" + here)) url.hash = location.hash
    location.href = url.href
  }
  return (
    <Select
      items={items}
      defaultValue={current}
      onValueChange={(v: string | null) => {
        if (v !== null && v !== current) go(v)
      }}
    >
      <SelectTrigger class="select-trigger lens-trigger" aria-label={label}>
        <span class="caption">{caption}</span>
        <SelectValue />
        <svg viewBox="0 0 10 10" width="10" height="10" aria-hidden="true">
          <path d="M2 4l3 3 3-3" fill="none" stroke="currentColor" stroke-width="1.3" />
        </svg>
      </SelectTrigger>
      <SelectContent class="select-content">
        <For each={items}>
          {(it) => (
            <SelectItem class="select-item" value={it.value}>
              <span>{it.label}</span>
              <svg class="check" viewBox="0 0 12 12" width="12" height="12" aria-hidden="true">
                <path d="M2.5 6.5l2.2 2.2 4.8-5" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
              </svg>
            </SelectItem>
          )}
        </For>
      </SelectContent>
    </Select>
  )
}
