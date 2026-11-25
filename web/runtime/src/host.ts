// The JS host: one long-lived Node process per runtime and session, which
// heron starts and supervises. It loads the modules that declarations name
// through js(…) and serves their exports over the protocol.
import { isAbsolute, resolve as resolvePath } from "node:path";
import { createRequire } from "node:module";
import { pathToFileURL } from "node:url";
import { Conn, type ConnOptions } from "./conn.js";
import { serveExtension, toRpcError } from "./extension.js";
import type { Declaration, InitializeParams } from "./protocol.gen.js";
import { collectRenderers, defineFunc, isImpl, type Impl } from "./sdk.js";
import { wireByName } from "./values.js";

/** Imports a module named by js(…): a file relative to the project root, or a package from its node_modules. */
export async function importModule(spec: string, root: string): Promise<Record<string, unknown>> {
  let url: string;
  if (spec.startsWith("./") || spec.startsWith("../") || isAbsolute(spec)) {
    url = pathToFileURL(isAbsolute(spec) ? spec : resolvePath(root, spec)).href;
  } else {
    const req = createRequire(resolvePath(root, "package.json"));
    let resolved: string;
    try {
      resolved = req.resolve(spec);
    } catch {
      resolved = import.meta.resolve(spec, pathToFileURL(resolvePath(root, "package.json")).href);
    }
    url = resolved.startsWith("file:") ? resolved : pathToFileURL(resolved).href;
  }
  return (await import(url)) as Record<string, unknown>;
}

/** Finds a declaration's implementation among a module's exports. */
export function exportOf(mod: Record<string, unknown>, decl: Declaration): Impl | null {
  if (decl.point === "renderer") return collectRenderers(mod);
  const name = decl.export ?? "default";
  let v = mod[name];
  if (v === undefined && name !== "default" && mod.default && typeof mod.default === "object") {
    v = (mod.default as Record<string, unknown>)[name];
  }
  if (v === undefined) return null;
  if (isImpl(v)) return v;
  if (typeof v === "function" && decl.point === "func") return defineFunc(v as never);
  throw new Error(`${decl.module}: export ${name} isn't an implementation made with @heron/runtime`);
}

/** Serves the JS host on a pair of streams. */
export function serveHost(input: NodeJS.ReadableStream, output: NodeJS.WritableStream, opts: ConnOptions & { onShutdown?: () => void } = {}): Conn {
  const conn = new Conn(input as never, output as never, { toError: toRpcError, ...opts });
  const modules = new Map<string, Promise<Record<string, unknown>>>();
  serveExtension(conn, {
    name: "@heron/runtime host",
    version: "0.1.0",
    onShutdown: opts.onShutdown,
    async resolve(decl: Declaration, init: InitializeParams) {
      if (!decl.module) return null;
      let m = modules.get(decl.module);
      if (!m) {
        m = importModule(decl.module, init.root);
        modules.set(decl.module, m);
      }
      return exportOf(await m, decl);
    },
  });
  return conn;
}

/**
 * Serves a standalone extension, as `extension(command = […])` starts one,
 * with its implementations by declaration path.
 */
export function runExtension(name: string, version: string, impls: Record<string, Impl>): Conn {
  const wire = wireByName(process.argv.includes("--wire") ? process.argv[process.argv.indexOf("--wire") + 1] : undefined);
  const conn = new Conn(process.stdin, process.stdout, { wire, toError: toRpcError });
  serveExtension(conn, { name, version, resolve: (d) => impls[d.path] ?? null });
  conn.closed.then(() => process.exit(0));
  return conn;
}
