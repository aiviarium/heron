// The code editor: one Monaco editor whose model is switched per file, with
// gutter marks beside the nodes they belong to.
import { createEffect, createMemo, onSettled } from "solid-js";
import { monaco, editorOptions, setupMonaco } from "./monaco.ts";

export interface Mark {
  line: number;
  status?: "ok" | "failed" | "running";
  label?: string;
  title?: string;
  action?: () => void;
}

export interface EditorProps {
  model: monaco.editor.ITextModel | null;
  readOnly: boolean;
  theme: "dark" | "light";
  marks: Mark[];
  /** Where to put the cursor after switching, once. */
  reveal?: { path: string; line: number; column: number } | null;
  onReady?: (ed: monaco.editor.IStandaloneCodeEditor) => void;
  onCursor?: (line: number, column: number, offset: number) => void;
}

export function Editor(props: EditorProps) {
  let host!: HTMLDivElement;
  let ed: monaco.editor.IStandaloneCodeEditor | null = null;
  let decorations: monaco.editor.IEditorDecorationsCollection | null = null;
  let marks: Mark[] = [];
  const viewStates = new Map<string, monaco.editor.ICodeEditorViewState | null>();

  onSettled(() => {
    setupMonaco();
    ed = monaco.editor.create(host, { ...editorOptions, model: null, theme: props.theme === "dark" ? "heron-dark" : "heron-light" });
    decorations = ed.createDecorationsCollection();
    ed.onDidChangeCursorPosition((e) => {
      const m = ed!.getModel();
      if (m) props.onCursor?.(e.position.lineNumber, e.position.column, m.getOffsetAt(e.position));
    });
    // Tokenize edited lines at once: auto-closing and the Enter rules read
    // the line's tokens, and the background tokenizer can lag behind fast
    // typing, which would skip them.
    ed.onDidChangeModelContent((e) => {
      const m = ed!.getModel();
      if (!m) return;
      let last = 1;
      for (const c of e.changes) last = Math.max(last, c.range.startLineNumber + c.text.split("\n").length - 1);
      (m as unknown as { tokenization?: { forceTokenization(n: number): void } }).tokenization?.forceTokenization(Math.min(last + 1, m.getLineCount()));
    });
    ed.onMouseDown((e) => {
      if (e.target.type !== monaco.editor.MouseTargetType.GUTTER_GLYPH_MARGIN) return;
      const line = e.target.position?.lineNumber;
      const mk = marks.find((m) => m.line === line && m.action);
      mk?.action?.();
    });
    props.onReady?.(ed);
    apply(props.model);
    paint(props.marks);
    return () => {
      ed?.dispose();
      ed = null;
    };
  });

  const apply = (model: monaco.editor.ITextModel | null) => {
    if (!ed) return;
    const prev = ed.getModel();
    if (prev === model) return;
    if (prev) viewStates.set(prev.uri.toString(), ed.saveViewState());
    ed.setModel(model);
    if (model) {
      const vs = viewStates.get(model.uri.toString());
      if (vs) ed.restoreViewState(vs);
      const p = ed.getPosition();
      if (p) props.onCursor?.(p.lineNumber, p.column, model.getOffsetAt(p));
    }
  };

  const paint = (ms: Mark[]) => {
    marks = ms;
    if (!decorations || !ed) return;
    const model = ed.getModel();
    if (!model) return;
    decorations.set(
      ms.filter((m) => m.line <= model.getLineCount()).map((m) => ({
        range: new monaco.Range(m.line, 1, m.line, model.getLineMaxColumn(m.line)),
        options: {
          glyphMarginClassName: ["h-glyph", m.status ? `h-glyph-${m.status}` : "h-glyph-action"].join(" "),
          glyphMarginHoverMessage: m.title ? { value: m.title } : undefined,
          after: m.label ? { content: "  " + m.label, inlineClassName: `h-mark-label h-mark-${m.status ?? "none"}` } : undefined,
          stickiness: monaco.editor.TrackedRangeStickiness.NeverGrowsWhenTypingAtEdges,
        },
      })),
    );
  };

  // Effects re-run whenever their inputs re-run, so each reads a memo:
  // the editor is touched only when a value really changes.
  const model = createMemo(() => props.model);
  const readOnly = createMemo(() => props.readOnly);
  const theme = createMemo(() => props.theme);
  createEffect(
    () => model(),
    (m) => apply(m),
  );
  createEffect(
    () => props.marks,
    (ms) => paint(ms),
  );
  createEffect(
    () => readOnly(),
    (ro) => ed?.updateOptions({ readOnly: ro }),
  );
  createEffect(
    () => theme(),
    (th) => monaco.editor.setTheme(th === "dark" ? "heron-dark" : "heron-light"),
  );
  createEffect(
    () => props.reveal,
    (r) => {
      if (!r) return;
      // After the model switch the same change may bring.
      setTimeout(() => {
        if (!ed) return;
        ed.setPosition({ lineNumber: r.line, column: r.column });
        ed.revealLineInCenterIfOutsideViewport(r.line);
        ed.focus();
      });
    },
  );

  return <div class="h-editor" ref={host} data-testid="editor" />;
}
