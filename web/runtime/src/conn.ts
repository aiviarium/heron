// One side of a protocol connection: length-prefixed frames over a pair of
// streams, requests both ways, cancellation with $/cancel and streamed
// results with $/stream. The Go side is internal/core/extension/protocol.
import type { Readable, Writable } from "node:stream";
import {
  codeCancelled,
  codeForeignError,
  codeInternalError,
  codeLimitExceeded,
  codeMethodNotFound,
  codeTimeout,
  methods,
  type ErrorObject,
  type Limits,
  type Message,
} from "./protocol.gen.js";
import { cbor, type Wire } from "./values.js";

export const defaultLimits: Limits = { max_frame: 16 << 20, max_in_flight: 64, timeout_ms: 30_000 };

/** An error response, or what a handler throws to send one with a code. */
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
  /** Aborted when the caller sends $/cancel or the connection ends. */
  signal: AbortSignal;
  /** The run the request belongs to. */
  run?: string;
  /** Sends one item of a streamed result. */
  stream(item: unknown): void;
}

export type Handler = (params: any, rc: RequestContext) => unknown | Promise<unknown>;

export interface ConnOptions {
  wire?: Wire;
  limits?: Limits;
  handlers?: Record<string, Handler>;
  notifications?: Record<string, (params: any) => void>;
  /** Turns an exception that isn't an RpcError into one. */
  toError?: (e: unknown) => RpcError;
}

interface Pending {
  resolve(v: unknown): void;
  reject(e: unknown): void;
  onItem?: (item: unknown) => void;
}

/** Removes undefined members, which have no Heron value, from plain objects. */
function clean(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(clean);
  if (v && typeof v === "object" && Object.getPrototypeOf(v) === Object.prototype) {
    const out: Record<string, unknown> = {};
    for (const [k, x] of Object.entries(v)) if (x !== undefined) out[k] = clean(x);
    return out;
  }
  return v;
}

export class Conn {
  readonly wire: Wire;
  limits: Limits;
  private handlers: Record<string, Handler>;
  private notifications: Record<string, (params: any) => void>;
  private toError: (e: unknown) => RpcError;
  private nextId = 0;
  private pending = new Map<number, Pending>();
  private serving = new Map<number, AbortController>();
  private buf: Buffer = Buffer.alloc(0);
  private closedWith: Error | undefined;
  readonly closed: Promise<void>;
  private markClosed!: () => void;

  constructor(
    private input: Readable,
    private output: Writable,
    opts: ConnOptions = {},
  ) {
    this.wire = opts.wire ?? cbor;
    this.limits = opts.limits ?? defaultLimits;
    this.handlers = opts.handlers ?? {};
    this.notifications = opts.notifications ?? {};
    this.toError =
      opts.toError ??
      ((e) => (e instanceof RpcError ? e : new RpcError(codeInternalError, e instanceof Error ? e.message : String(e))));
    this.closed = new Promise((r) => (this.markClosed = r));
    input.on("data", (chunk: Buffer) => this.onData(chunk));
    input.on("end", () => this.close(new Error("the connection closed")));
    input.on("error", (e) => this.close(e));
  }

  handle(method: string, h: Handler): void {
    this.handlers[method] = h;
  }

  onNotification(method: string, f: (params: any) => void): void {
    this.notifications[method] = f;
  }

  private write(m: Message): void {
    const payload = this.wire.encode(clean(m));
    if (this.limits.max_frame > 0 && payload.length > this.limits.max_frame) {
      throw new RpcError(codeLimitExceeded, `frame of ${payload.length} bytes is over the limit of ${this.limits.max_frame}`);
    }
    const hdr = Buffer.alloc(4);
    hdr.writeUInt32BE(payload.length, 0);
    this.output.write(Buffer.concat([hdr, payload]));
  }

  /** Sends a request and resolves with its result. */
  call<R = unknown>(
    method: string,
    params: unknown,
    opts: { signal?: AbortSignal; run?: string; onItem?: (item: unknown) => void } = {},
  ): Promise<R> {
    if (this.closedWith) return Promise.reject(this.closedWith);
    const id = ++this.nextId;
    return new Promise<R>((resolve, reject) => {
      let timer: NodeJS.Timeout | undefined;
      const done = () => {
        this.pending.delete(id);
        if (timer) clearTimeout(timer);
        opts.signal?.removeEventListener("abort", onAbort);
      };
      const onAbort = () => {
        done();
        this.notify(methods.cancel, { id });
        reject(new RpcError(codeCancelled, `${method} was cancelled`));
      };
      this.pending.set(id, {
        resolve: (v) => (done(), resolve(v as R)),
        reject: (e) => (done(), reject(e)),
        onItem: opts.onItem,
      });
      if (opts.signal) {
        if (opts.signal.aborted) return onAbort();
        opts.signal.addEventListener("abort", onAbort);
      }
      if (this.limits.timeout_ms > 0) {
        timer = setTimeout(() => {
          done();
          this.notify(methods.cancel, { id });
          reject(new RpcError(codeTimeout, `${method} timed out`));
        }, this.limits.timeout_ms);
      }
      try {
        this.write({ id, method, params, run: opts.run });
      } catch (e) {
        done();
        reject(e);
      }
    });
  }

  /** Sends a request whose result streams, yielding each item. */
  async *callStream(method: string, params: unknown, opts: { signal?: AbortSignal; run?: string } = {}): AsyncIterable<unknown> {
    const items: unknown[] = [];
    let wake: (() => void) | undefined;
    let finished = false;
    let failure: unknown;
    this.call(method, params, { ...opts, onItem: (i) => (items.push(i), wake?.()) }).then(
      () => ((finished = true), wake?.()),
      (e) => ((failure = e), (finished = true), wake?.()),
    );
    for (;;) {
      if (items.length) {
        yield items.shift();
        continue;
      }
      if (finished) break;
      await new Promise<void>((r) => (wake = r));
      wake = undefined;
    }
    if (failure) throw failure;
  }

  notify(method: string, params: unknown): void {
    if (!this.closedWith) this.write({ method, params });
  }

  private onData(chunk: Buffer): void {
    this.buf = this.buf.length ? Buffer.concat([this.buf, chunk]) : chunk;
    while (this.buf.length >= 4) {
      const n = this.buf.readUInt32BE(0);
      if (this.limits.max_frame > 0 && n > this.limits.max_frame) {
        this.close(new RpcError(codeLimitExceeded, `frame of ${n} bytes is over the limit of ${this.limits.max_frame}`));
        return;
      }
      if (this.buf.length < 4 + n) return;
      const payload = this.buf.subarray(4, 4 + n);
      this.buf = this.buf.subarray(4 + n);
      let m: Message;
      try {
        m = this.wire.decode(new Uint8Array(payload)) as Message;
      } catch {
        continue;
      }
      this.dispatch(m);
    }
  }

  private dispatch(m: Message): void {
    if (m.id !== undefined && m.id !== null && m.method === undefined) {
      const p = this.pending.get(Number(m.id));
      if (!p) return;
      if (m.error) p.reject(new RpcError(Number(m.error.code), m.error.message, m.error.type, m.error.data));
      else p.resolve(m.result ?? null);
      return;
    }
    if (m.id !== undefined && m.id !== null) {
      void this.serve(Number(m.id), m);
      return;
    }
    if (m.method === methods.cancel) {
      this.serving.get(Number((m.params as { id: number }).id))?.abort();
      return;
    }
    if (m.method === methods.stream) {
      const { id, item } = m.params as { id: number; item: unknown };
      this.pending.get(Number(id))?.onItem?.(item);
      return;
    }
    const f = m.method ? this.notifications[m.method] : undefined;
    if (f) f(m.params);
  }

  private async serve(id: number, m: Message): Promise<void> {
    const h = this.handlers[m.method!];
    if (!h) {
      this.write({ id, error: { code: codeMethodNotFound, message: `method ${JSON.stringify(m.method)} isn't served` } });
      return;
    }
    if (this.limits.max_in_flight > 0 && this.serving.size >= this.limits.max_in_flight) {
      this.write({ id, error: { code: codeLimitExceeded, message: `more than ${this.limits.max_in_flight} requests in flight` } });
      return;
    }
    const ac = new AbortController();
    this.serving.set(id, ac);
    const rc: RequestContext = {
      signal: ac.signal,
      run: m.run,
      stream: (item) => this.notify(methods.stream, { id, item }),
    };
    let out: Message;
    try {
      const result = await h(m.params, rc);
      out = ac.signal.aborted ? { id, error: { code: codeCancelled, message: "cancelled" } } : { id, result: result ?? null };
    } catch (e) {
      const err = this.toError(e);
      const eo: ErrorObject = { code: err.code, message: err.message, type: err.type, data: err.data };
      out = { id, error: eo };
    } finally {
      this.serving.delete(id);
    }
    try {
      this.write(out);
    } catch (e) {
      const err = this.toError(e);
      this.write({ id, error: { code: err.code, message: `result: ${err.message}` } });
    }
  }

  close(e: Error): void {
    if (this.closedWith) return;
    this.closedWith = e;
    for (const p of this.pending.values()) p.reject(new RpcError(codeForeignError, e.message));
    this.pending.clear();
    for (const ac of this.serving.values()) ac.abort();
    this.markClosed();
  }
}
