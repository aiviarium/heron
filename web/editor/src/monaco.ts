// Monaco set up for Heron: the language, its configuration (brackets with
// `{{ }}`, auto-closing, indentation and Enter rules at the formatter's two
// spaces), a static grammar, and themes coloured from the design tokens.
import * as monaco from "monaco-editor/editor";
import "monaco-editor/features/register.all";
import EditorWorker from "monaco-editor/editor/editor.worker.js?worker";
import { colors, type Theme } from "@heron/tokens";

export { monaco };

(self as unknown as { MonacoEnvironment: monaco.Environment }).MonacoEnvironment = {
  getWorker: () => new EditorWorker(),
};

export const LANG = "heron";

/** The formatter's indent. */
export const INDENT = 2;

export const languageConfiguration: monaco.languages.LanguageConfiguration = {
  comments: { lineComment: "//" },
  brackets: [
    ["{{", "}}"],
    ["{", "}"],
    ["[", "]"],
    ["(", ")"],
  ],
  autoClosingPairs: [
    { open: "{", close: "}" },
    { open: "[", close: "]" },
    { open: "(", close: ")" },
    { open: '"', close: '"', notIn: ["string", "comment"] },
    { open: "`", close: "`", notIn: ["string", "comment"] },
  ],
  autoCloseBefore: ";:.,=}])> \n\t",
  surroundingPairs: [
    { open: "{", close: "}" },
    { open: "[", close: "]" },
    { open: "(", close: ")" },
    { open: '"', close: '"' },
    { open: "`", close: "`" },
  ],
  indentationRules: {
    // A line that opens a body, list or argument list, `{{` included.
    increaseIndentPattern: /^.*(\{|\[|\()\s*(\/\/.*)?$/,
    // A line that starts with a closer, `}}` included.
    decreaseIndentPattern: /^\s*(\}|\]|\)).*$/,
  },
  onEnterRules: [
    {
      // Between an opener and its closer: indent the new line, outdent the closer.
      beforeText: /^.*(\{|\[|\()\s*$/,
      afterText: /^\s*(\}|\]|\))/,
      action: { indentAction: monaco.languages.IndentAction.IndentOutdent },
    },
    {
      beforeText: /^.*(\{|\[|\()\s*(\/\/.*)?$/,
      action: { indentAction: monaco.languages.IndentAction.Indent },
    },
  ],
  wordPattern: /[A-Za-z_][A-Za-z0-9_]*/,
};

// The static grammar. Prose tokens are named `string.prose` so Monaco
// treats them as text for quick suggestions, but they're coloured as body
// text, never as comments.
const grammar: monaco.languages.IMonarchLanguage = {
  defaultToken: "",
  keywords: ["import", "include", "do", "let", "if", "else", "for", "in", "return", "true", "false", "null", "and", "or", "not", "match"],
  tokenizer: {
    root: [
      [/^\s*\/\/\/.*$/, "comment.doc"],
      [/\/\/.*$/, "comment"],
      [/^\s*\|/, { token: "delimiter.prose", next: "@proseLine" }],
      [/=\|/, { token: "delimiter.prose", next: "@proseLine" }],
      [/\{\{/, { token: "delimiter.prose", next: "@proseBlock" }],
      [/@[A-Za-z_][\w.]*/, "annotation"],
      [/"/, { token: "string.quote", next: "@string" }],
      [/`/, { token: "string.quote", next: "@code" }],
      [/\d+(\.\d+)?([a-z]+)?/, "number"],
      [/[A-Z][\w]*/, "type"],
      [/[a-z_][\w]*/, { cases: { "@keywords": "keyword", "@default": "identifier" } }],
      [/[{}()[\]<>]/, "delimiter.bracket"],
      [/[=:,.?|$#]/, "delimiter"],
    ],
    string: [
      [/[^\\"$]+/, "string"],
      [/\\./, "string.escape"],
      [/\$\{/, { token: "delimiter.interp", next: "@interp" }],
      [/\$/, "string"],
      [/"/, { token: "string.quote", next: "@pop" }],
    ],
    interp: [
      [/\}/, { token: "delimiter.interp", next: "@pop" }],
      { include: "root" },
    ],
    code: [
      [/[^`]+/, "string.code"],
      [/`/, { token: "string.quote", next: "@pop" }],
    ],
    // A `|` line ends with its line: Monarch carries state across lines, so
    // the next line's start pops it before anything else is read.
    proseLine: [
      [/^/, { token: "", next: "@pop" }],
      { include: "@proseInline" },
    ],
    proseBlock: [
      [/\}\}/, { token: "delimiter.prose", next: "@pop" }],
      { include: "@proseInline" },
    ],
    proseInline: [
      [/`[^`]*`/, "string.code"],
      [/\[[^\]]*\]\([^)]*\)/, "link"],
      [/\$[A-Za-z_][\w.]*/, "insert"],
      [/<\/?[a-z][\w-]*[^>]*>/, "tag"],
      [/[^`[$<}]+/, "string.prose"],
      [/./, "string.prose"],
    ],
  },
};

function hex(c: string): string {
  if (c.startsWith("#")) return c;
  const m = c.match(/rgba?\(([^)]+)\)/);
  if (!m) return c;
  const [r, g, b, a = "1"] = m[1].split(",").map((s) => s.trim());
  const h = (n: number) => Math.round(n).toString(16).padStart(2, "0");
  return "#" + h(+r) + h(+g) + h(+b) + h(+a * 255);
}

/** A colour with an alpha, as Monaco's #rrggbbaa. */
function alpha(c: string, a: number): string {
  return hex(c).slice(0, 7) + Math.round(a * 255).toString(16).padStart(2, "0");
}

function theme(mode: Theme): monaco.editor.IStandaloneThemeData {
  const c = (name: keyof typeof colors) => hex(colors[name][mode]);
  const fg = (name: keyof typeof colors) => c(name).slice(1, 7);
  return {
    base: mode === "dark" ? "vs-dark" : "vs",
    inherit: false,
    rules: [
      { token: "", foreground: fg("text") },
      { token: "identifier", foreground: fg("syntax-name") },
      { token: "keyword", foreground: fg("syntax-keyword") },
      { token: "type", foreground: fg("syntax-type") },
      { token: "annotation", foreground: fg("syntax-annotation") },
      { token: "number", foreground: fg("syntax-number") },
      { token: "string", foreground: fg("syntax-string") },
      { token: "string.quote", foreground: fg("syntax-string") },
      { token: "string.escape", foreground: fg("syntax-number") },
      { token: "string.code", foreground: fg("syntax-string") },
      { token: "string.prose", foreground: fg("text") },
      { token: "link", foreground: fg("accent") },
      { token: "insert", foreground: fg("accent") },
      { token: "tag", foreground: fg("syntax-annotation") },
      { token: "comment", foreground: fg("syntax-comment") },
      { token: "comment.doc", foreground: fg("text-secondary") },
      { token: "delimiter", foreground: fg("syntax-punct") },
      { token: "delimiter.bracket", foreground: fg("syntax-punct") },
      { token: "delimiter.prose", foreground: fg("text-faint") },
      { token: "delimiter.interp", foreground: fg("syntax-keyword") },
      // Semantic tokens from the language server.
      { token: "namespace", foreground: fg("syntax-name") },
      { token: "class", foreground: fg("syntax-type") },
      { token: "enum", foreground: fg("syntax-type") },
      { token: "enumMember", foreground: fg("syntax-name") },
      { token: "parameter", foreground: fg("syntax-name") },
      { token: "variable", foreground: fg("syntax-name") },
      { token: "property", foreground: fg("syntax-name") },
      { token: "function", foreground: fg("syntax-name") },
      { token: "method", foreground: fg("syntax-name") },
      { token: "decorator", foreground: fg("syntax-annotation") },
      { token: "macro", foreground: fg("syntax-annotation") },
      { token: "operator", foreground: fg("syntax-punct") },
    ],
    colors: {
      "editor.background": c("bg-page"),
      "editor.foreground": c("text"),
      "editorLineNumber.foreground": c("text-decor"),
      "editorLineNumber.activeForeground": c("text-faint"),
      "editorCursor.foreground": c("text"),
      // A selection that stands out; faint occurrences and current line.
      "editor.selectionBackground": alpha(colors.accent[mode], mode === "dark" ? 0.38 : 0.28),
      "editor.inactiveSelectionBackground": alpha(colors.accent[mode], 0.18),
      "editor.selectionHighlightBackground": alpha(colors.accent[mode], 0.1),
      "editor.wordHighlightBackground": alpha(colors.text[mode], 0.06),
      "editor.wordHighlightStrongBackground": alpha(colors.text[mode], 0.08),
      "editor.wordHighlightBorder": "#00000000",
      "editor.wordHighlightStrongBorder": "#00000000",
      "editor.lineHighlightBackground": alpha(colors.text[mode], 0.035),
      "editor.lineHighlightBorder": "#00000000",
      "editor.findMatchBackground": alpha(colors["status-warn"][mode], 0.35),
      "editor.findMatchHighlightBackground": alpha(colors["status-warn"][mode], 0.15),
      // Brackets keep the punctuation colour: no pair colourization.
      ...Object.fromEntries([1, 2, 3, 4, 5, 6].map((i) => [`editorBracketHighlight.foreground${i}`, c("syntax-punct")])),
      "editorBracketHighlight.unexpectedBracket.foreground": c("syntax-error"),
      "editorBracketMatch.background": alpha(colors.text[mode], 0.08),
      "editorBracketMatch.border": "#00000000",
      "editorIndentGuide.background1": alpha(colors.text[mode], 0.05),
      "editorIndentGuide.activeBackground1": alpha(colors.text[mode], 0.1),
      "editorWhitespace.foreground": "#00000000",
      "editorGutter.background": c("bg-page"),
      "editorError.foreground": c("status-error"),
      "editorWarning.foreground": c("status-warn"),
      "editorInfo.foreground": c("status-info"),
      "editorHint.foreground": c("text-faint"),
      "editorWidget.background": c("bg-raised"),
      "editorWidget.foreground": c("text"),
      "editorWidget.border": c("border-strong"),
      "editorHoverWidget.background": c("bg-raised"),
      "editorHoverWidget.border": c("border-strong"),
      "editorHoverWidget.foreground": c("text"),
      "editorSuggestWidget.background": c("bg-raised"),
      "editorSuggestWidget.border": c("border-strong"),
      "editorSuggestWidget.foreground": c("text"),
      "editorSuggestWidget.selectedBackground": c("bg-highlight"),
      "editorSuggestWidget.highlightForeground": c("accent"),
      "editorSuggestWidget.focusHighlightForeground": c("accent"),
      "list.hoverBackground": c("bg-soft"),
      "list.activeSelectionBackground": c("bg-highlight"),
      "list.activeSelectionForeground": c("text"),
      "list.highlightForeground": c("accent"),
      "focusBorder": c("accent"),
      "input.background": c("bg-soft"),
      "input.border": c("border"),
      "textLink.foreground": c("accent"),
      "textLink.activeForeground": c("accent"),
      "scrollbarSlider.background": alpha(colors.text[mode], 0.1),
      "scrollbarSlider.hoverBackground": alpha(colors.text[mode], 0.16),
      "scrollbarSlider.activeBackground": alpha(colors.text[mode], 0.22),
      "scrollbar.shadow": "#00000000",
      "editorOverviewRuler.border": "#00000000",
      "editorStickyScroll.background": c("bg-page"),
      "editorStickyScrollHover.background": c("bg-soft"),
      "peekViewEditor.background": c("bg-raised"),
      "peekViewResult.background": c("bg-raised"),
      "peekViewTitle.background": c("bg-soft"),
      "peekView.border": c("accent-line"),
    },
  };
}

let registered = false;

/** Registers the language and both themes, once. */
export function setupMonaco(): typeof monaco {
  if (registered) return monaco;
  registered = true;
  monaco.languages.register({ id: LANG, extensions: [".heron"], aliases: ["Heron"] });
  monaco.languages.setLanguageConfiguration(LANG, languageConfiguration);
  monaco.languages.setMonarchTokensProvider(LANG, grammar);
  monaco.editor.defineTheme("heron-dark", theme("dark"));
  monaco.editor.defineTheme("heron-light", theme("light"));
  return monaco;
}

/** The options every Heron editor uses. */
export const editorOptions: monaco.editor.IStandaloneEditorConstructionOptions = {
  automaticLayout: true,
  fontFamily: '"JetBrains Mono", ui-monospace, "SF Mono", Menlo, Consolas, monospace',
  fontSize: 12.5,
  lineHeight: 20,
  tabSize: INDENT,
  insertSpaces: true,
  detectIndentation: false,
  autoIndent: "full",
  formatOnType: false,
  renderWhitespace: "none",
  bracketPairColorization: { enabled: false },
  guides: { bracketPairs: false, indentation: true, highlightActiveIndentation: false },
  matchBrackets: "near",
  renderLineHighlight: "line",
  occurrencesHighlight: "singleFile",
  selectionHighlight: true,
  // No completion while writing prose or strings, and none from words.
  quickSuggestions: { other: true, comments: false, strings: false },
  wordBasedSuggestions: "off",
  suggestOnTriggerCharacters: true,
  acceptSuggestionOnEnter: "smart",
  glyphMargin: true,
  folding: true,
  showFoldingControls: "mouseover",
  lineNumbersMinChars: 3,
  minimap: { enabled: false },
  scrollBeyondLastLine: false,
  stickyScroll: { enabled: false },
  overviewRulerBorder: false,
  hideCursorInOverviewRuler: true,
  scrollbar: { verticalScrollbarSize: 10, horizontalScrollbarSize: 10, useShadows: false },
  padding: { top: 8, bottom: 8 },
  fixedOverflowWidgets: true,
  "semanticHighlighting.enabled": true,
  hover: { delay: 300 },
  unicodeHighlight: { ambiguousCharacters: false },
};
