// Typing: 200+ characters with Enter, typed fast while the server answers
// with diagnostics, outlines and previews; the text must be byte-identical
// and the cursor where typing left it.
const { run, ready, lspConnected, assert } = require("./harness.cjs");

run("typing", async ({ page, url }) => {
  await page.goto(url + "edit/specs/store/model.heron");
  await ready(page);
  await lspConnected(page);
  const before = await page.evaluate(() => window.heronDebug.value());
  await page.click('[data-testid="editor"] .view-lines');
  await page.keyboard.press("Control+End");
  const lines = [
    "// The store keeps orders for a week and then archives them",
    "// so the search stays fast for the people who work the counter",
    "// and the reports for each month are built from the archive",
    "// without touching the live tables at all during the day",
  ];
  const typed = "\n" + lines.join("\n") + "\n";
  assert(typed.length > 200, "too little text: " + typed.length);
  await page.keyboard.type(typed, { delay: 0 });
  // Let every response to the edits arrive.
  await page.waitForTimeout(1500);
  const after = await page.evaluate(() => window.heronDebug.value());
  const want = before + typed;
  assert(after === want, `text differs:\n--- want tail\n${JSON.stringify(want.slice(-300))}\n--- got tail\n${JSON.stringify(after.slice(-300))}`);
  const pos = await page.evaluate(() => window.heronDebug.position());
  const lineCount = want.split("\n").length;
  assert(pos.lineNumber === lineCount && pos.column === 1, `cursor at ${pos.lineNumber}:${pos.column}, want ${lineCount}:1`);
});
