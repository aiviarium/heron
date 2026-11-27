// One side of the extension protocol: a Sparrow session on the pipe binding
// over a pair of streams. The extension's side publishes heron.extension and
// is the Sparrow client; heron's side publishes heron.host. Calls both ways,
// cancellation, deadlines, progress and streamed results are Sparrow's own.
// The Go side is internal/core/extension/protocol.
import type { Readable, Writable } from "node:stream";
import { Router, Session, SparrowError, cbor as spcbor, makeSchema, schema as spschema, type CallContext, type Facet, type Method, type TypeRef } from "@aiviarium/sparrow";
import { pipeConn } from "@aiviarium/sparrow/pipe";
import {
  codeCancelled,
  codeForeignError,
  codeInternalError,
  codeInvalidParams,
  codeInvalidRequest,
  codeLimitExceeded,
  codeMethodNotFound,
  codeParseError,
  codeThrown,
  codeTimeout,
  facetExtensionHex,
  facetHostHex,
  facets,
  version,
  type Limits,
} from "./protocol.gen.js";
import { Binder } from "./values.js";

export const defaultLimits: Limits = { max_message: 16 << 20, max_in_flight: 64, timeout_ms: 30_000 };

/** The metadata key that carries a run's id. */
export const runKey = "heron.run";

/** An error answer, or what a handler throws to send one with a code. */
export class RpcError extends Error {
  constructor(
    readonly code: number,
    message: string,
    readonly type?: string,
    readonly data?: unknown,
  ) {
    super(message);
    this.name = type ?? "RpcError";
  }
}

/** What a handler is called with besides its params. */
export interface RequestContext {
  /** Aborted when the caller cancels, the deadline passes or the session ends. */
  signal: AbortSignal;
  /** The run the call belongs to. */
  run?: string;
  /** Sends one item of a streamed result, for a method whose result is a Stream. */
  stream(item: unknown): Promise<void>;
  /** Reports progress to the caller as Sparrow's Progress. */
  progress(done: number, total?: number, note?: string): Promise<void>;
}

export type Handler = (params: any, rc: RequestContext) => unknown | Promise<unknown>;

/** Which side of the protocol a connection is. */
export type Role = "extension" | "host";

export interface ConnOptions {
  /** The extension's side, the default, or heron's. */
  role?: Role;
  limits?: Limits;
  handlers?: Record<string, Handler>;
  notifications?: Record<string, (params: any) => void>;
  /** Turns an exception that isn't an RpcError into one. */
  toError?: (e: unknown) => RpcError;
}

// The Sparrow status of each code, from the protocol's errors table; a
// status without details reads back as the first code that has it.
const codeStatus: [number, string][] = [
  [codeParseError, "protocol_error"],
  [codeInvalidParams, "invalid_argument"],
  [codeInvalidRequest, "invalid_argument"],
  [codeMethodNotFound, "unimplemented"],
  [codeInternalError, "internal"],
  [codeCancelled, "cancelled"],
  [codeForeignError, "unknown"],
  [codeThrown, "aborted"],
  [codeLimitExceeded, "resource_exhausted"],
  [codeLimitExceeded, "message_too_large"],
  [codeTimeout, "deadline_exceeded"],
];
const statusOf = (code: number) => codeStatus.find(([c]) => c === code)?.[1] ?? "unknown";
const codeOf = (status: string) => codeStatus.find(([, s]) => s === status)?.[0] ?? codeInternalError;

/** The facet heron.extension or heron.host, as the messages define it. */
export function facetDef(name: string): Facet {
  const hex = name === facets.extension ? facetExtensionHex : name === facets.host ? facetHostHex : undefined;
  if (!hex) throw new Error(`no facet ${name}`);
  return spschema.decodeFacet(spcbor.fromHex(hex));
}

function methodOf(f: Facet, name: string): Method | undefined {
  for (const s of f.services) for (const m of s.methods) if (m.name === name) return m;
  return undefined;
}

const isStream = (t: TypeRef): t is [3, TypeRef] => Array.isArray(t) && t[0] === 3;

export class Conn {
  readonly role: Role;
  limits: Limits;
  /** Settles once the handshake is done; a failed handshake ends the connection. */
  readonly ready: Promise<void>;
  readonly closed: Promise<void>;
  private handlers: Record<string, Handler>;
  private notifications: Record<string, (params: any) => void>;
  private toError: (e: unknown) => RpcError;
  private local: Facet;
  private lb: Binder;
  private peer?: Facet;
  private pb?: Binder;
  private session?: Session;
  private closedWith: Error | undefined;
  private markClosed!: () => void;
  private inFlight = new Set<(e: unknown) => void>();

  constructor(input: Readable, output: Writable, opts: ConnOptions = {}) {
    this.role = opts.role ?? "extension";
    this.limits = opts.limits ?? defaultLimits;
    this.handlers = opts.handlers ?? {};
    this.notifications = opts.notifications ?? {};
    this.toError =
      opts.toError ??
      ((e) => (e instanceof RpcError ? e : new RpcError(codeInternalError, e instanceof Error ? e.message : String(e))));
    this.closed = new Promise((r) => (this.markClosed = r));
    const [localName, peerName] = this.role === "extension" ? [facets.extension, facets.host] : [facets.host, facets.extension];
    this.local = facetDef(localName);
    this.lb = new Binder(this.local);
    const router = new Router().fallback((input: unknown, ctx: CallContext) => this.serve(input, ctx));
    const sopts = {
      schema: makeSchema(localName, version, [this.local]),
      handlers: router,
      params: { maxMessageSize: Math.max(this.limits.max_message, 16384), maxConcurrentCalls: Math.max(this.limits.max_in_flight, 1) },
      maxClientSchemaSize: -1,
    };
    const tc = pipeConn(input, output);
    const start = this.role === "extension" ? Session.connect(tc, sopts) : Session.accept(tc, sopts);
    this.ready = start.then(
      (s) => {
        this.session = s;
        this.peer = s.peerFacet(peerName) ?? undefined;
        if (!this.peer) {
          s.close();
          throw new RpcError(codeForeignError, `the peer doesn't publish ${peerName}`);
        }
        this.pb = new Binder(this.peer);
        void s.closed.then((info) => this.close(new Error(info.reason || "the connection closed")));
        if (this.closedWith) s.close();
      },
      (e) => {
        this.close(e instanceof Error ? e : new Error(String(e)));
        throw e;
      },
    );
    this.ready.catch(() => {});
  }

  handle(method: string, h: Handler): void {
    this.handlers[method] = h;
  }

  onNotification(method: string, f: (params: any) => void): void {
    this.notifications[method] = f;
  }

  private async peerMethod(method: string): Promise<Method> {
    if (this.closedWith) throw new RpcError(codeForeignError, this.closedWith.message);
    await this.ready;
    const m = methodOf(this.peer!, method);
    if (!m) throw new RpcError(codeMethodNotFound, `method ${JSON.stringify(method)} isn't served`);
    return m;
  }

  private callOpts(opts: { signal?: AbortSignal; run?: string }) {
    return {
      signal: opts.signal,
      timeoutMs: this.limits.timeout_ms > 0 ? this.limits.timeout_ms : undefined,
      metadata: opts.run ? { [runKey]: opts.run } : undefined,
    };
  }

  /** Tracks a call so close fails it at once, whatever the session does. */
  private track<R>(p: Promise<R>): Promise<R> {
    return new Promise<R>((resolve, reject) => {
      const fail = (e: unknown) => reject(e);
      this.inFlight.add(fail);
      p.then(resolve, reject).finally(() => this.inFlight.delete(fail));
    });
  }

  /** Calls a method of the peer's facet and resolves with its result. */
  call<R = unknown>(method: string, params: unknown, opts: { signal?: AbortSignal; run?: string; onItem?: (item: unknown) => void } = {}): Promise<R> {
    return this.track(
      (async () => {
        const m = await this.peerMethod(method);
        if (isStream(m.output)) {
          const items: unknown[] = [];
          for await (const i of this.callStream(method, params, opts)) {
            items.push(i);
            opts.onItem?.(i);
          }
          return items as R;
        }
        try {
          const out = await this.session!.call(this.peer!.id, m.id, this.pb!.toWire(m.input, params ?? {}), this.callOpts(opts));
          return this.pb!.fromWire(m.output, out) as R;
        } catch (e) {
          throw this.failure(method, e, opts.signal);
        }
      })(),
    );
  }

  /** Calls a method whose result streams, yielding each item. */
  async *callStream(method: string, params: unknown, opts: { signal?: AbortSignal; run?: string } = {}): AsyncIterable<unknown> {
    const m = await this.peerMethod(method);
    if (!isStream(m.output)) {
      yield await this.call(method, params, opts);
      return;
    }
    const elem = m.output[1];
    try {
      for await (const item of this.session!.stream(this.peer!.id, m.id, this.pb!.toWire(m.input, params ?? {}), this.callOpts(opts))) {
        yield this.pb!.fromWire(elem, item);
      }
    } catch (e) {
      throw this.failure(method, e, opts.signal);
    }
  }

  /** Sends a notification the peer's facet declares, such as log. */
  notify(method: string, params: unknown): void {
    if (this.closedWith) return;
    void this.peerMethod(method).then(
      (m) => this.session!.notify(this.peer!.id, m.id, this.pb!.toWire(m.input, params)),
      () => {},
    );
  }

  /** A failed call as an RpcError with the protocol's code. */
  private failure(method: string, e: unknown, signal?: AbortSignal): RpcError {
    if (e instanceof RpcError) return e;
    if (signal?.aborted) return new RpcError(codeCancelled, `${method} was cancelled`);
    if (!(e instanceof SparrowError)) return this.toError(e);
    const d = e.details as { typeName?: string; value?: unknown } | undefined;
    if (d && d.typeName === "ErrorDetails") {
      const b = this.pb ?? this.lb;
      const id = b.typeNamed("ErrorDetails")!;
      const ed = b.fromWire(id, d.value) as { code: number; type?: string; data?: unknown };
      return new RpcError(Number(ed.code), e.rawMessage, ed.type, ed.data);
    }
    if (e.code === "deadline_exceeded") return new RpcError(codeTimeout, `${method} timed out`);
    if (e.code === "unavailable" || e.code === "going_away") return new RpcError(codeForeignError, this.closedWith?.message ?? e.rawMessage);
    return new RpcError(codeOf(e.code), e.rawMessage);
  }

  /** A handler's failure as Sparrow's Error, with ErrorDetails. */
  private sparrowError(e: unknown): SparrowError {
    const err = this.toError(e);
    const id = this.lb.typeNamed("ErrorDetails")!;
    const value = this.lb.toWire(id, { code: err.code, type: err.type, data: err.data });
    return new SparrowError(statusOf(err.code), err.message, { type: id, value });
  }

  private async serve(input: unknown, ctx: CallContext): Promise<unknown> {
    const m = ctx.method;
    const params = this.lb.fromWire(m.input, input);
    if (ctx.notification) {
      this.notifications[m.name]?.(params);
      return undefined;
    }
    const h = this.handlers[m.name];
    if (!h) throw this.sparrowError(new RpcError(codeMethodNotFound, `method ${JSON.stringify(m.name)} isn't served`));
    const run = ctx.metadata.get(runKey);
    const out = m.output;
    const rc: RequestContext = {
      signal: ctx.signal,
      run: typeof run === "string" ? run : undefined,
      stream: (item) => {
        if (!isStream(out)) return Promise.reject(new Error(`${m.name} doesn't stream its result`));
        return ctx.send(this.lb.toWire(out[1], item));
      },
      progress: (done, total, note) => ctx.progress(done, total ?? null, note ?? null),
    };
    let result: unknown;
    try {
      result = await h(params, rc);
    } catch (e) {
      throw this.sparrowError(e);
    }
    if (ctx.signal.aborted) throw this.sparrowError(new RpcError(codeCancelled, "cancelled"));
    if (isStream(out)) return undefined;
    try {
      return this.lb.toWire(out, result ?? null);
    } catch (e) {
      throw this.sparrowError(new RpcError(codeInternalError, `result: ${e instanceof Error ? e.message : String(e)}`));
    }
  }

  close(e: Error): void {
    if (this.closedWith) return;
    this.closedWith = e;
    for (const fail of this.inFlight) fail(new RpcError(codeForeignError, e.message));
    this.inFlight.clear();
    this.session?.close();
    this.markClosed();
  }
}
