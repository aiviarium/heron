import { createDialogController, Dialog, DialogClose, DialogContent, DialogTitle } from "@ui-sdk/solid/dialog"
import { createSignal, For, Show } from "solid-js"
import { t } from "../data"
import { ApiError, createClient } from "../../../shell/src/api.gen"

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
  examples?: Record<string, unknown>[]
  /** The op's method on the generated client, when the page's own server publishes it. */
  client?: string
  stream?: boolean
  /** An RPC binding's call: Connect, or JSON-RPC over HTTP. */
  protocol?: "connect" | "jsonrpc"
  server?: string
  /** Each example's request message, or JSON-RPC envelope. */
  requests?: unknown[]
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
  const [server, setServer] = createSignal(op.server ?? localStorage.getItem(serverKey) ?? location.origin)
  const [message, setMessage] = createSignal("")
  const [values, setValues] = createSignal<Record<string, string>>({})
  const [result, setResult] = createSignal<{ status: string; body: string } | undefined>()
  const [busy, setBusy] = createSignal(false)
  const params = () => op.params ?? []

  const fill = () => {
    const ex = op.examples?.[example()] ?? {}
    const v: Record<string, string> = {}
    for (const p of params()) v[p.name] = text(ex[p.name])
    setValues(v)
    setMessage(text(op.requests?.[example()] ?? {}))
    setResult(undefined)
  }
  props.root.querySelector(".picker")?.addEventListener("change", (e) => setExample(Number((e.target as HTMLSelectElement).value)))
  props.root.querySelector("[data-tryit]")?.addEventListener("click", () => {
    fill()
    dialog.open()
  })

  // An op of the API the page's own server publishes goes through the
  // client generated from that API, as the frontend's requests do.
  const sendServed = async () => {
    const client = createClient("") as unknown as Record<string, (...a: unknown[]) => unknown>
    const args = params().map((p) => {
      const v = values()[p.name] ?? ""
      if (v === "") return undefined
      try { return JSON.parse(v) } catch { return v }
    })
    setBusy(true)
    try {
      const out = client[op.client!](...args)
      let body: unknown
      if (op.stream) {
        const events: unknown[] = []
        for await (const ev of out as AsyncIterable<unknown>) {
          events.push(ev)
          if (events.length >= 50) break
        }
        body = events
      } else body = await out
      setResult({ status: "200", body: JSON.stringify(body, null, 2) })
    } catch (err) {
      setResult(err instanceof ApiError ? { status: String(err.status), body: JSON.stringify({ error: err.message }, null, 2) } : { status: t("try_failed"), body: String(err) })
    } finally {
      setBusy(false)
    }
  }

  // An RPC call: the request message as JSON, and each message of the
  // response as it arrives.
  const sendRPC = async () => {
    const base = server().replace(/\/$/, "")
    setBusy(true)
    try {
      if (op.protocol === "jsonrpc") {
        const res = await fetch(base, { method: "POST", headers: { "Content-Type": "application/json" }, body: message() })
        const body = await res.text()
        let shown = body
        try { shown = JSON.stringify(JSON.parse(body), null, 2) } catch { /* not JSON */ }
        setResult({ status: `${res.status} ${res.statusText}`, body: shown })
        return
      }
      if (!op.stream) {
        const res = await fetch(base + op.path, { method: "POST", headers: { "Content-Type": "application/json", "Connect-Protocol-Version": "1" }, body: message() })
        const body = await res.text()
        let shown = body
        try { shown = JSON.stringify(JSON.parse(body), null, 2) } catch { /* not JSON */ }
        setResult({ status: `${res.status} ${res.statusText}`, body: shown })
        return
      }
      // A server stream: enveloped messages, the last flagged end-of-stream.
      const json = new TextEncoder().encode(message())
      const frame = new Uint8Array(5 + json.length)
      new DataView(frame.buffer).setUint32(1, json.length)
      frame.set(json, 5)
      const res = await fetch(base + op.path, { method: "POST", headers: { "Content-Type": "application/connect+json", "Connect-Protocol-Version": "1" }, body: frame })
      const reader = res.body!.getReader()
      const shown: unknown[] = []
      let buf = new Uint8Array(0)
      let status = `${res.status} ${res.statusText}`
      for (;;) {
        const { value, done } = await reader.read()
        if (value) {
          const next = new Uint8Array(buf.length + value.length)
          next.set(buf)
          next.set(value, buf.length)
          buf = next
        }
        while (buf.length >= 5) {
          const len = new DataView(buf.buffer, buf.byteOffset).getUint32(1)
          if (buf.length < 5 + len) break
          const flags = buf[0]
          const body = JSON.parse(new TextDecoder().decode(buf.slice(5, 5 + len)))
          buf = buf.slice(5 + len)
          if (flags & 2) {
            if (body.error) {
              status = body.error.code
              shown.push(body.error)
            }
          } else shown.push(body)
          setResult({ status, body: shown.map((m) => JSON.stringify(m, null, 2)).join("\n") })
        }
        if (done) break
      }
      setResult({ status, body: shown.map((m) => JSON.stringify(m, null, 2)).join("\n") })
    } catch (err) {
      setResult({ status: t("try_failed"), body: String(err) })
    } finally {
      setBusy(false)
    }
  }

  const send = async (e: SubmitEvent) => {
    e.preventDefault()
    if (op.client) return sendServed()
    if (op.protocol) return sendRPC()
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
          <Show when={!op.client}>
            <div class="field">
              <label for="tryit-server">{t("try_server")}</label>
              <input id="tryit-server" value={server()} onInput={(e) => setServer(e.currentTarget.value)} spellcheck={false} />
            </div>
          </Show>
          <Show when={op.protocol}>
            <div class="field">
              <label for="tryit-message">{op.protocol === "jsonrpc" ? t("try_envelope") : t("try_message")}</label>
              <textarea id="tryit-message" value={message()} spellcheck={false} onInput={(e) => setMessage(e.currentTarget.value)} />
            </div>
          </Show>
          <For each={op.protocol ? [] : params()}>
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
