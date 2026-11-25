// The tools' interface strings: one catalogue, strings.json, shared by the
// app, the docs and Go (package uistrings). Visible text in the tools comes
// from here or from the spec, and a test checks it.
import catalogue from "../strings.json" with { type: "json" };

export type Key = string;

function flatten(prefix: string, v: unknown, out: Record<string, string>) {
  if (typeof v === "string") out[prefix] = v;
  else if (v && typeof v === "object")
    for (const [k, x] of Object.entries(v)) if (!k.startsWith("$")) flatten(prefix ? `${prefix}.${k}` : k, x, out);
  return out;
}

export const strings: Readonly<Record<string, string>> = flatten("", catalogue, {});

/** The string for key, with `{name}` placeholders filled from vars. A missing key throws. */
export function t(key: Key, vars?: Record<string, string | number>): string {
  let s = strings[key];
  if (s === undefined) throw new Error(`no string for ${key}`);
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replaceAll(`{${k}}`, String(v));
  return s;
}

/** Whether a key exists. */
export const has = (key: string) => key in strings;

/** Every string's fixed parts, for the test of visible text. */
export function fragments(): string[] {
  const out = new Set<string>();
  for (const s of Object.values(strings)) for (const p of s.split(/\{[a-z_]+\}/)) if (p.trim()) out.add(p.trim());
  return [...out];
}
