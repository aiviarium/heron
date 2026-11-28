// Runs the end-to-end suite in a real VS Code, downloaded by
// @vscode/test-electron, on a copy of test/fixture/tree, with the heron
// binary HERON_BIN names (or heron on PATH) as the language server.
import { runTests } from "@vscode/test-electron";
import { cpSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join, resolve } from "node:path";

async function main() {
  const root = resolve(__dirname, "../..");
  const tree = mkdtempSync(join(tmpdir(), "heron-vscode-"));
  cpSync(join(root, "test/fixture/tree"), tree, { recursive: true });
  const env: Record<string, string> = { HERON_E2E: "1" };
  if (process.env.HERON_BIN) env.PATH = dirname(resolve(process.env.HERON_BIN)) + delimiter + (process.env.PATH ?? "");
  await runTests({
    version: process.env.VSCODE_VERSION ?? "1.91.1",
    extensionDevelopmentPath: root,
    extensionTestsPath: join(__dirname, "suite.js"),
    extensionTestsEnv: env,
    launchArgs: [tree, "--disable-extensions", "--disable-workspace-trust", "--skip-welcome", "--skip-release-notes", "--disable-gpu"],
  });
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});
