import { PassThrough } from "node:stream";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, it } from "vitest";
import { Conn, RpcError } from "./conn.js";
import { HeronError, serveExtension, toRpcError } from "./extension.js";
import { serveHost } from "./host.js";
import { codeCancelled, codeThrown, methods, type InitializeParams, type InitializeResult } from "./protocol.gen.js";
import { defineFunc, defineLint, defineResource, defineTransport } from "./sdk.js";
import { Decimal, Duration, Ref, Variant, cbor, json } from "./values.js";

function pair(opts: { wire?: typeof cbor } = {}) {
  const ab = new PassThrough();
  const ba = new PassThrough();
  const a = new Conn(ba, ab, { wire: opts.wire, toError: toRpcError });
  const b = new Conn(ab, ba, { wire: opts.wire, toError: toRpcError });
  return { a, b };
}

const init = (declarations: InitializeParams["declarations"], root = "/"): InitializeParams => ({
  protocol: "1.0",
  host: { name: "test", version: "0" },
  root,
  declarations,
  limits: { max_frame: 1 << 20, max_in_flight: 8, timeout_ms: 5000 },
  capabilities: { methods: [] },
});

describe("values", () => {
  it("round-trip with their Heron types", () => {
    for (const wire of [cbor]) {
      const v = {
        big: 2n ** 70n,
        dec: Decimal.parse("40.00"),
        dur: new Duration(1500),
        ref: new Ref("store.pets"),
        set: new Set(["a"]),
        variant: new Variant("card", { last4: "4242" }),
        bytes: new Uint8Array([1, 2]),
      };
      const back = wire.decode(wire.encode(v)) as typeof v;
      expect(back.big).toBe(2n ** 70n);
      expect(back.dec).toBeInstanceOf(Decimal);
      expect(back.dec.toString()).toBe("40.00");
      expect(back.dur).toEqual(new Duration(1500));
      expect(back.ref).toEqual(new Ref("store.pets"));
      expect(back.set).toEqual(new Set(["a"]));
      expect(back.variant).toEqual(new Variant("card", { last4: "4242" }));
      expect(back.bytes).toEqual(new Uint8Array([1, 2]));
    }
  });
  it("decimals format exactly", () => {
    expect(Decimal.parse("-0.05").toString()).toBe("-0.05");
    expect(new Decimal(12n, 2).toString()).toBe("1200");
  });
});

describe("conn", () => {
  for (const wire of [cbor, json]) {
    it(`calls both ways over ${wire.name}`, async () => {
      const { a, b } = pair({ wire });
      a.handle("ping", (p) => ({ pong: p.n + 1 }));
      b.handle("ping", (p) => ({ pong: p.n * 10 }));
      expect(await a.call("ping", { n: 2 })).toEqual({ pong: 20 });
      expect(await b.call("ping", { n: 2 })).toEqual({ pong: 3 });
    });
  }
  it("sends thrown Heron errors with their type", async () => {
    const { a, b } = pair();
    b.handle("x", () => {
      throw new HeronError("NotFound", "no such pet", { id: 3 });
    });
    await expect(a.call("x", {})).rejects.toMatchObject({ code: codeThrown, type: "NotFound", data: { id: 3 } });
  });
  it("cancels", async () => {
    const { a, b } = pair();
    let aborted = false;
    b.handle("slow", (_p, rc) => new Promise((r) => rc.signal.addEventListener("abort", () => ((aborted = true), r(null)))));
    const ac = new AbortController();
    const p = a.call("slow", {}, { signal: ac.signal });
    setTimeout(() => ac.abort(), 10);
    await expect(p).rejects.toMatchObject({ code: codeCancelled });
    await new Promise((r) => setTimeout(r, 20));
    expect(aborted).toBe(true);
  });
  it("streams", async () => {
    const { a, b } = pair();
    b.handle("count", (_p, rc) => {
      for (let i = 0; i < 3; i++) rc.stream(i);
      return null;
    });
    const got: unknown[] = [];
    for await (const i of a.callStream("count", {})) got.push(i);
    expect(got).toEqual([0, 1, 2]);
  });
  it("fails calls in flight when the peer goes away", async () => {
    const { a } = pair();
    const p = a.call("never", {});
    a.close(new Error("gone"));
    await expect(p).rejects.toBeInstanceOf(RpcError);
  });
});

describe("serveExtension", () => {
  it("handshakes and serves each extension point", async () => {
    const { a, b } = pair();
    serveExtension(b, {
      name: "sample",
      version: "1.0.0",
      onShutdown: () => {},
      resolve: (d) =>
        ({
          "s.slug": defineFunc<{ text: string }, string>(({ text }) => text.toLowerCase().replace(/\W+/g, "-")),
          "s.no_todo": defineLint({ run: (nodes) => nodes.filter((n) => n.name?.includes("todo")).map((n) => ({ node: n.path, message: "todo" })) }),
          "s.store": defineResource<{ items: string[] }>({ setup: () => ({ items: [] }), reset: (i) => void (i.items.length = 0) }),
          "s.fast": defineTransport({ send: async (op, req, ctx) => ({ op: op.path, req, store: await ctx.resource("s.store") }) }),
        })[d.path] ?? null,
    });
    a.handle(methods.node, (p) => ({ path: p.path }));
    let handle: unknown;
    a.handle(methods.resource, () => handle);
    const res = (await a.call(methods.initialize, init([
      { path: "s.slug", point: "func" },
      { path: "s.no_todo", point: "lint" },
      { path: "s.store", point: "resource" },
      { path: "s.fast", point: "transport" },
      { path: "s.other", point: "func" },
    ]))) as InitializeResult;
    expect(res.implements.map((i) => i.path)).toEqual(["s.slug", "s.no_todo", "s.store", "s.fast"]);
    expect(await a.call(methods.call, { target: "s.slug", args: { text: "Hello World" } })).toBe("hello-world");
    expect(await a.call(methods.lint, { lint: "s.no_todo", nodes: [{ path: "a.todo_x", name: "todo_x" }, { path: "b", name: "b" }] })).toEqual({
      findings: [{ node: "a.todo_x", message: "todo", severity: "warning" }],
    });
    handle = ((await a.call(methods.resourceSetup, { resource: "s.store" })) as { handle: unknown }).handle;
    const sent = (await a.call(methods.call, { target: "s.fast", member: "send", args: { op: new Ref("api.get"), request: { id: 1 } } })) as Record<string, unknown>;
    expect(sent).toEqual({ op: "api.get", req: { id: 1 }, store: { items: [] } });
  });
  it("rejects an implementation of the wrong extension point", async () => {
    const { a, b } = pair();
    serveExtension(b, { name: "s", version: "1", resolve: () => defineFunc(() => 1) });
    await expect(a.call(methods.initialize, init([{ path: "x", point: "lint" }]))).rejects.toThrow(/declared as a lint but implemented as a func/);
  });
});

describe("serveHost", () => {
  it("loads the module a declaration names", async () => {
    const dir = mkdtempSync(join(tmpdir(), "heron-host-"));
    writeFileSync(join(dir, "package.json"), "{}");
    writeFileSync(join(dir, "m.mjs"), "export const twice = ({ n }) => n * 2;\n");
    const ab = new PassThrough();
    const ba = new PassThrough();
    const host = serveHost(ab, ba, { onShutdown: () => {} });
    const client = new Conn(ba, ab);
    const res = (await client.call(methods.initialize, init([{ path: "twice", point: "func", module: "./m.mjs", export: "twice" }], dir))) as InitializeResult;
    expect(res.implements).toEqual([{ path: "twice", point: "func" }]);
    expect(await client.call(methods.call, { target: "twice", args: { n: 21 } })).toBe(42);
    void host;
  });
  it("rejects an export that isn't an implementation", async () => {
    const dir = mkdtempSync(join(tmpdir(), "heron-host-"));
    writeFileSync(join(dir, "package.json"), "{}");
    writeFileSync(join(dir, "m.mjs"), "export const answer = 42;\n");
    const ab = new PassThrough();
    const ba = new PassThrough();
    serveHost(ab, ba, { onShutdown: () => {} });
    const client = new Conn(ba, ab);
    await expect(client.call(methods.initialize, init([{ path: "a", point: "lint", module: "./m.mjs", export: "answer" }], dir))).rejects.toThrow(
      /export answer isn't an implementation made with @heron\/runtime/,
    );
  });
  it("serves a renderer module's renderers by kind", async () => {
    const dir = mkdtempSync(join(tmpdir(), "heron-host-"));
    writeFileSync(join(dir, "package.json"), "{}");
    const sdk = JSON.stringify(new URL("./sdk.ts", import.meta.url).href);
    writeFileSync(
      join(dir, "r.mjs"),
      `import { defineRenderer, escapeHtml } from ${sdk};\n` +
        `export default [defineRenderer("g.steps", (n) => "<ol>" + n.children + "</ol>")];\n` +
        `export const badge = defineRenderer("g.badge", (n) => ({ html: "<b data-n=" + n.attrs.n + ">" + n.body + "</b>", block: false }));\n` +
        `export const notOne = escapeHtml;\n`,
    );
    const ab = new PassThrough();
    const ba = new PassThrough();
    serveHost(ab, ba, { onShutdown: () => {} });
    const client = new Conn(ba, ab);
    const res = (await client.call(methods.initialize, init([{ path: "renderers:r", point: "renderer", module: "./r.mjs", export: "*" }], dir))) as InitializeResult;
    expect(res.implements).toEqual([{ path: "renderers:r", point: "renderer", kinds: ["g.badge", "g.steps"] }]);
    expect(await client.call(methods.render, { renderer: "renderers:r", kind: "g.steps", path: "a", children: "<li>x</li>" })).toEqual({ html: "<ol><li>x</li></ol>" });
    expect(await client.call(methods.render, { renderer: "renderers:r", kind: "g.badge", body: "Beta", attrs: { n: 2 } })).toEqual({ html: "<b data-n=2>Beta</b>", block: false });
    await expect(client.call(methods.render, { renderer: "renderers:r", kind: "g.other" })).rejects.toThrow(/no renderer for g.other/);
  });
});
