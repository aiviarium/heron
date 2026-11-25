import { Command, CommandEmpty, CommandInput, CommandItem, CommandList } from "@ui-sdk/solid/command"
import { createDialogController, Dialog, DialogBackdrop, DialogContent, DialogTitle } from "@ui-sdk/solid/dialog"
import { createMemo, createSignal, For } from "solid-js"
import { data, type Entry, t } from "../data"

/** Scores an entry for a query: name matches first, then text. */
export function score(e: Entry, q: string): number {
  const title = e.t.toLowerCase()
  const path = e.p.toLowerCase()
  let s = 0
  for (const word of q.toLowerCase().split(/\s+/).filter(Boolean)) {
    if (title === word) s += 100
    else if (title.startsWith(word)) s += 60
    else if (title.includes(word)) s += 40
    else if (path.includes(word)) s += 25
    else if (e.x?.toLowerCase().includes(word)) s += 8
    else return 0
  }
  return s
}

export function search(q: string, limit = 40): Entry[] {
  if (!q.trim()) return []
  return data()
    .search.map((e) => [e, score(e, q)] as const)
    .filter(([, s]) => s > 0)
    .sort((a, b) => b[1] - a[1] || a[0].t.length - b[0].t.length)
    .slice(0, limit)
    .map(([e]) => e)
}

/** The search palette: ⌘K or the top bar's button opens it. */
export function Search(props: { trigger: HTMLElement }) {
  const dialog = createDialogController()
  const [query, setQuery] = createSignal("")
  const results = createMemo(() => search(query()))
  const open = () => dialog.open()
  props.trigger.addEventListener("click", open)
  document.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
      e.preventDefault()
      open()
    } else if (e.key === "/" && !(e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement)) {
      e.preventDefault()
      open()
    }
  })
  const go = (url: string) => {
    dialog.close()
    location.href = url
  }
  return (
    <Dialog controller={dialog}>
      <DialogBackdrop class="overlay-backdrop" />
      <DialogContent class="palette">
        <DialogTitle class="sr-only">{t("search_placeholder")}</DialogTitle>
        <Command filter={false} onAction={(v) => go(String(v))}>
          <div class="palette-input">
            <svg viewBox="0 0 16 16" width="15" height="15" aria-hidden="true">
              <circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" stroke-width="1.4" />
              <path d="M10.5 10.5 14 14" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" />
            </svg>
            <CommandInput
              aria-label={t("search_placeholder")}
              placeholder={t("search_placeholder")}
              onInput={(e: InputEvent) => setQuery((e.currentTarget as HTMLInputElement).value)}
            />
            <kbd>{t("key_esc")}</kbd>
          </div>
          <CommandList class="palette-list">
            <For each={results()}>
              {(e) => (
                <CommandItem class="palette-item" value={e.u} label={e.t}>
                  <span class="pk">{e.k}</span>
                  <span class="pt">
                    <b>{e.t}</b>
                    <span class="px">{subtitle(e)}</span>
                  </span>
                </CommandItem>
              )}
            </For>
          </CommandList>
          {query().trim() !== "" && results().length === 0 && <CommandEmpty class="palette-empty">{t("search_empty")}</CommandEmpty>}
          <div class="palette-foot">
            <span><kbd>↑</kbd> <kbd>↓</kbd> {t("search_move")}</span>
            <span><kbd>↵</kbd> {t("search_open")}</span>
            <span><kbd>{t("key_esc")}</kbd> {t("search_close")}</span>
          </div>
        </Command>
      </DialogContent>
    </Dialog>
  )
}

/** Where a result is, without repeating its title. */
function subtitle(e: Entry): string {
  const parts: string[] = []
  if (e.g && !e.p.toLowerCase().startsWith(e.g.toLowerCase())) parts.push(e.g)
  if (e.p.toLowerCase() !== e.t.toLowerCase()) parts.push(e.p)
  return parts.join(" · ")
}
