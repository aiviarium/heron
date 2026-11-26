// Monaco's language features for Heron, answered by the language server:
// diagnostics as markers, completion, hover, definition, rename, code
// actions for fixes, formatting and semantic tokens.
import { monaco, LANGS } from "./monaco.ts";
import type { LspClient } from "./lsp.ts";
import { t, type Key } from "@heron/strings";

type M = typeof monaco;
type LPos = { line: number; character: number };
type LRange = { start: LPos; end: LPos };

const toPos = (p: monaco.IPosition): LPos => ({ line: p.lineNumber - 1, character: p.column - 1 });
const toRange = (r: LRange): monaco.IRange => ({
  startLineNumber: r.start.line + 1,
  startColumn: r.start.character + 1,
  endLineNumber: r.end.line + 1,
  endColumn: r.end.character + 1,
});
const lspRange = (r: monaco.IRange): LRange => ({
  start: { line: r.startLineNumber - 1, character: r.startColumn - 1 },
  end: { line: r.endLineNumber - 1, character: r.endColumn - 1 },
});

/** The name of a diagnostic's source as the interface shows it. */
const sources: Record<string, Key> = {
  "heron check": "app.source.check",
  check: "app.source.check",
  "heron lint": "app.source.lint",
  lint: "app.source.lint",
  vale: "app.source.vale",
  spec: "app.source.spec",
};

export function sourceName(source: string | undefined): string {
  if (!source) return t("app.source.lsp");
  const key = sources[source.toLowerCase()];
  return key ? t(key) : source;
}

/**
 * Whether a position is in prose (a `|` line or a `{{ }}` body) and not in
 * a link target or an insert, where completion would only get in the way.
 */
export function inProse(model: monaco.editor.ITextModel, pos: monaco.IPosition): boolean {
  const line = model.getLineContent(pos.lineNumber);
  const before = line.slice(0, pos.column - 1);
  if (/(\(#|\$)[\w.]*$/.test(before)) return false;
  if (/^\s*\|/.test(line) || /=\|/.test(before)) return true;
  // Inside an unclosed {{ above the cursor?
  const text = model.getValueInRange({ startLineNumber: Math.max(1, pos.lineNumber - 200), startColumn: 1, endLineNumber: pos.lineNumber, endColumn: pos.column });
  const open = text.lastIndexOf("{{");
  return open >= 0 && text.indexOf("}}", open) < 0;
}

const kinds: Record<number, monaco.languages.CompletionItemKind> = {};

function docText(d: any): string | monaco.IMarkdownString | undefined {
  if (!d) return undefined;
  if (typeof d === "string") return d;
  return { value: d.value ?? String(d) };
}

function edit(e: any): monaco.languages.TextEdit {
  return { range: toRange(e.range), text: e.newText };
}

/** A workspace edit as Monaco's, by document URI. */
export function workspaceEdit(m: M, we: any): monaco.languages.WorkspaceEdit {
  const edits: monaco.languages.IWorkspaceTextEdit[] = [];
  for (const [uri, es] of Object.entries<any[]>(we?.changes ?? {})) {
    for (const e of es) edits.push({ resource: m.Uri.parse(uri), textEdit: edit(e), versionId: undefined });
  }
  for (const dc of we?.documentChanges ?? []) {
    if (!dc.textDocument) continue;
    for (const e of dc.edits) edits.push({ resource: m.Uri.parse(dc.textDocument.uri), textEdit: edit(e), versionId: undefined });
  }
  return { edits };
}

export interface ProviderHooks {
  /** Makes sure a model exists for a URI, such as a definition's target. */
  ensureModel: (uri: string) => Promise<void>;
}

/** Registers the providers once; they answer for every Heron model. */
export function registerProviders(m: M, lsp: LspClient, hooks: ProviderHooks): monaco.IDisposable[] {
  const uri = (model: monaco.editor.ITextModel) => model.uri.toString();
  const synced = (model: monaco.editor.ITextModel) => lsp.isOpen(uri(model));
  const ds: monaco.IDisposable[] = [];

  lsp.on("textDocument/publishDiagnostics", (p: { uri: string; diagnostics: any[] }) => {
    const model = m.editor.getModel(m.Uri.parse(p.uri));
    if (!model) return;
    const sev = [m.MarkerSeverity.Error, m.MarkerSeverity.Error, m.MarkerSeverity.Warning, m.MarkerSeverity.Info, m.MarkerSeverity.Hint];
    m.editor.setModelMarkers(
      model,
      "heron",
      p.diagnostics.map((d) => ({
        ...toRange(d.range),
        severity: sev[d.severity ?? 1],
        message: d.message,
        source: sourceName(d.source),
        code: d.code === undefined ? undefined : String(d.code),
        tags: d.tags,
      })),
    );
  });

  ds.push(
    m.languages.registerCompletionItemProvider(LANGS, {
      triggerCharacters: [".", "@", "<", "#", "$", "(", ":"],
      async provideCompletionItems(model, pos, ctx) {
        if (!synced(model) || inProse(model, pos)) return { suggestions: [] };
        const res: any = await lsp.request("textDocument/completion", {
          textDocument: { uri: uri(model) },
          position: toPos(pos),
          context: { triggerKind: ctx.triggerKind + 1, triggerCharacter: ctx.triggerCharacter },
        }).catch(() => null);
        const items: any[] = Array.isArray(res) ? res : (res?.items ?? []);
        const word = model.getWordUntilPosition(pos);
        const range = { startLineNumber: pos.lineNumber, endLineNumber: pos.lineNumber, startColumn: word.startColumn, endColumn: word.endColumn };
        return {
          incomplete: !!res?.isIncomplete,
          suggestions: items.map((it) => ({
            label: it.label,
            kind: kinds[it.kind] ?? ((it.kind ?? 1) - 1),
            detail: it.detail,
            documentation: docText(it.documentation),
            insertText: it.textEdit?.newText ?? it.insertText ?? it.label,
            insertTextRules: it.insertTextFormat === 2 ? m.languages.CompletionItemInsertTextRule.InsertAsSnippet : undefined,
            range: it.textEdit?.range ? toRange(it.textEdit.range) : it.textEdit?.insert ? toRange(it.textEdit.insert) : range,
            sortText: it.sortText,
            filterText: it.filterText,
          })),
        };
      },
    }),
    m.languages.registerHoverProvider(LANGS, {
      async provideHover(model, pos) {
        if (!synced(model)) return null;
        const res: any = await lsp.request("textDocument/hover", { textDocument: { uri: uri(model) }, position: toPos(pos) }).catch(() => null);
        if (!res?.contents) return null;
        const cs = Array.isArray(res.contents) ? res.contents : [res.contents];
        return {
          range: res.range ? toRange(res.range) : undefined,
          contents: cs.map((c: any) => (typeof c === "string" ? { value: c } : c.language ? { value: "```" + c.language + "\n" + c.value + "\n```" } : { value: c.value })),
        };
      },
    }),
    m.languages.registerDefinitionProvider(LANGS, {
      async provideDefinition(model, pos) {
        if (!synced(model)) return null;
        const res: any = await lsp.request("textDocument/definition", { textDocument: { uri: uri(model) }, position: toPos(pos) }).catch(() => null);
        if (!res) return null;
        const locs: any[] = Array.isArray(res) ? res : [res];
        const out: monaco.languages.Location[] = [];
        for (const l of locs) {
          const target = l.targetUri ?? l.uri;
          await hooks.ensureModel(target);
          out.push({ uri: m.Uri.parse(target), range: toRange(l.targetSelectionRange ?? l.targetRange ?? l.range) });
        }
        return out;
      },
    }),
    m.languages.registerRenameProvider(LANGS, {
      async provideRenameEdits(model, pos, newName) {
        const res: any = await lsp.request("textDocument/rename", { textDocument: { uri: uri(model) }, position: toPos(pos), newName });
        const we = workspaceEdit(m, res);
        for (const e of we.edits) await hooks.ensureModel((e as monaco.languages.IWorkspaceTextEdit).resource.toString());
        return we;
      },
      async resolveRenameLocation(model, pos) {
        const res: any = await lsp.request("textDocument/prepareRename", { textDocument: { uri: uri(model) }, position: toPos(pos) }).catch(() => undefined);
        const word = model.getWordAtPosition(pos);
        if (res === null) return { range: new m.Range(1, 1, 1, 1), text: "", rejectReason: "" };
        if (res?.range) return { range: toRange(res.range), text: res.placeholder ?? model.getValueInRange(toRange(res.range)) };
        if (res?.start) return { range: toRange(res), text: model.getValueInRange(toRange(res)) };
        if (!word) return { range: new m.Range(1, 1, 1, 1), text: "", rejectReason: "" };
        return { range: new m.Range(pos.lineNumber, word.startColumn, pos.lineNumber, word.endColumn), text: word.word };
      },
    }),
    m.languages.registerCodeActionProvider(LANGS, {
      async provideCodeActions(model, range, ctx) {
        if (!synced(model)) return { actions: [], dispose() {} };
        const diagnostics = ctx.markers.map((mk) => ({
          range: lspRange(mk),
          message: mk.message,
          severity: mk.severity === m.MarkerSeverity.Error ? 1 : mk.severity === m.MarkerSeverity.Warning ? 2 : 3,
          code: typeof mk.code === "string" ? mk.code : mk.code?.value,
        }));
        const res: any[] = (await lsp.request("textDocument/codeAction", { textDocument: { uri: uri(model) }, range: lspRange(range), context: { diagnostics } }).catch(() => null)) ?? [];
        return {
          actions: res
            .filter((a) => a.edit || a.command)
            .map((a) => ({
              title: a.title,
              kind: a.kind ?? "quickfix",
              isPreferred: a.isPreferred,
              edit: a.edit ? workspaceEdit(m, a.edit) : undefined,
              diagnostics: ctx.markers,
            })),
          dispose() {},
        };
      },
    }),
    m.languages.registerDocumentFormattingEditProvider(LANGS, {
      async provideDocumentFormattingEdits(model, opts) {
        const res: any[] = (await lsp.request("textDocument/formatting", { textDocument: { uri: uri(model) }, options: { tabSize: opts.tabSize, insertSpaces: opts.insertSpaces } }).catch(() => null)) ?? [];
        return res.map(edit);
      },
    }),
  );

  // Semantic tokens, registered once a server says it has them, with its
  // legend.
  let semantic = false;
  const onReady = async () => {
    for (;;) {
      await lsp.whenReady();
      const cap = lsp.capabilities.semanticTokensProvider;
      if (cap?.legend && !semantic) {
        semantic = true;
        const legend = cap.legend as monaco.languages.SemanticTokensLegend;
        ds.push(
          m.languages.registerDocumentSemanticTokensProvider(LANGS, {
            getLegend: () => legend,
            async provideDocumentSemanticTokens(model) {
              if (!synced(model)) return null;
              const res: any = await lsp.request("textDocument/semanticTokens/full", { textDocument: { uri: uri(model) } }).catch(() => null);
              return res ? { data: new Uint32Array(res.data), resultId: res.resultId } : null;
            },
            releaseDocumentSemanticTokens() {},
          }),
        );
      }
      await lsp.whenLost();
    }
  };
  void onReady();
  return ds;
}
