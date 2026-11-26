// The binding switch of an op or an api bound to more than one protocol:
// it shows the parts of the page, and of the examples beside it, that the
// binding picked describes, and keeps the pick from page to page.

const key = "heron-binding"

const $$ = <T extends Element = HTMLElement>(sel: string, root: ParentNode = document) => [...root.querySelectorAll<T>(sel)]

function show(binding: string) {
  for (const tab of $$("[data-binding-tab]")) {
    const on = tab.dataset.bindingTab === binding
    tab.setAttribute("aria-selected", String(on))
    tab.tabIndex = on ? 0 : -1
  }
  for (const part of $$("[data-binding]")) part.hidden = part.dataset.binding !== binding
}

export function bindings() {
  const tabs = $$("[data-binding-tab]")
  if (!tabs.length) return
  const offered = tabs.map((t) => t.dataset.bindingTab!)
  let saved: string | null = null
  try { saved = localStorage.getItem(key) } catch { /* storage blocked */ }
  if (saved && offered.includes(saved)) show(saved)
  for (const tab of tabs) {
    tab.addEventListener("click", () => {
      const b = tab.dataset.bindingTab!
      show(b)
      try { localStorage.setItem(key, b) } catch { /* storage blocked */ }
    })
    tab.addEventListener("keydown", (e) => {
      const own = $$("[data-binding-tab]", tab.parentElement!)
      const i = own.indexOf(tab)
      let j = -1
      if (e.key === "ArrowRight") j = (i + 1) % own.length
      else if (e.key === "ArrowLeft") j = (i - 1 + own.length) % own.length
      if (j < 0) return
      e.preventDefault()
      own[j].click()
      own[j].focus()
    })
  }
}
