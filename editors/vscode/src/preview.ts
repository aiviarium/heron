// The live preview: a webview beside the code that shows heron/preview of
// the cursor, asked again as the author types and moves.
import * as vscode from "vscode";
import type { LanguageClient } from "vscode-languageclient/node";
import { nonce, previewPage } from "./core";

/** heron/preview's answer. */
export interface PreviewAnswer {
  path: string;
  html: string;
  error?: string;
  styles?: string;
}

/** What the preview shows now, for the tests. */
export interface PreviewState {
  uri: string;
  path: string;
  html: string;
  error?: string;
}

export const isHeron = (d: vscode.TextDocument) => d.languageId === "heron" || d.languageId === "heron-md";

/** How long after an edit or a cursor move the preview asks again. */
const delay = 100;

export class Preview implements vscode.Disposable {
  private panel: vscode.WebviewPanel | undefined;
  private editor: vscode.TextEditor | undefined;
  private styled = false;
  private ready = false;
  private pending: unknown;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private asked = 0;
  private subs: vscode.Disposable[] = [];
  state: PreviewState | undefined;
  /** Resolved after each page the preview shows, for the tests. */
  onShown = new vscode.EventEmitter<PreviewState>();

  constructor(private readonly client: () => LanguageClient | undefined) {
    this.subs.push(
      vscode.window.onDidChangeActiveTextEditor((e) => {
        if (e && isHeron(e.document) && this.panel) {
          this.editor = e;
          this.ask(0);
        }
      }),
      vscode.window.onDidChangeTextEditorSelection((e) => {
        if (e.textEditor === this.editor) this.ask(delay);
      }),
      vscode.workspace.onDidChangeTextDocument((e) => {
        if (this.editor && e.document === this.editor.document) this.ask(delay);
      }),
    );
  }

  /** Opens the preview beside the editor, or shows it on that editor. */
  open(editor: vscode.TextEditor | undefined) {
    if (!editor || !isHeron(editor.document)) {
      void vscode.window.showInformationMessage("Heron: open a .heron or .heron.md file to preview it.");
      return;
    }
    this.editor = editor;
    if (!this.panel) {
      this.panel = vscode.window.createWebviewPanel(
        "heron.preview",
        "Heron Preview",
        { viewColumn: vscode.ViewColumn.Beside, preserveFocus: true },
        { enableScripts: true, localResourceRoots: [] },
      );
      this.styled = false;
      this.panel.webview.onDidReceiveMessage((m) => {
        if (m && m.ready) {
          this.ready = true;
          this.flush();
        }
      });
      this.panel.onDidDispose(() => {
        this.panel = undefined;
        this.state = undefined;
      });
    } else {
      this.panel.reveal(vscode.ViewColumn.Beside, true);
    }
    this.ask(0);
  }

  private ask(after: number) {
    if (!this.panel) return;
    clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.render(), after);
  }

  private async render() {
    const c = this.client();
    const editor = this.editor;
    const panel = this.panel;
    if (!c || !editor || !panel) return;
    const n = ++this.asked;
    const doc = editor.document;
    const position = editor.selection.active;
    let a: PreviewAnswer;
    try {
      a = await c.sendRequest<PreviewAnswer>("heron/preview", {
        uri: doc.uri.toString(),
        position: { line: position.line, character: position.character },
        styles: !this.styled,
      });
    } catch (e) {
      a = { path: "", html: "", error: e instanceof Error ? e.message : String(e) };
    }
    // A newer answer is on its way, or the preview was closed.
    if (n !== this.asked || this.panel !== panel) return;
    if (!this.styled) {
      // The page loads once, with the stylesheet the first answer carries;
      // every later page is a message to its script.
      this.ready = false;
      panel.webview.html = previewPage(nonce(), panel.webview.cspSource, a.styles ?? "");
      this.styled = a.styles !== undefined;
    }
    panel.title = `Preview ${a.path || doc.uri.path.split("/").pop()}`;
    this.pending = a.error ? { error: a.error } : { html: a.html };
    this.flush();
    this.state = a.error
      ? { ...(this.state ?? { path: a.path, html: "" }), uri: doc.uri.toString(), error: a.error }
      : { uri: doc.uri.toString(), path: a.path, html: a.html };
    this.onShown.fire(this.state);
  }

  private flush() {
    if (this.ready && this.panel && this.pending !== undefined) {
      void this.panel.webview.postMessage(this.pending);
      this.pending = undefined;
    }
  }

  dispose() {
    clearTimeout(this.timer);
    this.panel?.dispose();
    this.onShown.dispose();
    for (const s of this.subs) s.dispose();
  }
}
