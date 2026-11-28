// The extension's parts that need no VS Code: its manifest, its language
// configuration, its grammars, the commands it runs and the preview's page.
import { test } from "node:test";
import assert from "node:assert/strict";
import { chmodSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { cliCommands, generateArgs, generateTargets, heronCommand, notFoundMessage, previewPage, previewPolicy, resolveCommand, serverArgs } from "../../src/core";

const root = join(__dirname, "../..");
const manifest = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));

test("languages: heron for .heron and heron-md for .heron.md, each with its grammar", () => {
  const langs = Object.fromEntries(manifest.contributes.languages.map((l: any) => [l.id, l]));
  assert.deepEqual(langs.heron.extensions, [".heron"]);
  assert.deepEqual(langs["heron-md"].extensions, [".heron.md"]);
  const grammars = Object.fromEntries(manifest.contributes.grammars.map((g: any) => [g.language, g]));
  assert.equal(grammars.heron.scopeName, "source.heron");
  assert.equal(grammars["heron-md"].scopeName, "text.heron.markdown");
  assert.deepEqual(grammars["heron-md"].embeddedLanguages, { "meta.embedded.block.heron": "heron" });
});

test("grammars: the extension's are the published ones, as the generator writes them", () => {
  for (const name of ["heron.tmLanguage.json", "heron-md.tmLanguage.json"]) {
    const built = readFileSync(join(root, "syntaxes", name), "utf8");
    const published = readFileSync(join(root, "../../grammar/textmate", name), "utf8");
    assert.equal(built, published, `${name} differs from grammar/textmate`);
  }
  const md = readFileSync(join(root, "syntaxes/heron-md.tmLanguage.json"), "utf8");
  assert.match(md, /"contentName": "meta\.embedded\.block\.heron"/);
});

test("language configuration: comments, brackets, auto-closing, folding and words", () => {
  const conf = JSON.parse(readFileSync(join(root, "language-configuration.json"), "utf8"));
  for (const l of manifest.contributes.languages) assert.equal(l.configuration, "./language-configuration.json");
  assert.equal(conf.comments.lineComment, "//");
  assert.deepEqual(conf.brackets, [["{{", "}}"], ["{", "}"], ["[", "]"], ["(", ")"]]);
  const closing = Object.fromEntries(conf.autoClosingPairs.map((p: any) => [p.open, p]));
  for (const o of ["{", "[", "("]) assert.ok(closing[o], o);
  for (const o of ['"', "`"]) assert.deepEqual(closing[o].notIn, ["string", "comment"]);
  assert.equal(conf.folding.offSide, true);
  assert.equal(conf.wordPattern, "[A-Za-z_][A-Za-z0-9_]*");
});

test("bundles: one for the desktop and one for the browser, and no publish step", () => {
  assert.equal(manifest.main, "./dist/extension.js");
  assert.equal(manifest.browser, "./dist/web.js");
  for (const [name, script] of Object.entries<string>(manifest.scripts)) {
    assert.doesNotMatch(script, /vsce publish|ovsx publish/, name);
  }
  const ids = manifest.contributes.commands.map((c: any) => `${c.category}: ${c.title}`);
  assert.deepEqual(ids, ["Heron: Check", "Heron: Lint", "Heron: Generate Docs", "Heron: Generate…", "Heron: Open Preview to the Side", "Heron: Restart Language Server"]);
});

test("the server: heron.path, or heron on PATH, started as heron lsp --stdio", () => {
  assert.equal(heronCommand(undefined), "heron");
  assert.equal(heronCommand("  "), "heron");
  assert.equal(heronCommand("/opt/heron/bin/heron"), "/opt/heron/bin/heron");
  assert.deepEqual(serverArgs, ["lsp", "--stdio"]);
  const dir = mkdtempSync(join(tmpdir(), "heron-path-"));
  const bin = join(dir, "heron");
  writeFileSync(bin, "#!/bin/sh\n");
  chmodSync(bin, 0o755);
  assert.equal(resolveCommand("heron", { PATH: `/nowhere:${dir}` }), bin);
  assert.equal(resolveCommand(bin, { PATH: "" }), bin);
});

test("no server: a message that names the command and the heron.path setting", () => {
  assert.equal(resolveCommand("heron", { PATH: "/nowhere" }), undefined);
  assert.equal(resolveCommand("/nowhere/heron", {}), undefined);
  const m = notFoundMessage("heron");
  assert.match(m, /`heron lsp`/);
  assert.match(m, /heron\.path/);
});

test("commands: check, lint, generate docs, and generate with the targets picked", () => {
  assert.deepEqual(cliCommands["heron.check"].args, ["check"]);
  assert.deepEqual(cliCommands["heron.lint"].args, ["lint"]);
  assert.deepEqual(cliCommands["heron.generateDocs"].args, ["generate", "docs"]);
  assert.deepEqual(generateArgs(["ts", "proto"]), ["generate", "ts", "proto"]);
  assert.deepEqual(generateArgs([]), ["generate"]);
  assert.deepEqual(generateTargets, ["ts", "docs", "agents", "asyncapi", "proto", "openrpc", "sparrow"]);
});

test("the preview's policy: nothing by default, style and script by nonce, no network", () => {
  const csp = previewPolicy("abc", "vscode-resource:");
  assert.match(csp, /^default-src 'none';/);
  assert.match(csp, /style-src 'nonce-abc'(;|$)/);
  assert.match(csp, /script-src 'nonce-abc'(;|$)/);
  assert.match(csp, /img-src vscode-resource: data:/);
  assert.doesNotMatch(csp, /unsafe-inline|unsafe-eval|https?:|connect-src/);
  const page = previewPage("abc", "vscode-resource:", "a { color: red } </style><script>x()</script>");
  assert.equal(page.match(/<\/style>/g)?.length, 1, "the stylesheet ends where the page ends it");
  assert.equal(page.match(/<script nonce=/g)?.length, 1, "one script");
  assert.match(page, /<script nonce="abc">/);
  assert.match(page, /<style nonce="abc">/);
  assert.doesNotMatch(page, /<\/style><script>x\(\)/, "the stylesheet can't close its element");
  assert.match(page, /closest\("a"\)\) e\.preventDefault\(\)/, "links are inert");
});

test("icon: Heron's icon, a 128 px PNG the manifest names and the package keeps", () => {
  assert.equal(manifest.icon, "images/icon.png");
  const png = readFileSync(join(root, manifest.icon));
  assert.equal(png.subarray(1, 4).toString(), "PNG");
  assert.equal(png.readUInt32BE(16), 128, "width");
  assert.equal(png.readUInt32BE(20), 128, "height");
  const ignore = readFileSync(join(root, ".vscodeignore"), "utf8").split("\n");
  assert.ok(ignore.includes("!images/icon.png"), "the .vsix keeps the icon");
});
