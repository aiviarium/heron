// The shell's extension points, each proven with a sample extension that
// registers the way a later unit's frontend package would.
import { describe, expect, it } from "vitest";
import { createRoot, flush } from "solid-js";
import {
  allCommands,
  allPanels,
  gutterMarks,
  registerCommand,
  registerGutterProvider,
  registerPanel,
  registerRenderer,
  rendererFor,
} from "./extensions.ts";
import { routes } from "./api.gen.ts";

describe("extension points", () => {
  it("mounts a sample panel, gutter provider, command and renderer", () => {
    createRoot((dispose) => {
      let ran = 0;
      const offPanel = registerPanel({ id: "sample", title: "Sample", slot: "bottom", render: () => null });
      const offGutter = registerGutterProvider({
        id: "sample",
        provide: ({ outline, results }) =>
          outline.map((n) => ({ line: n.line, status: results.find((r) => r.path === n.path)?.status as "ok" | undefined, label: "sampled" })),
      });
      const offCmd = registerCommand({ id: "sample", title: "Sample", shortcut: "mod+j", run: () => void ran++ });
      const offRenderer = registerRenderer({ kind: "scenario", mount: (el) => void (el.dataset.sampled = "1") });
      flush();

      expect(allPanels().map((p) => p.id)).toContain("sample");
      const marks = gutterMarks({
        file: "a.heron",
        outline: [{ path: "x", name: "x", kind: "task", depth: 0, line: 3, end_line: 5, runnable: true }],
        results: [{ source: "sample", path: "x", status: "ok", summary: "" }],
      });
      expect(marks).toEqual([{ line: 3, status: "ok", label: "sampled" }]);
      void allCommands().find((c) => c.id === "sample")!.run();
      expect(ran).toBe(1);
      const el = document.createElement("div");
      rendererFor("scenario")!.mount(el, { path: "x", kind: "scenario" });
      expect(el.dataset.sampled).toBe("1");
      expect(() => registerCommand({ id: "sample", title: "Again", run: () => {} })).toThrow(/twice/);

      offPanel(), offGutter(), offCmd(), offRenderer();
      flush();
      expect(allPanels().some((p) => p.id === "sample")).toBe(false);
      expect(rendererFor("scenario")).toBeUndefined();
      dispose();
    });
  });

  it("has a generated client for every op of spec_server", () => {
    const ops = routes.map((r) => r.op);
    for (const op of ["preview", "results", "get_record", "lsp"]) expect(ops).toContain("spec_server." + op);
    expect(routes.find((r) => r.op === "spec_server.lsp")!.socket).toBe(true);
    expect(routes.find((r) => r.op === "spec_server.run_task")!.stream).toBe(true);
  });
});
