// Done means #2, the preview half: a change to an api shows in the preview,
// rendered as in the docs, within a second of typing, and a task's latest
// record shows beside its node.
const { run, ready, lspConnected, assert } = require("./harness.cjs");

const norm = (s) => s.replace(/\s+/g, " ").trim();

async function cursorOn(page, text) {
  // Puts the cursor on the first line that contains text, through the
  // editor's own find.
  await page.click('[data-testid="editor"] .view-lines');
  await page.keyboard.press("Control+Home");
  const lines = (await page.evaluate(() => window.heronDebug.value())).split("\n");
  const n = lines.findIndex((l) => l.includes(text));
  assert(n >= 0, "no line with " + text);
  for (let i = 0; i < n; i++) await page.keyboard.press("ArrowDown");
  await page.keyboard.press("End");
}

run("preview", async ({ page, url }) => {
  await page.goto(url + "edit/specs/store/api.heron");
  await ready(page);
  await lspConnected(page);

  await cursorOn(page, "add_pet(");
  await page.waitForFunction(() => document.querySelector('[data-testid="preview-pane"] .h-pane-path')?.textContent === "store.add_pet");
  // Rendered as in the docs: the spec browser's page for the op has the same content.
  const docsText = await page.evaluate(async () => {
    const html = await (await fetch("/docs/store.add_pet.html")).text();
    const d = new DOMParser().parseFromString(html, "text/html");
    return (d.querySelector("main .page-title, main h1")?.textContent ?? "") + " " + (d.querySelector("main")?.textContent ?? "");
  });
  const previewText = await page.innerText('[data-testid="preview"] .h-preview-body');
  for (const piece of ["/pets", "Add pet", "POST"]) {
    assert(norm(docsText).includes(piece) && norm(previewText).includes(piece), `"${piece}" isn't in both the docs and the preview`);
  }

  // Edit the op's path; the preview follows within a second.
  await cursorOn(page, '@http<post>("/pets")');
  for (let i = 0; i < 2; i++) await page.keyboard.press("ArrowLeft");
  const t0 = Date.now();
  await page.keyboard.type("/mine");
  await page.keyboard.press("ArrowDown"); // onto add_pet, the node shown
  await page.waitForFunction(() => document.querySelector('[data-testid="preview"] .h-preview-body')?.textContent.includes("/pets/mine"), null, { timeout: 1000 });
  const dt = Date.now() - t0;
  assert(dt < 1000, `the preview took ${dt} ms`);

  // A broken edit shows its error in place and keeps the page.
  await page.keyboard.press("End");
  await page.keyboard.type(" (((");
  await page.waitForTimeout(500);
  assert((await page.innerText('[data-testid="preview"] .h-preview-body')).trim().length > 0, "the preview blanked on an error");
  for (let i = 0; i < 4; i++) await page.keyboard.press("Backspace");

  // The latest record beside a task: run it, then edit it.
  await page.click('.h-tree-row[data-path="services/store/tasks.heron"]');
  await page.waitForFunction(() => window.heronDebug.current() === "services/store/tasks.heron");
  await cursorOn(page, "build: task");
  await page.waitForSelector('[data-testid="preview-run"]');
  await page.click('[data-testid="preview-run"]');
  await page.waitForSelector('.h-result[data-path="store_svc.build"][data-status="failed"], .h-result[data-path="store_svc.build"][data-status="ok"]', { timeout: 30000 });
  const status = await page.getAttribute('.h-result[data-path="store_svc.build"]', "data-status");
  await page.keyboard.press("End");
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("End");
  await page.keyboard.type(" ");
  // After the edit, the record from disk is still beside the node.
  await page.reload();
  await ready(page);
  await cursorOn(page, "build: task");
  await page.waitForSelector(`.h-result[data-path="store_svc.build"][data-status="${status}"]`, { timeout: 5000 });
  await page.waitForSelector(`.h-mark-label`, { timeout: 5000 });
});
