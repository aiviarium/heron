// The editor screen's state: the workspace from the server, one model per
// open file, the language-server client, and the analyses the server runs
// on the unsaved texts (problems, outline, preview, results).
import { createSignal } from "solid-js";
import { LspClient, Models, registerProviders, monaco, setupMonaco, type FileModel } from "@heron/editor";
import { createClient, type NodeResult, type OutlineNode, type Preview, type Problem, type Workspace, type GeneratedFile } from "./api.gen.ts";

export const api = createClient();

const sig = <T,>(v: T) => createSignal<T>(v as Exclude<T, Function>, { ownedWrite: true });

export const [workspace, setWorkspace] = sig<Workspace | null>(null);
export const [loadError, setLoadError] = sig<string | null>(null);
export const [current, setCurrent] = sig<string | null>(null);
export const [openFiles, setOpenFiles] = sig<string[]>([]);
export const [problems, setProblems] = sig<Problem[]>([]);
export const [outline, setOutline] = sig<OutlineNode[]>([]);
export const [cursor, setCursor] = sig({ line: 1, column: 1, offset: 0 });
export const [preview, setPreview] = sig<Preview | null>(null);
export const [previewError, setPreviewError] = sig<string | null>(null);
export const [results, setResults] = sig<NodeResult[]>([]);
/** The latest results of every node, for the gutter. */
export const [allResults, setAllResults] = sig<NodeResult[]>([]);
export const [generated, setGenerated] = sig<GeneratedFile[]>([]);
export const [showPreview, setShowPreview] = sig(true);
export const [bottomPanel, setBottomPanel] = sig<string | null>(null);
export const [paletteOpen, setPaletteOpen] = sig(false);
export const [reveal, setReveal] = sig<{ path: string; line: number; column: number } | null>(null);
/** Runs started from the gutter, by task path: status and streamed output. */
export const [runs, setRuns] = sig<Record<string, { status: string; output: string[]; summary: string }>>({});

function initialTheme(): "dark" | "light" {
  try {
    const s = localStorage.getItem("heron.theme");
    if (s === "dark" || s === "light") return s;
  } catch {
    /* storage can be blocked */
  }
  return "dark";
}
export const [theme, setThemeSignal] = sig<"dark" | "light">(initialTheme());

export function setTheme(th: "dark" | "light") {
  setThemeSignal(th);
  document.documentElement.dataset.theme = th;
  try {
    localStorage.setItem("heron.theme", th);
  } catch {
    /* storage can be blocked */
  }
}

export const [ready, setReady] = sig(false);
export let lsp: LspClient;
export let models: Models;
export let editor: monaco.editor.IStandaloneCodeEditor | null = null;
export const setEditor = (e: monaco.editor.IStandaloneCodeEditor) => (editor = e);

export const isGenerated = (path: string) => path.startsWith("generated:") || path.startsWith("external:");

/** Loads the workspace and opens the file in the URL, or the entry point. */
export async function start() {
  document.documentElement.dataset.theme = theme();
  setupMonaco();
  let ws: Workspace;
  try {
    ws = await api.workspace();
  } catch (e) {
    setLoadError(String((e as Error).message ?? e));
    return;
  }
  const rootUri = monaco.Uri.file(ws.root).toString();
  lsp = new LspClient(api.lsp(), rootUri);
  models = new Models(rootUri, lsp);
  registerProviders(monaco, lsp, {
    ensureModel: async (uri) => {
      const path = models.pathOf(uri);
      if (path && !models.get(path)) await load(path);
      else if (!path && !monaco.editor.getModel(monaco.Uri.parse(uri))) {
        // A standard package's file: read only, from the language server.
        const res = await lsp.request<{ text: string }>("heron/source", { uri }).catch(() => null);
        if (res) models.external(uri, res.text);
      }
    },
  });
  monaco.editor.registerEditorOpener({
    openCodeEditor(_src, resource, sel) {
      const ext = models.get("external:" + resource.toString());
      if (ext) {
        const p = "external:" + resource.toString();
        if (!openFiles().includes(p)) setOpenFiles([...openFiles(), p]);
        setCurrent(p);
        const line = sel && "startLineNumber" in sel ? sel.startLineNumber : sel && "lineNumber" in sel ? sel.lineNumber : 1;
        setReveal({ path: p, line, column: 1 });
        return true;
      }
      const path = models.pathOf(resource.toString());
      if (!path) return false;
      const pos = sel && "startLineNumber" in sel ? { line: sel.startLineNumber, column: sel.startColumn } : sel && "lineNumber" in sel ? { line: sel.lineNumber, column: sel.column } : { line: 1, column: 1 };
      void openFile(path, pos);
      return true;
    },
  });
  lsp.start();
  exposeDebug();
  setReady(true);
  setWorkspace(ws);
  window.addEventListener("popstate", () => void openFile(fromURL() ?? ws.entry, undefined, false));
  await openFile(fromURL() ?? ws.entry, undefined, false);
  refreshGenerated();
}

/**
 * Read-only views of the editor for the acceptance tests, which otherwise
 * act through the visible interface: Monaco's markers, models and cursor.
 */
function exposeDebug() {
  (window as unknown as { heronDebug: unknown }).heronDebug = {
    ready: () => ready() && !!current() && !!currentModel(),
    current: () => current(),
    value: () => currentModel()?.model.getValue(),
    text: (path: string) => models.get(path)?.model.getValue(),
    /** Page coordinates of a position, for pointing at it. */
    coords: (line: number, column: number) => {
      const p = editor?.getScrolledVisiblePosition({ lineNumber: line, column });
      const r = editor?.getDomNode()?.getBoundingClientRect();
      return p && r ? { x: r.left + p.left + 2, y: r.top + p.top + p.height / 2 } : null;
    },
    position: () => editor?.getPosition(),
    models: () => monaco.editor.getModels().map((m) => m.uri.toString()),
    // The static grammar's tokens of a text, line by line, as [offset, type].
    tokenize: (text: string, lang: string) => monaco.editor.tokenize(text, lang).map((l) => l.map((t) => [t.offset, t.type])),
    /** The static grammar's tokens of a line of the current model. */
    tokens: (line: number) => {
      const m = currentModel()?.model as unknown as { tokenization?: { getLineTokens(n: number): { getCount(): number; getEndOffset(i: number): number; getClassName(i: number): string } } };
      const lt = m?.tokenization?.getLineTokens(line);
      return lt ? Array.from({ length: lt.getCount() }, (_, i) => [lt.getEndOffset(i), lt.getClassName(i)]) : null;
    },
    options: () => {
      const o = editor?.getOptions();
      return o ? { autoClosingBrackets: o.get(monaco.editor.EditorOption.autoClosingBrackets), autoIndent: o.get(monaco.editor.EditorOption.autoIndent) } : null;
    },
    markers: () =>
      monaco.editor.getModelMarkers({}).map((m) => ({
        file: models.pathOf(m.resource.toString()),
        line: m.startLineNumber,
        column: m.startColumn,
        severity: m.severity === monaco.MarkerSeverity.Error ? "error" : m.severity === monaco.MarkerSeverity.Warning ? "warning" : "info",
        message: m.message,
        source: m.source,
      })),
  };
}

function fromURL(): string | null {
  const m = location.pathname.match(/^\/edit\/(.+)$/);
  return m ? decodeURIComponent(m[1]) : null;
}

async function load(path: string): Promise<FileModel> {
  const have = models.get(path);
  if (have) return have;
  const f = await api.readFile(path);
  return models.open(path, f.text);
}

/** Opens a file in the editor, at a position when given. */
export async function openFile(path: string, pos?: { line: number; column: number }, push = true) {
  if (path.startsWith("external:")) {
    setCurrent(path);
    if (pos) setReveal({ path, ...pos });
    return;
  }
  if (isGenerated(path)) {
    const g = generated().find((x) => "generated:" + x.path === path);
    if (!g) return;
    models.generated(g.path, g.text);
  } else {
    try {
      await load(path);
    } catch (e) {
      setLoadError(String((e as Error).message ?? e));
      return;
    }
  }
  if (!openFiles().includes(path)) setOpenFiles([...openFiles(), path]);
  setCurrent(path);
  if (push && !isGenerated(path)) history.pushState(null, "", "/edit/" + path.split("/").map(encodeURIComponent).join("/"));
  if (pos) setReveal({ path, ...pos });
  schedule();
}

export function closeFile(path: string) {
  const rest = openFiles().filter((p) => p !== path);
  setOpenFiles(rest);
  if (current() === path) {
    const next = rest[rest.length - 1] ?? workspace()?.entry ?? null;
    if (next) void openFile(next);
  }
}

export function currentModel(): FileModel | undefined {
  const c = current();
  if (!c || !ready()) return undefined;
  models.tick();
  if (c.startsWith("external:")) return models.get(c);
  return models.get(isGenerated(c) ? "heron-generated:" + c.slice("generated:".length) : c);
}

export async function save() {
  const c = current();
  const fm = currentModel();
  if (!c || !fm || fm.readOnly) return;
  const text = fm.model.getValue();
  await api.writeFile({ path: c, text });
  fm.setSaved(text);
  lsp.save(fm.model.uri.toString(), text);
  schedule();
  refreshGenerated();
}

export async function format() {
  await editor?.getAction("editor.action.formatDocument")?.run();
}

// The analyses run on the unsaved texts: the preview within a frame of an
// edit, the outline and problems shortly after.
let frame = 0;
let previewSeq = 0;
let slowTimer: ReturnType<typeof setTimeout> | null = null;

export function schedule() {
  if (!frame) frame = requestAnimationFrame(() => ((frame = 0), void runPreview()));
  if (slowTimer) clearTimeout(slowTimer);
  slowTimer = setTimeout(() => void runSlow(), 150);
}

async function runPreview() {
  if (!models) return; // the workspace is still loading
  const file = current();
  if (!file || isGenerated(file)) {
    setPreview(null);
    return;
  }
  const seq = ++previewSeq;
  try {
    const p = await api.preview({ file, offset: cursor().offset, overlays: models.overlays() });
    if (seq !== previewSeq) return;
    if (p.error) {
      setPreviewError(p.error);
    } else {
      setPreviewError(null);
      setPreview(p);
    }
    setResults(p.results.results);
  } catch (e) {
    if (seq === previewSeq) setPreviewError(String((e as Error).message ?? e));
  }
}

let slowSeq = 0;
async function runSlow() {
  if (!models) return; // the workspace is still loading
  const file = current();
  const seq = ++slowSeq;
  const overlays = models.overlays();
  try {
    const [pr, ol, rs] = await Promise.all([
      api.problems({ overlays }),
      file && !isGenerated(file) ? api.outline({ file, overlays }) : Promise.resolve({ file: "", nodes: [] }),
      api.results(""),
    ]);
    if (seq !== slowSeq) return;
    // Unchanged answers keep their objects, so lists don't re-render.
    if (JSON.stringify(pr.problems) !== JSON.stringify(problems())) setProblems(pr.problems);
    if (JSON.stringify(ol.nodes) !== JSON.stringify(outline())) setOutline(ol.nodes);
    if (JSON.stringify(rs.results) !== JSON.stringify(allResults())) setAllResults(rs.results);
  } catch {
    /* the next edit retries */
  }
}

async function refreshGenerated() {
  try {
    const g = await api.generated({ overlays: {} });
    setGenerated(g.files);
  } catch {
    setGenerated([]);
  }
}

/** Runs a task and streams its output into the preview and the gutter. */
export async function runTask(path: string) {
  if (runs()[path]?.status === "running") return;
  const put = (f: (r: { status: string; output: string[]; summary: string }) => { status: string; output: string[]; summary: string }) =>
    setRuns({ ...runs(), [path]: f(runs()[path] ?? { status: "running", output: [], summary: "" }) });
  put(() => ({ status: "running", output: [], summary: "" }));
  try {
    for await (const ev of api.runTask({ path })) {
      if (ev.type === "output" && ev.text !== undefined) put((r) => ({ ...r, output: [...r.output, ev.text!] }));
      if (ev.type === "done" && ev.result) put((r) => ({ ...r, status: ev.result!.status, summary: ev.result!.summary }));
    }
  } catch (e) {
    put((r) => ({ ...r, status: "failed", output: [...r.output, String((e as Error).message ?? e)] }));
  }
  schedule();
}

/** Problem counts of a file, or of the whole tree. */
export function counts(path?: string) {
  let errors = 0;
  let warnings = 0;
  for (const p of problems()) {
    if (path !== undefined && p.file !== path) continue;
    if (p.severity === "error") errors++;
    else if (p.severity === "warning") warnings++;
  }
  return { errors, warnings };
}

/** Moves the cursor to the next problem of the tree, opening its file. */
export function nextProblem() {
  const ps = problems();
  if (ps.length === 0) return;
  const c = current();
  const cur = cursor();
  const after = ps.find((p) => p.file === c && (p.line > cur.line || (p.line === cur.line && p.column > cur.column)));
  const p = after ?? ps.find((x) => x.file !== c) ?? ps[0];
  void openFile(p.file, { line: p.line, column: p.column });
}
