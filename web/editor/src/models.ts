// One Monaco model per file. Switching files switches models; a model is
// never reused for another file's text. Files of the tree are synced with
// the language server under their real URIs; generated previews live under
// their own scheme and never reach it.
import { createSignal } from "solid-js";
import { monaco, LANG } from "./monaco.ts";
import type { LspClient } from "./lsp.ts";

export const GENERATED = "heron-generated";

export interface FileModel {
  path: string;
  model: monaco.editor.ITextModel;
  readOnly: boolean;
  /** The text on disk, as last read or saved. */
  saved: () => string;
  setSaved: (s: string) => void;
  dirty: () => boolean;
}

/**
 * The smallest single edit that turns a model's text into `next`: the
 * common prefix and suffix stay, so cursors and selections outside the
 * changed span don't move. Returns whether anything changed.
 */
export function mergeText(model: monaco.editor.ITextModel, next: string): boolean {
  const cur = model.getValue();
  if (cur === next) return false;
  let start = 0;
  const max = Math.min(cur.length, next.length);
  while (start < max && cur.charCodeAt(start) === next.charCodeAt(start)) start++;
  let endCur = cur.length;
  let endNext = next.length;
  while (endCur > start && endNext > start && cur.charCodeAt(endCur - 1) === next.charCodeAt(endNext - 1)) (endCur--, endNext--);
  const a = model.getPositionAt(start);
  const b = model.getPositionAt(endCur);
  model.pushEditOperations(
    [],
    [{ range: new monaco.Range(a.lineNumber, a.column, b.lineNumber, b.column), text: next.slice(start, endNext) }],
    () => null,
  );
  return true;
}

export class Models {
  private files = new Map<string, FileModel>();
  private readonly versionTick: () => number;
  private bump: (f: (n: number) => number) => void;

  constructor(
    private rootUri: string,
    private lsp: LspClient,
  ) {
    const [v, setV] = createSignal(0, { ownedWrite: true });
    this.versionTick = v;
    this.bump = setV;
  }

  /** Changes whenever any model's text or saved state changes. */
  tick() {
    return this.versionTick();
  }

  uriOf(path: string): monaco.Uri {
    return monaco.Uri.parse(this.rootUri.replace(/\/$/, "") + "/" + path.split("/").map(encodeURIComponent).join("/"));
  }

  pathOf(uri: string): string | null {
    const root = this.rootUri.replace(/\/$/, "") + "/";
    const u = monaco.Uri.parse(uri).toString();
    if (!u.startsWith(monaco.Uri.parse(root).toString())) return null;
    return decodeURIComponent(u.slice(monaco.Uri.parse(root).toString().length));
  }

  get(path: string): FileModel | undefined {
    return this.files.get(path);
  }

  all(): FileModel[] {
    return [...this.files.values()];
  }

  /** The unsaved texts of the tree's files, by path. */
  overlays(): Record<string, string> {
    this.tick();
    const out: Record<string, string> = {};
    for (const f of this.files.values()) if (!f.readOnly && f.dirty()) out[f.path] = f.model.getValue();
    return out;
  }

  /** Opens a file's model, creating it from text the first time. */
  open(path: string, text: string): FileModel {
    const have = this.files.get(path);
    if (have) return have;
    const uri = this.uriOf(path);
    const model = monaco.editor.getModel(uri) ?? monaco.editor.createModel(text, path.endsWith(".heron") || path.endsWith(".heron.md") ? LANG : undefined, uri);
    model.updateOptions({ tabSize: 2, insertSpaces: true });
    const [saved, setSaved] = createSignal(text, { ownedWrite: true });
    const fm: FileModel = {
      path,
      model,
      readOnly: false,
      saved,
      setSaved: (s) => (setSaved(s), this.bump((n) => n + 1)),
      dirty: () => (this.tick(), model.getValue() !== saved()),
    };
    this.files.set(path, fm);
    if (model.getLanguageId() === LANG) {
      this.lsp.open({ uri: uri.toString(), languageId: LANG, version: model.getVersionId(), text: () => model.getValue() });
    }
    model.onDidChangeContent((e) => {
      this.lsp.change(
        uri.toString(),
        model.getVersionId(),
        // Monaco's ranges all refer to the text before the event, while LSP
        // applies changes one after another: several at once go as the text.
        e.changes.length === 1
          ? e.changes.map((c) => ({
              range: {
                start: { line: c.range.startLineNumber - 1, character: c.range.startColumn - 1 },
                end: { line: c.range.endLineNumber - 1, character: c.range.endColumn - 1 },
              },
              text: c.text,
            }))
          : [{ text: model.getValue() }],
      );
      this.bump((n) => n + 1);
    });
    return fm;
  }

  /** A generated file's preview: read only, and never synced. */
  generated(path: string, text: string): FileModel {
    const key = GENERATED + ":" + path;
    const uri = monaco.Uri.from({ scheme: GENERATED, path: "/" + path });
    let fm = this.files.get(key);
    if (fm) {
      mergeText(fm.model, text);
      fm.setSaved(text);
      return fm;
    }
    const model = monaco.editor.getModel(uri) ?? monaco.editor.createModel(text, path.endsWith(".heron") || path.endsWith(".heron.md") ? LANG : path.endsWith(".ts") ? "typescript" : undefined, uri);
    const [saved, setSaved] = createSignal(text, { ownedWrite: true });
    fm = { path: key, model, readOnly: true, saved, setSaved, dirty: () => false };
    this.files.set(key, fm);
    return fm;
  }

  /** A file outside the workspace, such as a standard package's: read only. */
  external(uri: string, text: string): FileModel {
    const key = "external:" + uri;
    const have = this.files.get(key);
    if (have) return have;
    const u = monaco.Uri.parse(uri);
    const model = monaco.editor.getModel(u) ?? monaco.editor.createModel(text, LANG, u);
    const [saved, setSaved] = createSignal(text, { ownedWrite: true });
    const fm: FileModel = { path: key, model, readOnly: true, saved, setSaved, dirty: () => false };
    this.files.set(key, fm);
    return fm;
  }

  /** Applies text from the server, such as the file on disk, as an edit. */
  reload(path: string, text: string) {
    const f = this.files.get(path);
    if (!f) return;
    const wasClean = !f.dirty();
    if (wasClean) mergeText(f.model, text);
    f.setSaved(text);
  }
}
