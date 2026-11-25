// Serves implementations over a connection: the handshake, calls, lints,
// generation and the resource lifecycle. The JS host and a standalone
// extension written with the SDK both serve through this.
import { join } from "node:path";
import { Conn, RpcError, type RequestContext } from "./conn.js";
import {
  codeForeignError,
  codeInvalidParams,
  codeThrown,
  methods,
  version as protocolVersion,
  type CallParams,
  type Declaration,
  type GenerateParams,
  type Implemented,
  type InitializeParams,
  type InitializeResult,
  type LintParams,
  type Node,
  type RenderParams,
  type RenderResult,
  type ResourceParams,
  type ResourceResult,
} from "./protocol.gen.js";
import type { Ctx, Impl, ResourceImpl } from "./sdk.js";

/** An error a declaration throws, as `@throws` types; anything else thrown becomes ForeignError. */
export class HeronError extends Error {
  constructor(
    readonly type: string,
    message: string,
    readonly value: Record<string, unknown> = {},
  ) {
    super(message);
    this.name = type;
  }
}

export function toRpcError(e: unknown): RpcError {
  if (e instanceof RpcError) return e;
  if (e instanceof HeronError) return new RpcError(codeThrown, e.message, e.type, { message: e.message, ...e.value });
  const msg = e instanceof Error ? e.message : String(e);
  return new RpcError(codeForeignError, msg, "ForeignError", { message: msg, stack: e instanceof Error ? e.stack : undefined });
}

const HANDLE = "$heron_handle";

export interface ServeOptions {
  name: string;
  version: string;
  /** Finds the implementation of each declaration, or null when this side has none. */
  resolve(decl: Declaration, init: InitializeParams): Promise<Impl | null> | Impl | null;
  /** Called after shutdown is answered; exits the process by default. */
  onShutdown?: () => void;
}

/** Serves the Extension methods on a connection. */
export function serveExtension(conn: Conn, opts: ServeOptions): void {
  const impls = new Map<string, Impl>();
  const decls = new Map<string, Declaration>();
  const handles = new Map<number, unknown>();
  let nextHandle = 0;
  let init: InitializeParams | undefined;

  const ctxFor = (rc: RequestContext): Ctx => ({
    run: rc.run,
    signal: rc.signal,
    node: (path) => conn.call(methods.node, { path }, { signal: rc.signal, run: rc.run }),
    call: (target, args = {}) => conn.call(methods.call, { target, args }, { signal: rc.signal, run: rc.run }),
    resource: async (name) => {
      const v = await conn.call(methods.resource, { name }, { signal: rc.signal, run: rc.run });
      return unhandle(v) as any;
    },
    secret: (name) => conn.call(methods.secret, { name }, { signal: rc.signal, run: rc.run }),
    temp: (name) => join(init?.temp ?? "/tmp/heron", rc.run ?? "local", name),
    log: (message, level = "info") => conn.notify(methods.log, { level, message }),
    progress: (message, done, total) => conn.notify(methods.progress, { id: 0, message, done, total }),
  });

  const unhandle = (v: unknown): unknown => {
    if (v && typeof v === "object" && HANDLE in v) {
      const id = Number((v as Record<string, unknown>)[HANDLE]);
      if (handles.has(id)) return handles.get(id);
    }
    return v;
  };

  const impl = <P extends Impl["point"]>(path: string, point: P): Extract<Impl, { point: P }> => {
    const i = impls.get(path);
    if (!i || i.point !== point) throw new RpcError(codeInvalidParams, `${path} isn't a ${point} this extension implements`);
    return i as Extract<Impl, { point: P }>;
  };

  conn.handle(methods.initialize, async (p: InitializeParams): Promise<InitializeResult> => {
    init = p;
    if (p.limits) conn.limits = p.limits;
    const implemented: Implemented[] = [];
    for (const d of p.declarations ?? []) {
      const i = await opts.resolve(d, p);
      if (!i) continue;
      if (i.point !== d.point) {
        throw new RpcError(codeInvalidParams, `${d.path} is declared as a ${d.point} but implemented as a ${i.point}`);
      }
      if (i.path && i.path !== d.path) {
        throw new RpcError(codeInvalidParams, `${d.path} is bound to the implementation generated for ${i.path}`);
      }
      impls.set(d.path, i);
      decls.set(d.path, d);
      implemented.push(i.point === "renderer" ? { path: d.path, point: d.point, kinds: [...i.renderers.keys()].sort() } : { path: d.path, point: d.point });
    }
    return {
      protocol: protocolVersion,
      extension: { name: opts.name, version: opts.version },
      implements: implemented,
      capabilities: {
        methods: Object.values(methods).filter((m) => !m.startsWith("$/")),
        streaming: true,
        cancellation: true,
      },
    };
  });

  conn.handle(methods.call, async (p: CallParams, rc) => {
    const ctx = ctxFor(rc);
    const i = impls.get(p.target);
    if (!i) throw new RpcError(codeInvalidParams, `${p.target} isn't implemented here`);
    switch (i.point) {
      case "func": {
        const out = await i.fn(p.args ?? {}, ctx);
        if (out && typeof out === "object" && Symbol.asyncIterator in out) {
          for await (const item of out as AsyncIterable<unknown>) rc.stream(item);
          return null;
        }
        return out;
      }
      case "macro":
        return i.expand(p.args ?? {}, ctx);
      case "transport": {
        if ((p.member ?? "send") !== "send") throw new RpcError(codeInvalidParams, `a transport has no member ${p.member}`);
        const op = p.args.op as { path?: string } | string;
        const path = typeof op === "string" ? op : (op?.path ?? String(op));
        const node = (await ctx.node(path)) ?? { path };
        return i.send(node, p.args.request, ctx);
      }
      default:
        throw new RpcError(codeInvalidParams, `${p.target} is a ${i.point}, which isn't called`);
    }
  });

  conn.handle(methods.lint, async (p: LintParams, rc) => {
    const l = impl(p.lint, "lint");
    const findings = await l.run(p.nodes as Node[], ctxFor(rc));
    return { findings: findings.map((f) => ({ severity: "warning", ...f })) };
  });

  conn.handle(methods.render, async (p: RenderParams, rc): Promise<RenderResult> => {
    const m = impl(p.renderer, "renderer");
    const r = m.renderers.get(p.kind);
    if (!r) throw new RpcError(codeInvalidParams, `${p.renderer} has no renderer for ${p.kind}`);
    const { renderer: _, ...node } = p;
    const out = await r.render(node, ctxFor(rc));
    return typeof out === "string" ? { html: out } : out;
  });

  conn.handle(methods.generate, async (p: GenerateParams, rc) => {
    const g = impl(p.generator, "generator");
    return { files: await g.generate(p.tree, ctxFor(rc)) };
  });

  const lifecycle = (
    name: "setup" | "reset" | "snapshot" | "restore" | "fork" | "teardown",
  ) => async (p: ResourceParams, rc: RequestContext): Promise<ResourceResult> => {
    const r = impl(p.resource, "resource") as ResourceImpl<unknown, unknown>;
    const ctx = ctxFor(rc);
    const keep = (instance: unknown) => {
      const id = ++nextHandle;
      handles.set(id, instance);
      return { [HANDLE]: id };
    };
    const instance = unhandle(p.handle);
    switch (name) {
      case "setup":
        return { handle: keep(await r.setup(ctx)) };
      case "fork":
        if (!r.fork) throw new RpcError(codeInvalidParams, `${p.resource} can't fork`);
        return { handle: keep(await r.fork(instance, ctx)) };
      case "snapshot":
        if (!r.snapshot) throw new RpcError(codeInvalidParams, `${p.resource} can't snapshot`);
        return { snapshot: await r.snapshot(instance, ctx) };
      case "restore":
        if (!r.restore) throw new RpcError(codeInvalidParams, `${p.resource} can't restore`);
        await r.restore(instance, p.snapshot, ctx);
        return { handle: p.handle };
      case "reset":
        if (r.reset) await r.reset(instance, ctx);
        return { handle: p.handle };
      case "teardown":
        if (r.teardown) await r.teardown(instance, ctx);
        if (p.handle && typeof p.handle === "object" && HANDLE in p.handle) {
          handles.delete(Number((p.handle as Record<string, unknown>)[HANDLE]));
        }
        return {};
    }
  };
  conn.handle(methods.resourceSetup, lifecycle("setup"));
  conn.handle(methods.resourceReset, lifecycle("reset"));
  conn.handle(methods.resourceSnapshot, lifecycle("snapshot"));
  conn.handle(methods.resourceRestore, lifecycle("restore"));
  conn.handle(methods.resourceFork, lifecycle("fork"));
  conn.handle(methods.resourceTeardown, lifecycle("teardown"));

  conn.handle(methods.shutdown, async () => {
    setImmediate(opts.onShutdown ?? (() => process.exit(0)));
    return {};
  });
}
