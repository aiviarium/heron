// The language client's reconnect: a lost socket backs off, reconnects,
// re-initializes and re-opens every synced document; after the last
// attempt it reports failed with the reason, and a retry starts over.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { flush } from "solid-js";
import { LspClient } from "../../editor/src/lsp.ts";

class FakeSocket {
  static OPEN = 1;
  static all: FakeSocket[] = [];
  readyState = 0;
  sent: any[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: string }) => void) | null = null;
  onclose: ((ev: { code: number; reason: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(readonly url: string) {
    FakeSocket.all.push(this);
  }
  send(s: string) {
    const m = JSON.parse(s);
    this.sent.push(m);
    if (m.method === "initialize") queueMicrotask(() => this.onmessage?.({ data: JSON.stringify({ jsonrpc: "2.0", id: m.id, result: { capabilities: {} } }) }));
  }
  open() {
    this.readyState = 1;
    this.onopen?.();
  }
  close(code = 1006, reason = "") {
    this.readyState = 3;
    this.onclose?.({ code, reason });
  }
}

const last = () => FakeSocket.all[FakeSocket.all.length - 1];
const settle = async () => {
  for (let i = 0; i < 5; i++) await Promise.resolve();
};

describe("language client", () => {
  beforeEach(() => {
    FakeSocket.all = [];
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", FakeSocket);
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it("reconnects with backoff and re-opens every synced document", async () => {
    const c = new LspClient("/api/v1/lsp", "file:///w");
    c.open({ uri: "file:///w/index.heron", languageId: "heron", version: 3, text: () => "a = 1\n" });
    c.start();
    expect((flush(), c.status()).state).toBe("connecting");
    last().open();
    await settle();
    expect((flush(), c.status()).state).toBe("connected");
    expect(last().sent.map((m) => m.method)).toEqual(["initialize", "initialized", "textDocument/didOpen"]);

    last().close(1006, "gone");
    expect((flush(), c.status())).toMatchObject({ state: "reconnecting", reason: "gone", attempt: 1 });
    vi.advanceTimersByTime(249);
    expect(FakeSocket.all.length).toBe(1);
    vi.advanceTimersByTime(1);
    expect(FakeSocket.all.length).toBe(2);
    last().open();
    await settle();
    expect((flush(), c.status()).state).toBe("connected");
    const reopened = last().sent.find((m) => m.method === "textDocument/didOpen");
    expect(reopened.params.textDocument).toMatchObject({ uri: "file:///w/index.heron", version: 3, text: "a = 1\n" });
  });

  it("cancels a request with no answer after 15 s", async () => {
    const c = new LspClient("/api/v1/lsp", "file:///w");
    c.start();
    last().open();
    await settle();
    const res = c.request("textDocument/hover", {}).then(() => "answered", (e) => String(e.message));
    const id = last().sent.find((m) => m.method === "textDocument/hover").id;
    vi.advanceTimersByTime(15000);
    expect(await res).toBe("timeout");
    expect(last().sent.at(-1)).toMatchObject({ method: "$/cancelRequest", params: { id } });
  });

  it("fails with the reason after the last attempt, and a retry starts over", () => {
    const c = new LspClient("/api/v1/lsp", "file:///w");
    c.start();
    for (let i = 0; i < 8; i++) {
      last().close(1006, "refused");
      vi.advanceTimersByTime(5000);
    }
    last().close(1006, "refused");
    expect((flush(), c.status())).toMatchObject({ state: "failed", reason: "refused" });
    const n = FakeSocket.all.length;
    vi.advanceTimersByTime(60000);
    expect(FakeSocket.all.length).toBe(n);
    c.retry();
    expect(FakeSocket.all.length).toBe(n + 1);
    expect((flush(), c.status()).state).toBe("connecting");
  });
});
