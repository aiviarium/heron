// Done means #1: diagnostics, completion, hover, go to definition and
// rename in Monaco, answered by the real language server through heron serve.
const { run, ready, lspConnected, assert } = require("./harness.cjs");

async function goTo(page, line, column) {
  await page.click('[data-testid="editor"] .view-lines');
  await page.keyboard.press("Control+Home");
  for (let i = 1; i < line; i++) await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Home");
  // Home may stop at the indentation; go to column 1, then right.
  const pos = await page.evaluate(() => window.heronDebug.position());
  if (pos.column !== 1) await page.keyboard.press("Home");
  for (let i = 1; i < column; i++) await page.keyboard.press("ArrowRight");
}

function find(text, needle) {
  const lines = text.split("\n");
  const i = lines.findIndex((l) => l.includes(needle));
  assert(i >= 0, "no line with " + needle);
  return { line: i + 1, column: lines[i].indexOf(needle) + 1 };
}

run("language-server", async ({ page, url }) => {
  await page.goto(url + "edit/specs/store/api.heron");
  await ready(page);
  await lspConnected(page);
  let text = await page.evaluate(() => window.heronDebug.value());

  // Diagnostics: an unknown type gets an underline, whose hover names the problem and its source.
  let at = find(text, "add_pet(@body pet: Pet)");
  await goTo(page, at.line, at.column + "add_pet(@body pet: ".length + 3);
  await page.keyboard.type("Nope");
  await page.waitForFunction(() => window.heronDebug.markers().some((m) => m.file === "specs/store/api.heron" && /PetNope/.test(m.message)), null, { timeout: 5000 });
  const marker = (await page.evaluate(() => window.heronDebug.markers())).find((m) => /PetNope/.test(m.message));
  assert(marker.severity === "error" && marker.source === "type checker", "marker: " + JSON.stringify(marker));
  await page.waitForSelector(".squiggly-error", { timeout: 3000 });
  const sq = await page.evaluate(([l, c]) => window.heronDebug.coords(l, c), [marker.line, marker.column + 2]);
  await page.mouse.move(sq.x, sq.y);
  await page.waitForSelector(".monaco-hover:not(.hidden)", { timeout: 5000 });
  const hoverText = await page.innerText(".monaco-hover:not(.hidden)");
  assert(/PetNope/.test(hoverText) && /type checker/.test(hoverText), "the problem hover: " + hoverText);
  for (let i = 0; i < 4; i++) await page.keyboard.press("Backspace");
  await page.mouse.move(5, 5);
  await page.waitForFunction(() => !window.heronDebug.markers().some((m) => /PetNope/.test(m.message)), null, { timeout: 5000 });

  // Completion from schemas: a type name after a colon.
  text = await page.evaluate(() => window.heronDebug.value());
  at = find(text, "store: api {");
  await goTo(page, at.line, "store: api {".length + 1);
  await page.keyboard.press("Enter");
  await page.keyboard.type("probe(@query x: Pe");
  await page.keyboard.press("Control+Space");
  await page.waitForSelector(".suggest-widget.visible .monaco-list-row", { timeout: 5000 });
  const items = await page.$$eval(".suggest-widget.visible .monaco-list-row", (rows) => rows.map((r) => r.getAttribute("aria-label") || r.textContent));
  assert(items.some((i) => /\bPet\b/.test(i)), "completion items: " + items.join(", "));
  await page.keyboard.press("Escape");
  // Undo the probe line.
  for (let i = 0; i < 12 && (await page.evaluate(() => window.heronDebug.value())) !== text; i++) await page.keyboard.press("Control+z");
  assert((await page.evaluate(() => window.heronDebug.value())) === text, "undo didn't restore the text");
  await page.waitForFunction(() => !window.heronDebug.markers().some((m) => /probe/.test(m.message)), null, { timeout: 5000 });

  // Hover from the tree: over a type name.
  at = find(text, "): op(Pet)");
  await goTo(page, at.line, at.column + "): op(".length + 1);
  const box = await page.evaluate(([l, c]) => window.heronDebug.coords(l, c), [at.line, at.column + "): op(".length + 1]);
  await page.mouse.move(box.x, box.y);
  await page.waitForSelector(".monaco-hover:not(.hidden) .hover-contents", { timeout: 5000 });
  const hover = await page.innerText(".monaco-hover:not(.hidden)");
  assert(/Pet/.test(hover), "hover: " + hover);
  await page.mouse.move(5, 5);

  // Go to definition across files.
  await page.keyboard.press("F12");
  await page.waitForFunction(() => window.heronDebug.current() === "specs/store/model.heron", null, { timeout: 5000 });
  const def = await page.evaluate(() => ({ pos: window.heronDebug.position(), text: window.heronDebug.value() }));
  assert(def.text.split("\n")[def.pos.lineNumber - 1].startsWith("Pet: type"), "definition landed on line " + def.pos.lineNumber);

  // Rename by path, across files.
  await page.keyboard.press("F2");
  await page.waitForFunction(() => !!document.activeElement?.closest(".rename-box"), null, { timeout: 5000 });
  await page.keyboard.press("Control+a");
  await page.keyboard.type("Animal");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => window.heronDebug.value().includes("Animal: type"), null, { timeout: 5000 });
  await page.waitForFunction(() => (window.heronDebug.text("specs/store/api.heron") ?? "").includes("op(Animal)"), null, { timeout: 5000 });
  await page.waitForTimeout(1000);
  const errs = (await page.evaluate(() => window.heronDebug.markers())).filter((m) => m.severity === "error");
  assert(errs.length === 0, "errors after the rename: " + JSON.stringify(errs));
});
