// Builds the extension: the TextMate grammars copied from
// grammar/textmate, where internal/tools/textmate publishes them, and two
// esbuild bundles, dist/extension.js for the desktop and dist/web.js for
// the browser. With --tests it also bundles the tests into out/.
import { build } from "esbuild";
import { copyFileSync, mkdirSync, readdirSync, rmSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const grammars = join(here, "../../grammar/textmate");

mkdirSync(join(here, "syntaxes"), { recursive: true });
for (const name of ["heron.tmLanguage.json", "heron-md.tmLanguage.json"]) {
  copyFileSync(join(grammars, name), join(here, "syntaxes", name));
}

const common = { bundle: true, sourcemap: false, logLevel: "warning", target: "es2022" };
await build({
  ...common,
  entryPoints: [join(here, "src/extension.ts")],
  outfile: join(here, "dist/extension.js"),
  platform: "node",
  format: "cjs",
  external: ["vscode"],
  minify: true,
});
await build({
  ...common,
  entryPoints: [join(here, "src/web.ts")],
  outfile: join(here, "dist/web.js"),
  platform: "browser",
  format: "cjs",
  external: ["vscode"],
  minify: true,
});

if (process.argv.includes("--tests")) {
  rmSync(join(here, "out"), { recursive: true, force: true });
  const unit = readdirSync(join(here, "test/unit")).filter((f) => f.endsWith(".test.ts"));
  await build({
    ...common,
    entryPoints: unit.map((f) => join(here, "test/unit", f)),
    outdir: join(here, "out/unit"),
    outExtension: { ".js": ".cjs" },
    platform: "node",
    format: "cjs",
  });
  await build({
    ...common,
    entryPoints: [join(here, "test/e2e/run.ts"), join(here, "test/e2e/suite.ts")],
    outdir: join(here, "out/e2e"),
    platform: "node",
    format: "cjs",
    external: ["vscode", "@vscode/test-electron"],
  });
}
