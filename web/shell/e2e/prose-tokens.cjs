// Prose is authored text, not code: in a .heron.md file and in `|` lines
// the static grammar gives its words no keyword, name or type token, and
// highlights only Markdown's marks, kind elements, links, inline code and
// fences, a `heron` fence as Heron.
const fs = require("node:fs");
const path = require("node:path");
const { run, ready, assert } = require("./harness.cjs");

const md = [
  "# Refunds are asked for {#refunds}",
  "",
  "Refunds are asked for in the console and do it if not, for each order in turn.",
  "",
  "- a list item with *emphasis*, `code`, [window] and \\[escaped].",
  "",
  '<howto id="ask" @lens="user">',
  "Then call the <ref to=\"desk\"/>.",
  "</howto>",
  "",
  "```heron",
  "window: term {{ The 30 days. }}",
  "```",
].join("\n");

const heron = ["s: section {", "  | Refunds are asked for in the console and do it if not.", "}"].join("\n");

run("prose-tokens", async ({ page, url, ws }) => {
  fs.writeFileSync(path.join(ws, "guide.heron.md"), md);
  await page.goto(url);
  await ready(page);
  const mdTokens = await page.evaluate((t) => window.heronDebug.tokenize(t, "heron-md"), md);
  const heronTokens = await page.evaluate((t) => window.heronDebug.tokenize(t, "heron"), heron);
  const lines = md.split("\n");
  const words = (toks, line) => toks.map(([o, ty], i) => [line.slice(o, i + 1 < toks.length ? toks[i + 1][0] : line.length), ty]);
  const codeish = /keyword|identifier|\btype\b|number/;
  // The prose lines: the heading's text, the paragraph and the list item.
  for (const i of [0, 2, 4]) {
    const bad = words(mdTokens[i], lines[i]).filter(([w, ty]) => codeish.test(ty) && w.trim());
    assert(bad.length === 0, `prose line ${i + 1} has code tokens: ${JSON.stringify(bad)}`);
  }
  const bad = words(heronTokens[1], heron.split("\n")[1]).filter(([w, ty]) => codeish.test(ty) && w.trim());
  assert(bad.length === 0, `a | line has code tokens: ${JSON.stringify(bad)}`);
  const types = (i) => new Set(mdTokens[i].map(([, ty]) => ty.replace(/\.heron-md$/, "")));
  assert(types(0).has("delimiter.markdown") && types(0).has("annotation"), "the heading's # and {#name}: " + [...types(0)]);
  assert(types(4).has("delimiter.markdown") && types(4).has("string.code") && types(4).has("link") && types(4).has("string.escape"), "the list item's marks: " + [...types(4)]);
  assert(types(6).has("tag") && types(6).has("attribute.name") && types(6).has("attribute.value") && types(6).has("annotation"), "the element's head: " + [...types(6)]);
  assert([...types(11)].some((t) => t.startsWith("keyword") || t.startsWith("identifier")), "the heron fence reads as Heron: " + [...types(11)]);
});
