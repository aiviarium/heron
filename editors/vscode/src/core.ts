// What the extension decides without VS Code: which command to start, the
// arguments of each `heron` command, and the preview's page and policy.
import { accessSync, constants, statSync } from "node:fs";
import { delimiter, isAbsolute, join } from "node:path";

/** The built-in generation targets `heron generate` lists. */
export const generateTargets = ["ts", "docs", "agents", "asyncapi", "proto", "openrpc", "sparrow"];

/** The `heron` arguments of each command the extension runs in a task. */
export const cliCommands: Record<string, { title: string; args: string[] }> = {
  "heron.check": { title: "check", args: ["check"] },
  "heron.lint": { title: "lint", args: ["lint"] },
  "heron.generateDocs": { title: "generate docs", args: ["generate", "docs"] },
};

/** The arguments of Generate… with the targets picked; none runs them all. */
export function generateArgs(picked: string[]): string[] {
  return ["generate", ...picked];
}

/** The arguments the language client starts the command with. */
export const serverArgs = ["lsp", "--stdio"];

/** The command the extension runs: the heron.path setting, or heron. */
export function heronCommand(setting: string | undefined): string {
  const s = (setting ?? "").trim();
  return s === "" ? "heron" : s;
}

function executable(p: string): boolean {
  try {
    if (!statSync(p).isFile()) return false;
    accessSync(p, process.platform === "win32" ? constants.F_OK : constants.X_OK);
    return true;
  } catch {
    return false;
  }
}

/**
 * Resolves a command to the file it starts: a path as it is, a bare name
 * on PATH (with PATHEXT's extensions on Windows), or undefined.
 */
export function resolveCommand(cmd: string, env: NodeJS.ProcessEnv = process.env, cwd = process.cwd()): string | undefined {
  if (isAbsolute(cmd) || cmd.includes("/") || cmd.includes("\\")) {
    const p = isAbsolute(cmd) ? cmd : join(cwd, cmd);
    return executable(p) ? p : undefined;
  }
  const exts = process.platform === "win32" ? (env.PATHEXT ?? ".EXE;.CMD;.BAT").split(";").concat([""]) : [""];
  for (const dir of (env.PATH ?? "").split(delimiter)) {
    if (!dir) continue;
    for (const ext of exts) {
      const p = join(dir, cmd + ext);
      if (executable(p)) return p;
    }
  }
  return undefined;
}

/** The message shown when the command can't be started. */
export function notFoundMessage(cmd: string): string {
  return `Heron: can't start \`${cmd} lsp\`: no such command. Install heron, or set heron.path to the heron binary.`;
}

/** A nonce for one webview: 32 random characters. */
export function nonce(random: () => number = Math.random): string {
  const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789";
  let s = "";
  for (let i = 0; i < 32; i++) s += chars.charAt(Math.floor(random() * chars.length));
  return s;
}

/**
 * The preview's content security policy: nothing by default, its style
 * and its one script only by the nonce, images only from the extension's
 * own files and data: URLs, and no network.
 */
export function previewPolicy(n: string, cspSource: string): string {
  return [
    "default-src 'none'",
    `style-src 'nonce-${n}'`,
    `script-src 'nonce-${n}'`,
    `img-src ${cspSource} data:`,
    `font-src ${cspSource} data:`,
  ].join("; ");
}

/** The preview's script: it shows each page it's sent, keeps the last page under an error, and leaves links inert. */
const previewScript = `
const host = acquireVsCodeApi();
const page = document.getElementById("page");
const error = document.getElementById("error");
const theme = () => {
  const dark = document.body.classList.contains("vscode-dark") || document.body.classList.contains("vscode-high-contrast");
  document.documentElement.dataset.theme = dark ? "dark" : "light";
};
theme();
new MutationObserver(theme).observe(document.body, { attributes: true, attributeFilter: ["class"] });
document.addEventListener("click", (e) => {
  if (e.target instanceof Element && e.target.closest("a")) e.preventDefault();
});
window.addEventListener("message", (e) => {
  const m = e.data;
  if (m.error) {
    error.textContent = m.error;
    error.hidden = false;
    return;
  }
  error.hidden = true;
  page.innerHTML = m.html;
});
host.postMessage({ ready: true });
`;

/** The preview's page, its stylesheet and script under the nonce. */
export function previewPage(n: string, cspSource: string, styles: string): string {
  // A stylesheet can't end the style element it's in.
  const css = styles.replace(/<\/style/gi, "<\\/style");
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="${previewPolicy(n, cspSource)}">
<meta name="viewport" content="width=device-width, initial-scale=1">
<style nonce="${n}">${css}
body { padding: 0 16px 24px; }
#error { color: var(--h-status-error, #e5484d); border: 1px solid currentColor; border-radius: 6px; padding: 8px 12px; margin: 12px 0; white-space: pre-wrap; }
</style>
</head>
<body>
<div id="error" role="alert" hidden></div>
<main id="page"></main>
<script nonce="${n}">${previewScript}</script>
</body>
</html>`;
}
