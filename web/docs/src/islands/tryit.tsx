import { createDialogController, Dialog, DialogClose, DialogContent, DialogTitle } from "@ui-sdk/solid/dialog"
import { createSignal, For, Show } from "solid-js"
import { t } from "../data"

interface Param {
  name: string
  in: string
  header: string
  required: boolean
}

interface OpData {
  method: string
  path: string
  params: Param[] | null
  examples: Record<string, unknown>[]
}

const serverKey = "heron-tryit-server"

const text = (v: unknown) => (v === undefined ? "" : typeof v === "string" ? v : JSON.stringify(v, null, 2))

/**
 * Try it: sends the op's request to a configured server, with the
 * parameters filled from the selected example.
 */
export function TryIt(props: { root: HTMLElement }) {
  const raw = props.root.querySelector<HTMLScriptElement>(".tryit-data")
  if (!raw) return null
  const op = JSON.parse(raw.textContent ?? "{}") as OpData
  const dialog = createDialogController()
  const [example, setExample] = createSignal(0)
  const [server, setServer] = createSignal(localStorage.getItem(serverKey) ?? location.origin)
  const [values, setValues] = createSignal<Record<string, string>>({})
  const [result, setResult] = createSignal<{ status: string; body: string } | undefined>()
  const [busy, setBusy] = createSignal(false)
  const params = () => op.params ?? []

  const fill = () => {
    const ex = op.examples[example()] ?? {}
    const v: Record<string, string> = {}
    for (const p of params()) v[p.name] = text(ex[p.name])
    setValues(v)
    setResult(undefined)
  }
  props.root.querySelector(".picker")?.addEventListener("change", (e) => setExample(Number((e.target as HTMLSelectElement).value)))
  props.root.querySelector("[data-tryit]")?.addEventListener("click", () => {
    fill()
    dialog.open()
  })

  const send = async (e: SubmitEvent) => {
    e.preventDefault()
    localStorage.setItem(serverKey, server())
    let path = op.path
    const query = new URLSearchParams()
    const headers: Record<string, string> = {}
    let body: string | undefined
    for (const p of params()) {
      const v = values()[p.name] ?? ""
      if (v === "") continue
      if (p.in === "path") path = path.replace(`:${p.name}`, encodeURIComponent(v))
      else if (p.in === "query") {
        let parsed: unknown = v
        try { parsed = JSON.parse(v) } catch { /* a plain string */ }
        for (const x of Array.isArray(parsed) ? parsed : [parsed]) query.append(p.name, String(x))
      } else if (p.in === "header") headers[p.header] = v
      else {
        body = v
        headers["Content-Type"] = "application/json"
      }
    }
    const qs = query.toString()
    setBusy(true)
    try {
      const res = await fetch(server().replace(/\/$/, "") + path + (qs ? `?${qs}` : ""), { method: op.method, headers, body })
      const textBody = await res.text()
      let shown = textBody
      try { shown = JSON.stringify(JSON.parse(textBody), null, 2) } catch { /* not JSON */ }
      setResult({ status: `${res.status} ${res.statusText}`, body: shown })
    } catch (err) {
      setResult({ status: t("try_failed"), body: String(err) })
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog controller={dialog} modal="trap-focus">
      <DialogContent class="sheet">
        <div class="sheet-head">
          <DialogTitle>{t("try_title")}</DialogTitle>
          <DialogClose class="icon-btn" aria-label={t("try_close")}>
            <svg viewBox="0 0 16 16" width="14" height="14" aria-hidden="true"><path d="M4 4l8 8M12 4l-8 8" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" /></svg>
          </DialogClose>
        </div>
        <form class="sheet-body" onSubmit={send}>
          <div class="field">
            <label for="tryit-server">{t("try_server")}</label>
            <input id="tryit-server" value={server()} onInput={(e) => setServer(e.currentTarget.value)} spellcheck={false} />
          </div>
          <For each={params()}>
            {(p) => (
              <div class="field">
                <label for={`tryit-${p.name}`}>
                  <code>{p.in === "header" ? p.header : p.name}</code> <span class="kind">{p.in || t("try_body")}</span>
                </label>
                {p.in === "body" || p.in === "" ? (
                  <textarea id={`tryit-${p.name}`} value={values()[p.name] ?? ""} spellcheck={false}
                    onInput={(e) => setValues({ ...values(), [p.name]: e.currentTarget.value })} />
                ) : (
                  <input id={`tryit-${p.name}`} value={values()[p.name] ?? ""} spellcheck={false} required={p.required}
                    onInput={(e) => setValues({ ...values(), [p.name]: e.currentTarget.value })} />
                )}
              </div>
            )}
          </For>
          <div>
            <button type="submit" class="btn primary" disabled={busy()}>{busy() ? t("try_sending") : t("try_send")}</button>
          </div>
          <Show when={result()}>
            {(r) => (
              <div class="result" aria-live="polite">
                <div class="panel-head"><code class="status">{r().status}</code></div>
                <pre><code>{r().body}</code></pre>
              </div>
            )}
          </Show>
        </form>
      </DialogContent>
    </Dialog>
  )
}
