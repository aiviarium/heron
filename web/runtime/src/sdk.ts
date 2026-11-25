// The SDK: what a TS package writes to implement a foreign func, a macro, a
// transport, a lint, a resource driver or a generation adapter. Each
// define* call returns an implementation tagged with its extension point;
// the generated `.heron/bindings.ts` wraps them with the types of their
// Heron declarations, so `tsc` checks an implementation against them.
import type { Finding, GeneratedFile, Node, RenderParams, Severity } from "./protocol.gen.js";

/** The way back into Heron, as the Interop tab's `ctx`. */
export interface Ctx {
  /** The run the call belongs to. */
  readonly run?: string;
  /** Aborted when the call is cancelled or times out. */
  readonly signal: AbortSignal;
  /** Reads a node of the resolved tree lazily, by path. */
  node(path: string): Promise<Node | null>;
  /** Calls an op or a func of the tree. */
  call<R = unknown>(target: string, args?: Record<string, unknown>): Promise<R>;
  /** The instance of a resource from the lifecycle, by path. */
  resource<T = unknown>(name: string): Promise<T>;
  /** Resolves a secret, only inside the host and only when used. */
  secret(name: string): Promise<string>;
  /** A path for a run-scoped file. */
  temp(name: string): string;
  /** Writes a line to the run's log. */
  log(message: string, level?: Severity): void;
  /** Reports progress of the call. */
  progress(message: string, done?: number, total?: number): void;
}

export type Point = "func" | "macro" | "transport" | "lint" | "resource" | "generator" | "renderer";

interface Tagged<P extends Point> {
  readonly point: P;
  /** The declaration's path, which generated bindings stamp. */
  readonly path?: string;
}

export interface FuncImpl<A = Record<string, unknown>, R = unknown> extends Tagged<"func"> {
  readonly fn: (args: A, ctx: Ctx) => R | Promise<R> | AsyncIterable<R>;
}

export interface MacroImpl<A = Record<string, unknown>> extends Tagged<"macro"> {
  /** Returns the Heron source the macro expands to. */
  readonly expand: (args: A, ctx: Ctx) => string | Promise<string>;
}

/** A transport implements std/apis' Transport interface. */
export interface Transport {
  /** Sends one request of an op and returns its response. */
  send(op: Node, request: unknown, ctx: Ctx): Promise<unknown> | unknown;
}

export interface TransportImpl extends Tagged<"transport">, Transport {}

/** What a lint reports: a finding on a node, optionally at a byte range and with a fix. */
export type LintFinding = Finding;

export interface LintImpl extends Tagged<"lint"> {
  run(nodes: Node[], ctx: Ctx): LintFinding[] | Promise<LintFinding[]>;
}

/** A resource driver: the lifecycle of the Testing tab, for instances of type H. */
export interface ResourceDriver<H = unknown, S = unknown> {
  setup(ctx: Ctx): H | Promise<H>;
  reset?(instance: H, ctx: Ctx): void | Promise<void>;
  snapshot?(instance: H, ctx: Ctx): S | Promise<S>;
  restore?(instance: H, snapshot: S, ctx: Ctx): void | Promise<void>;
  fork?(instance: H, ctx: Ctx): H | Promise<H>;
  teardown?(instance: H, ctx: Ctx): void | Promise<void>;
}

export interface ResourceImpl<H = unknown, S = unknown> extends Tagged<"resource">, ResourceDriver<H, S> {}

/** A generation adapter: reads the resolved tree and returns files. */
export interface GeneratorImpl extends Tagged<"generator"> {
  readonly target?: string;
  generate(tree: Node, ctx: Ctx): GeneratedFile[] | Promise<GeneratedFile[]>;
}

/**
 * What a docs renderer receives: a node, or a markup tag (no path), as
 * plain data. The generator has rendered its prose, children and slots
 * already; `attrs` are the kind's arguments or the tag's attributes, typed
 * by the kind's parameters.
 */
export type RenderInput = Omit<RenderParams, "renderer">;

/** What a docs renderer returns: HTML, or HTML with whether a tag is a block and the islands it uses. */
export type RenderOutput = string | { html: string; block?: boolean; islands?: string[] };

const RENDERER = Symbol.for("@heron/runtime.renderer");

/** A docs renderer for one kind, made with defineRenderer. */
export interface Renderer {
  readonly [RENDERER]: true;
  /** The kind's declaration path, such as "spec.rule" or "guide.steps". */
  readonly kind: string;
  render(node: RenderInput, ctx: Ctx): RenderOutput | Promise<RenderOutput>;
}

/**
 * A module named in a docs block's `renderers`: every renderer its exports
 * hold, by kind. The JS host collects it; a module exports renderers, or
 * arrays of them, made with defineRenderer.
 */
export interface RendererModuleImpl extends Tagged<"renderer"> {
  readonly renderers: ReadonlyMap<string, Renderer>;
}

export type Impl = FuncImpl<any, any> | MacroImpl<any> | TransportImpl | LintImpl | ResourceImpl<any, any> | GeneratorImpl | RendererModuleImpl;

export function defineFunc<A = Record<string, unknown>, R = unknown>(
  fn: (args: A, ctx: Ctx) => R | Promise<R> | AsyncIterable<R>,
  opts: { path?: string } = {},
): FuncImpl<A, R> {
  return { point: "func", fn, path: opts.path };
}

export function defineMacro<A = Record<string, unknown>>(
  expand: (args: A, ctx: Ctx) => string | Promise<string>,
  opts: { path?: string } = {},
): MacroImpl<A> {
  return { point: "macro", expand, path: opts.path };
}

export function defineTransport(t: Transport, opts: { path?: string } = {}): TransportImpl {
  return { point: "transport", path: opts.path, send: t.send.bind(t) };
}

export function defineLint(l: { run: LintImpl["run"] }, opts: { path?: string } = {}): LintImpl {
  return { point: "lint", path: opts.path, run: l.run.bind(l) };
}

export function defineResource<H, S = unknown>(d: ResourceDriver<H, S>, opts: { path?: string } = {}): ResourceImpl<H, S> {
  return { ...d, point: "resource", path: opts.path };
}

export function defineGenerator(
  g: { target?: string; generate: GeneratorImpl["generate"] },
  opts: { path?: string } = {},
): GeneratorImpl {
  return { point: "generator", path: opts.path, target: g.target, generate: g.generate.bind(g) };
}

/**
 * Renders the nodes of a kind in the docs, and the markup tags of it. It
 * runs in the JS host when the docs are generated. Return markup around
 * what `node` carries (its title, prose, children, body and attrs), not
 * words of your own: the docs show only the spec's text and the
 * catalogue's.
 */
export function defineRenderer(kind: string, render: Renderer["render"]): Renderer {
  return { [RENDERER]: true, kind, render };
}

/** Whether a value is a renderer made with defineRenderer. */
export function isRenderer(v: unknown): v is Renderer {
  return typeof v === "object" && v !== null && (v as Record<symbol, unknown>)[RENDERER] === true;
}

/** Collects the renderers of a module's exports, by kind; a later export of a kind replaces an earlier one. */
export function collectRenderers(mod: Record<string, unknown>): RendererModuleImpl {
  const renderers = new Map<string, Renderer>();
  const add = (v: unknown) => {
    if (isRenderer(v)) renderers.set(v.kind, v);
    else if (Array.isArray(v)) v.forEach(add);
  };
  add(mod.default);
  for (const [k, v] of Object.entries(mod)) if (k !== "default") add(v);
  return { point: "renderer", renderers };
}

/** Escapes text for HTML, for a renderer that writes a value of its own into markup. */
export function escapeHtml(s: string): string {
  return s.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}

/** Whether a value is an implementation made with a define* call. */
export function isImpl(v: unknown): v is Impl {
  return (
    typeof v === "object" &&
    v !== null &&
    typeof (v as { point?: unknown }).point === "string" &&
    ["func", "macro", "transport", "lint", "resource", "generator", "renderer"].includes((v as { point: string }).point)
  );
}

/** Stamps an implementation with its declaration's path; generated bindings use it. */
export function stamp<T extends Impl>(impl: T, path: string): T {
  return { ...impl, path };
}
