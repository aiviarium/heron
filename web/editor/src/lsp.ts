// A language-server client over the server's /lsp socket. It reconnects
// with backoff, re-initializes and re-opens every synced document, and its
// status is always one of connecting, connected, reconnecting (with the
// reason) or failed (with the reason): it never stays on connecting.
import { createSignal } from "solid-js";
import { t } from "@heron/strings";

export type LspState = "connecting" | "connected" | "reconnecting" | "failed";

export interface LspStatus {
  state: LspState;
  reason?: string;
  attempt: number;
}

export interface Doc {
  uri: string;
  languageId: string;
  version: number;
  text: () => string;
}

export interface TextChange {
  range?: { start: { line: number; character: number }; end: { line: number; character: number } };
  text: string;
}

type Handler = (params: any) => void;

interface Pending {
  resolve: (v: any) => void;
  reject: (e: Error) => void;
  timer: ReturnType<typeof setTimeout>;
}

const MAX_ATTEMPTS = 8;
const TIMEOUT_MS = 15000;

export class LspClient {
  readonly status: () => LspStatus;
  private setStatus: (s: LspStatus) => void;
  /** The server's capabilities from the last initialize. */
  capabilities: any = {};
  private ws: WebSocket | null = null;
  private nextId = 1;
  private pending = new Map<number, Pending>();
  private handlers = new Map<string, Handler[]>();
  private docs = new Map<string, Doc>();
  private ready = false;
  private attempt = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;
  private readyWaiters: (() => void)[] = [];
  private lostWaiters: (() => void)[] = [];

  constructor(
    private url: string,
    private rootUri: string,
  ) {
    const [status, setStatus] = createSignal<LspStatus>({ state: "connecting", attempt: 0 }, { ownedWrite: true });
    this.status = status;
    this.setStatus = setStatus;
  }

  start() {
    this.stopped = false;
    this.connect();
  }

  stop() {
    this.stopped = true;
    if (this.timer) clearTimeout(this.timer);
    this.ws?.close();
  }

  /** Reconnects now, after a failure or on request. */
  retry() {
    if (this.timer) clearTimeout(this.timer);
    this.attempt = 0;
    this.ws?.close();
    this.connect();
  }

  on(method: string, h: Handler) {
    this.handlers.set(method, [...(this.handlers.get(method) ?? []), h]);
  }

  private connect() {
    if (this.stopped) return;
    this.ready = false;
    this.setStatus({ state: this.attempt === 0 ? "connecting" : "reconnecting", reason: this.status().reason, attempt: this.attempt });
    let opened = false;
    let ws: WebSocket;
    try {
      ws = new WebSocket(new URL(this.url, location.href).href.replace(/^http/, "ws"));
    } catch (e) {
      this.lost(String(e));
      return;
    }
    this.ws = ws;
    ws.onopen = () => {
      opened = true;
      void this.initialize(ws);
    };
    ws.onmessage = (ev) => this.receive(String(ev.data));
    ws.onclose = (ev) => {
      if (this.ws !== ws) return;
      this.ws = null;
      const reason = ev.reason || (opened ? `${t("app.lsp.closed")} (${ev.code})` : t("app.lsp.unavailable"));
      this.lost(reason);
    };
    ws.onerror = () => {
      /* onclose follows with the reason */
    };
  }

  private lost(reason: string) {
    this.ready = false;
    for (const w of this.lostWaiters.splice(0)) w();
    for (const [, p] of this.pending) (clearTimeout(p.timer), p.reject(new Error(reason)));
    this.pending.clear();
    if (this.stopped) return;
    this.attempt++;
    if (this.attempt > MAX_ATTEMPTS) {
      this.setStatus({ state: "failed", reason, attempt: this.attempt });
      return;
    }
    this.setStatus({ state: "reconnecting", reason, attempt: this.attempt });
    const delay = Math.min(250 * 2 ** (this.attempt - 1), 5000);
    this.timer = setTimeout(() => this.connect(), delay);
  }

  private async initialize(ws: WebSocket) {
    try {
      const res = await this.request("initialize", {
        processId: null,
        rootUri: this.rootUri,
        workspaceFolders: [{ uri: this.rootUri, name: "workspace" }],
        capabilities: {
          textDocument: {
            synchronization: { didSave: true, dynamicRegistration: false },
            publishDiagnostics: { relatedInformation: true, codeDescriptionSupport: true },
            completion: { completionItem: { snippetSupport: true, documentationFormat: ["markdown", "plaintext"] }, contextSupport: true },
            hover: { contentFormat: ["markdown", "plaintext"] },
            definition: { linkSupport: true },
            rename: { prepareSupport: true },
            codeAction: { codeActionLiteralSupport: { codeActionKind: { valueSet: ["", "quickfix", "refactor", "source"] } } },
            formatting: {},
            documentSymbol: { hierarchicalDocumentSymbolSupport: true },
            semanticTokens: {
              requests: { full: true },
              tokenTypes: [],
              tokenModifiers: [],
              formats: ["relative"],
            },
          },
          workspace: { workspaceFolders: true, configuration: true, applyEdit: true },
        },
      }, ws);
      this.capabilities = res?.capabilities ?? {};
      this.notify("initialized", {});
      this.ready = true;
      this.attempt = 0;
      for (const d of this.docs.values()) this.sendOpen(d);
      this.setStatus({ state: "connected", attempt: 0 });
      for (const w of this.readyWaiters.splice(0)) w();
    } catch (e) {
      ws.close(4000, String((e as Error).message ?? e).slice(0, 100));
    }
  }

  /** Resolves once the client is connected and initialized. */
  whenReady(): Promise<void> {
    return this.ready ? Promise.resolve() : new Promise((r) => this.readyWaiters.push(r));
  }

  /** Resolves when the connection is lost. */
  whenLost(): Promise<void> {
    return new Promise((r) => this.lostWaiters.push(r));
  }

  private receive(text: string) {
    let msg: any;
    try {
      msg = JSON.parse(text);
    } catch {
      return;
    }
    if (msg.id !== undefined && msg.method === undefined) {
      const p = this.pending.get(msg.id);
      if (!p) return;
      this.pending.delete(msg.id);
      clearTimeout(p.timer);
      if (msg.error) p.reject(new Error(msg.error.message));
      else p.resolve(msg.result);
      return;
    }
    if (msg.id !== undefined) {
      // A request from the server: answer what a browser editor can.
      let result: unknown = null;
      if (msg.method === "workspace/configuration") result = (msg.params?.items ?? []).map(() => null);
      if (msg.method === "workspace/applyEdit") {
        for (const h of this.handlers.get("workspace/applyEdit") ?? []) h(msg.params);
        result = { applied: true };
      }
      this.send({ jsonrpc: "2.0", id: msg.id, result });
      return;
    }
    for (const h of this.handlers.get(msg.method) ?? []) h(msg.params);
  }

  private send(msg: unknown, ws = this.ws) {
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(msg));
  }

  request<T = any>(method: string, params: unknown, ws = this.ws): Promise<T> {
    if (!ws || ws.readyState !== WebSocket.OPEN || (!this.ready && method !== "initialize")) {
      return Promise.reject(new Error(t("app.lsp.unavailable")));
    }
    const id = this.nextId++;
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        this.send({ jsonrpc: "2.0", method: "$/cancelRequest", params: { id } });
        reject(new Error("timeout"));
      }, TIMEOUT_MS);
      this.pending.set(id, { resolve, reject, timer });
      this.send({ jsonrpc: "2.0", id, method, params }, ws);
    });
  }

  notify(method: string, params: unknown) {
    this.send({ jsonrpc: "2.0", method, params });
  }

  private sendOpen(d: Doc) {
    this.notify("textDocument/didOpen", { textDocument: { uri: d.uri, languageId: d.languageId, version: d.version, text: d.text() } });
  }

  /** Starts syncing a document; it's re-opened after every reconnect. */
  open(d: Doc) {
    this.docs.set(d.uri, d);
    if (this.ready) this.sendOpen(d);
  }

  /** Sends a change: incremental ranges when the server takes them, else the text. */
  change(uri: string, version: number, changes: TextChange[]) {
    const d = this.docs.get(uri);
    if (!d) return;
    d.version = version;
    if (!this.ready) return;
    const sync = this.capabilities.textDocumentSync;
    const kind = typeof sync === "number" ? sync : sync?.change;
    const contentChanges = kind === 2 && changes.every((c) => c.range) ? changes : [{ text: d.text() }];
    this.notify("textDocument/didChange", { textDocument: { uri, version }, contentChanges });
  }

  save(uri: string, text: string) {
    if (this.ready) this.notify("textDocument/didSave", { textDocument: { uri }, text });
  }

  close(uri: string) {
    if (!this.docs.delete(uri)) return;
    if (this.ready) this.notify("textDocument/didClose", { textDocument: { uri } });
  }

  isOpen(uri: string) {
    return this.docs.has(uri);
  }
}
