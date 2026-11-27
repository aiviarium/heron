import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@ui-sdk/solid/select"
import { For } from "solid-js"

/**
 * The version switch as a select of the theme's own, in place of the
 * page's native one, which stays the control without a script. Choosing a
 * version changes the native select, whose change handler opens the page in
 * that build.
 */
export function Version(props: { root: HTMLElement }) {
  const native = props.root.querySelector<HTMLSelectElement>("select")
  if (!native) return null
  const items = [...native.options].map((o) => ({ value: o.value, label: o.textContent ?? o.value }))
  props.root.classList.add("ready")
  return (
    <Select
      items={items}
      defaultValue={native.value}
      onValueChange={(v: string | null) => {
        if (v === null || v === native.value) return
        native.value = v
        native.dispatchEvent(new Event("change"))
      }}
    >
      <SelectTrigger class="select-trigger" aria-label={native.getAttribute("aria-label") ?? undefined}>
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
