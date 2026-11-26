// The frontend is one client of the published API: every request it makes
// goes through the client generated from spec_server, and the only other
// paths it loads are the docs' assets the server's serving table lists.
import { describe, expect, it } from "vitest";
import { routes } from "./api.gen.ts";

// Every source file of the shell and the editor, as text, by path from web/.
const raw = {
  ...(import.meta as unknown as { glob: Glob }).glob("./**/*.{ts,tsx}", { query: "?raw", import: "default", eager: true }),
  ...(import.meta as unknown as { glob: Glob }).glob("../../editor/src/**/*.{ts,tsx}", { query: "?raw", import: "default", eager: true }),
};
type Glob = (p: string, o: object) => Record<string, string>;
const sources = Object.entries(raw)
  .filter(([p]) => !/\.test\.tsx?$/.test(p))
  .map(([p, text]) => ({ file: p.startsWith("./") ? "shell/src/" + p.slice(2) : p.replace(/^\.\.\/\.\.\//, ""), text }));

// Where a request may start: the generated client for the API, the
// language client for the socket whose URL the generated client gives,
// and the docs islands' loader for /docs/_assets/.
const allowed: Record<string, RegExp> = {
  "shell/src/api.gen.ts": /fetch/,
  "editor/src/lsp.ts": /new WebSocket\(/,
  "shell/src/docs.ts": /fetch\(ASSETS/,
};

describe("the frontend uses only the published API", () => {
  it("reads every source of the shell and the editor", () => {
    const files = sources.map((s) => s.file);
    expect(files).toContain("shell/src/state.ts");
    expect(files).toContain("editor/src/lsp.ts");
  });

  it("sends no request outside the generated client", () => {
    const bad: string[] = [];
    for (const { file, text } of sources) {
      for (const m of text.matchAll(/\bfetch\(|new WebSocket\(|new EventSource\(|XMLHttpRequest|navigator\.sendBeacon/g)) {
        const line = text.slice(text.lastIndexOf("\n", m.index) + 1, text.indexOf("\n", m.index));
        if (!allowed[file]?.test(line)) bad.push(`${file}: ${line.trim()}`);
      }
    }
    expect(bad).toEqual([]);
  });

  it("names no /api/ path the API doesn't declare", () => {
    const declared = routes.map((r) => r.path.replace(/:\w+/g, "[^/]+"));
    const bad: string[] = [];
    for (const { file, text } of sources) {
      if (file === "shell/src/api.gen.ts") continue;
      for (const m of text.matchAll(/["'`](\/api\/[^"'`$]*)/g)) {
        if (!declared.some((d) => new RegExp("^" + d + "$").test(m[1]))) bad.push(`${file}: ${m[1]}`);
      }
    }
    expect(bad).toEqual([]);
  });

  it("loads the docs' assets from the path the server serves them at", () => {
    const docs = sources.find((s) => s.file === "shell/src/docs.ts")!.text;
    expect(docs).toMatch(/ASSETS = "\/docs\/_assets\/"/);
  });
});
