// Generated from tokens.json by `go generate ./web/tokens`. Do not edit.

export const colors = {
  "bg-page": { dark: "#111111", light: "#ffffff" },
  "bg-raised": { dark: "#181818", light: "#fafafa" },
  "bg-soft": { dark: "#1f1f1f", light: "#f3f3f3" },
  "bg-highlight": { dark: "#292929", light: "#e9e9e9" },
  "bg-topbar": { dark: "rgba(17, 17, 17, 0.78)", light: "rgba(255, 255, 255, 0.8)" },
  "bg-button": { dark: "#262626", light: "#ededed" },
  "bg-button-hover": { dark: "#303030", light: "#e3e3e3" },
  "border-faint": { dark: "rgba(255, 255, 255, 0.06)", light: "rgba(0, 0, 0, 0.06)" },
  "border": { dark: "rgba(255, 255, 255, 0.10)", light: "rgba(0, 0, 0, 0.10)" },
  "border-strong": { dark: "rgba(255, 255, 255, 0.17)", light: "rgba(0, 0, 0, 0.17)" },
  "text": { dark: "#ededed", light: "#171717" },
  "text-secondary": { dark: "#a8a8a8", light: "#545454" },
  "text-faint": { dark: "#949494", light: "#666666" },
  "text-decor": { dark: "#4d4d4d", light: "#c2c2c2" },
  "text-on-primary": { dark: "#111111", light: "#ffffff" },
  "accent": { dark: "#a99cf7", light: "#5b45cc" },
  "accent-line": { dark: "rgba(169, 156, 247, 0.45)", light: "rgba(91, 69, 204, 0.4)" },
  "status-ok": { dark: "#4fc785", light: "#18794a" },
  "status-warn": { dark: "#e5a83b", light: "#9a5b05" },
  "status-error": { dark: "#f2777a", light: "#c4282b" },
  "status-info": { dark: "#6aa9f5", light: "#1f62c4" },
  "type": { dark: "#4ec6c0", light: "#0b7671" },
  "syntax-keyword": { dark: "#a99cf7", light: "#5b45cc" },
  "syntax-string": { dark: "#4fc785", light: "#18794a" },
  "syntax-number": { dark: "#e5a83b", light: "#9a5b05" },
  "syntax-type": { dark: "#4ec6c0", light: "#0b7671" },
  "syntax-annotation": { dark: "#6aa9f5", light: "#1f62c4" },
  "syntax-comment": { dark: "#949494", light: "#666666" },
  "syntax-punct": { dark: "#a8a8a8", light: "#545454" },
  "syntax-name": { dark: "#ededed", light: "#171717" },
  "syntax-error": { dark: "#f2777a", light: "#c4282b" },
  "method-get": { dark: "#6aa9f5", light: "#1f62c4" },
  "method-post": { dark: "#4fc785", light: "#18794a" },
  "method-put": { dark: "#e5a83b", light: "#9a5b05" },
  "method-patch": { dark: "#e5a83b", light: "#9a5b05" },
  "method-delete": { dark: "#f2777a", light: "#c4282b" },
  "shadow-overlay": { dark: "0 8px 28px rgba(0, 0, 0, 0.5), 0 0 0 1px rgba(255, 255, 255, 0.08)", light: "0 8px 28px rgba(0, 0, 0, 0.12), 0 0 0 1px rgba(0, 0, 0, 0.08)" },
  "bg-backdrop": { dark: "rgba(0, 0, 0, 0.55)", light: "rgba(0, 0, 0, 0.28)" },
  "dot-grid": { dark: "rgba(255, 255, 255, 0.07)", light: "rgba(0, 0, 0, 0.09)" },
} as const;

export const values = {
  "size": {
    "control-sm": "24px",
    "control": "28px",
    "control-lg": "32px",
    "row": "28px",
    "topbar": "44px",
    "sidebar": "248px",
    "aside": "300px",
    "measure": "570px",
    "border": "1px",
    "focus": "2px",
  },
  "radius": {
    "mark": "4px",
    "control": "6px",
    "card": "8px",
    "overlay": "10px",
  },
  "space": {
    "1": "4px",
    "2": "8px",
    "3": "12px",
    "4": "16px",
    "5": "20px",
    "6": "24px",
    "8": "32px",
    "10": "40px",
    "12": "48px",
    "16": "64px",
  },
  "font": {
    "sans": "Inter, ui-sans-serif, system-ui, -apple-system, \"Segoe UI\", Roboto, sans-serif",
    "mono": "\"JetBrains Mono\", ui-monospace, \"SF Mono\", Menlo, Consolas, monospace",
    "size-ui": "13px",
    "size-prose": "15px",
    "size-code": "12.5px",
    "size-label": "11px",
    "size-h1": "28px",
    "size-h2": "20px",
    "size-h3": "16px",
    "line-ui": "20px",
    "line-prose": "1.65",
    "line-code": "1.6",
    "weight-regular": "400",
    "weight-medium": "500",
    "weight-semibold": "600",
    "tracking-heading": "-0.012em",
    "tracking-label": "0.01em",
  },
  "motion": {
    "fast": "120ms",
    "base": "160ms",
    "slow": "200ms",
    "ease": "cubic-bezier(0.2, 0, 0, 1)",
  },
} as const;

export type ColorToken = keyof typeof colors;
export type Theme = "dark" | "light";

/** The CSS variable of a colour, such as cssVar("accent") → "var(--h-accent)". */
export function cssVar(name: ColorToken): string {
  return `var(--h-${name})`;
}

/** The CSS variable of a non-colour token, such as valueVar("radius", "card"). */
export function valueVar<G extends keyof typeof values>(group: G, name: keyof (typeof values)[G] & string): string {
  return `var(--h-${group}-${name})`;
}
