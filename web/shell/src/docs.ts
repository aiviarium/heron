// The docs theme and islands in the preview: docs.css once, the data the
// islands read (window.__HERON__), and docs.js run again once the preview
// settles, so the preview looks and behaves as the docs do. The islands'
// hosts from the run before are removed first, and nothing runs while the
// person is typing, so focus and keys stay in the editor.
const ASSETS = "/docs/_assets/";
let loaded: Promise<void> | null = null;
let runs = 0;
void runs;
let timer: ReturnType<typeof setTimeout> | null = null;
let hosts: Element[] = [];

function script(src: string): Promise<void> {
  return new Promise((resolve) => {
    const el = document.createElement("script");
    el.src = src;
    el.dataset.heronDocs = "";
    el.onload = el.onerror = () => resolve();
    document.head.appendChild(el);
  });
}

/** Loads the theme and the islands' data, once. */
export function loadDocs(): Promise<void> {
  if (!loaded) {
    const link = document.createElement("link");
    link.rel = "stylesheet";
    link.href = ASSETS + "docs.css";
    document.head.prepend(link);
    loaded = script(ASSETS + "data.js");
  }
  return loaded;
}

let code: Promise<string> | null = null;

async function run(root: HTMLElement) {
  await loadDocs();
  if (!root.isConnected || !root.querySelector("[data-island], [data-card]")) return;
  code ??= fetch(ASSETS + "docs.js").then((r) => r.text());
  const src = await code;
  // docs.js runs synchronously here, so the body's new children are its
  // islands' hosts and nothing else, such as an overlay opened meanwhile.
  for (const h of hosts) h.remove();
  const before = new Set(document.body.children);
  const active = document.activeElement as HTMLElement | null;
  runs++;
  (0, eval)(src);
  hosts = [...document.body.children].filter((c) => !before.has(c));
  if (active && document.activeElement !== active) active.focus();
}

/** Enhances the islands in freshly inserted HTML once typing pauses. */
export function enhance(root: HTMLElement, delay = 600) {
  void loadDocs();
  if (timer) clearTimeout(timer);
  timer = setTimeout(() => void run(root), delay);
}
