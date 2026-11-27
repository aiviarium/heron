#!/usr/bin/env node
// The JS host's entry point, which heron starts as
// `node <runtime>/dist/host-main.js`, speaking Sparrow on its standard
// input and output. TS modules load through
// Node's type stripping; an import without an extension, as in
// `import { bind } from "../.heron/bindings"`, finds its .ts or .js file.
import { existsSync } from "node:fs";
import * as nodeModule from "node:module";
import { fileURLToPath } from "node:url";
import { serveHost } from "./host.js";

type ResolveFn = (spec: string, ctx: unknown) => { url: string };
const register = (nodeModule as unknown as { registerHooks?: (h: { resolve: (s: string, c: unknown, next: ResolveFn) => unknown }) => void }).registerHooks;
register?.({
  resolve(spec, ctx, next) {
    try {
      return next(spec, ctx);
    } catch (e) {
      if (!(spec.startsWith("./") || spec.startsWith("../"))) throw e;
      for (const ext of [".ts", ".js", "/index.ts", "/index.js"]) {
        try {
          const r = next(spec + ext, ctx);
          if (r.url.startsWith("file:") && existsSync(fileURLToPath(r.url))) return r;
        } catch {
          // try the next extension
        }
      }
      throw e;
    }
  },
});

const conn = serveHost(process.stdin, process.stdout);
conn.closed.then(() => process.exit(0));
