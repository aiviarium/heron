// The end-to-end suite, run inside VS Code: it opens the sample tree, waits
// for the language server's diagnostics, and checks hover, completion, the
// preview following edits and the cursor, and a restart.
import * as vscode from "vscode";
import type { HeronApi } from "../../src/extension";
import type { PreviewState } from "../../src/preview";

const tests: [string, () => Promise<void>][] = [];
const it = (name: string, f: () => Promise<void>) => tests.push([name, f]);

function ok(cond: unknown, msg: string): asserts cond {
  if (!cond) throw new Error(msg);
}

async function until<T>(what: string, f: () => T | undefined | Promise<T | undefined>, ms = 30000): Promise<T> {
  const end = Date.now() + ms;
  for (;;) {
    const v = await f();
    if (v) return v;
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`);
    await new Promise((r) => setTimeout(r, 100));
  }
}

const folder = () => vscode.workspace.workspaceFolders![0].uri;
const file = (name: string) => vscode.Uri.joinPath(folder(), name);
let api: HeronApi;

function at(doc: vscode.TextDocument, text: string, plus = 0): vscode.Position {
  const i = doc.getText().indexOf(text);
  ok(i >= 0, `no ${text} in ${doc.uri.path}`);
  return doc.positionAt(i + plus);
}

function previewWhere(want: (s: PreviewState) => boolean): Promise<PreviewState> {
  return until("the preview", () => {
    const s = api.preview();
    return s && want(s) ? s : undefined;
  });
}

it("the extension activates and starts heron lsp", async () => {
  const ext = vscode.extensions.getExtension<HeronApi>("aiviarium.heron");
  ok(ext, "the extension isn't installed");
  api = await ext.activate();
  await until("the language client", () => api.client()?.isRunning());
});

it("a .heron file is heron and a .heron.md file is heron-md", async () => {
  const a = await vscode.workspace.openTextDocument(file("index.heron"));
  const b = await vscode.workspace.openTextDocument(file("guide.heron.md"));
  ok(a.languageId === "heron", `index.heron is ${a.languageId}`);
  ok(b.languageId === "heron-md", `guide.heron.md is ${b.languageId}`);
});

it("diagnostics come from the language server", async () => {
  const doc = await vscode.workspace.openTextDocument(file("model.heron"));
  await vscode.window.showTextDocument(doc);
  const ds = await until("diagnostics on model.heron", () => {
    const d = vscode.languages.getDiagnostics(doc.uri);
    return d.length ? d : undefined;
  });
  ok(ds.some((d) => d.message.includes("unknown kind or type Strng") && d.range.start.line === 5), `diagnostics: ${JSON.stringify(ds.map((d) => d.message))}`);
});

it("hover shows a node's docs", async () => {
  const doc = await vscode.workspace.openTextDocument(file("index.heron"));
  const hovers = await until("a hover", async () => {
    const h = await vscode.commands.executeCommand<vscode.Hover[]>("vscode.executeHoverProvider", doc.uri, at(doc, "#Order", 2));
    return h && h.length ? h : undefined;
  });
  const text = hovers.flatMap((h) => h.contents.map((c) => (typeof c === "string" ? c : c.value))).join("\n");
  ok(text.includes("An order someone places."), `hover: ${text}`);
});

it("completion offers the tree's nodes in a link", async () => {
  const doc = await vscode.workspace.openTextDocument(file("index.heron"));
  const list = await vscode.commands.executeCommand<vscode.CompletionList>("vscode.executeCompletionItemProvider", doc.uri, at(doc, "#Order", 1));
  const labels = list.items.map((i) => (typeof i.label === "string" ? i.label : i.label.label));
  ok(labels.includes("Order"), `completion: ${labels.slice(0, 20).join(", ")}`);
});

it("the preview shows the node at the cursor and follows edits and the cursor", async () => {
  const doc = await vscode.workspace.openTextDocument(file("index.heron"));
  const editor = await vscode.window.showTextDocument(doc, vscode.ViewColumn.One);
  const p = at(doc, "Read [the order]");
  editor.selection = new vscode.Selection(p, p);
  await vscode.commands.executeCommand("heron.openPreviewToSide");
  await previewWhere((s) => s.path === "guide" && s.html.includes("before you place one"));
  await editor.edit((e) => e.replace(new vscode.Range(at(doc, "before you place one"), at(doc, "before you place one", "before you place one".length)), "when you place one"));
  await previewWhere((s) => s.path === "guide" && s.html.includes("when you place one"));
  const top = new vscode.Position(0, 0);
  editor.selection = new vscode.Selection(top, top);
  await previewWhere((s) => s.path === "" && !s.error);
});

it("Restart Language Server starts a new client", async () => {
  const before = api.client();
  await vscode.commands.executeCommand("heron.restartServer");
  await until("a new running client", () => {
    const c = api.client();
    return c && c !== before && c.isRunning();
  });
});

export async function run(): Promise<void> {
  const failed: string[] = [];
  for (const [name, f] of tests) {
    try {
      await f();
      console.log(`ok   ${name}`);
    } catch (e) {
      console.log(`FAIL ${name}: ${e instanceof Error ? e.message : e}`);
      failed.push(name);
    }
  }
  if (failed.length) throw new Error(`${failed.length} of ${tests.length} failed`);
}
