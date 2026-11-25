// First run: an empty profile opens /, and follows only what's visible:
// the file tree and every file, the outline, the palette, the preview, the
// problems and a run from the gutter.
const { run, ready, lspConnected, assert, noStuckStates } = require("./harness.cjs");

run("first-run", async ({ page, url }) => {
  await page.goto(url);
  await ready(page);
  await lspConnected(page);
  const name = await page.innerText(".h-workspace-name");
  assert(name === "petstore", "workspace name: " + name);
  assert((await page.innerText('[data-testid="breadcrumb"]')).includes("index.heron"), "/ doesn't open the entry point");

  // Every file in the tree.
  const files = await page.$$eval('.h-tree [role="treeitem"]:not([aria-expanded]) > .h-tree-row', (rows) => rows.map((r) => r.dataset.path));
  assert(files.length >= 5, "the tree lists " + files.length + " files");
  for (const f of files) {
    await page.click(`.h-tree-row[data-path="${f}"]`);
    await page.waitForFunction((f) => window.heronDebug.current() === f && window.heronDebug.value()?.length > 0, f);
    await page.waitForFunction((f) => location.pathname === "/edit/" + f, f);
    const crumb = await page.innerText('[data-testid="breadcrumb"]');
    assert(crumb.replace(/\s/g, "").includes(f.replace(/\s/g, "")), `breadcrumb for ${f}: ${crumb}`);
    await noStuckStates(page);
  }

  // The outline of a file with nodes, and the preview of a node.
  await page.click('.h-tree-row[data-path="specs/store/model.heron"]');
  await page.waitForSelector('[data-testid="outline"] .h-outline-row[data-path="Pet"]');
  const rows = await page.$$('[data-testid="outline"] .h-outline-row[data-depth="0"]');
  assert(rows.length > 2, "outline rows: " + rows.length);
  const target = await rows[1].getAttribute("data-path");
  await page.click(`[data-testid="outline"] .h-outline-row[data-path="${target}"]`);
  await page.waitForFunction((p) => document.querySelector('[data-testid="preview-pane"] .h-pane-path')?.textContent === p, target, { timeout: 5000 });
  const html = await page.innerText('[data-testid="preview"] .h-preview-body');
  assert(html.trim().length > 0, "the preview is empty");

  // The palette: ⌘K, type, Enter.
  await page.keyboard.press("Control+K");
  await page.waitForFunction(() => document.activeElement?.matches('[data-testid="palette"] input'));
  await page.keyboard.type("tasks.heron");
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => window.heronDebug.current() === "services/store/tasks.heron");

  // The problems panel.
  await page.click('[data-testid="problem-counts"]');
  await page.waitForSelector('[data-testid="bottom-panel"]');
  assert((await page.innerText('[data-testid="bottom-panel"]')).includes("No problems"), "the sample has problems");

  // A run from the gutter streams into place.
  await page.waitForSelector(".h-glyph");
  await page.click(".h-glyph");
  await page.waitForSelector('.h-result[data-status="running"], .h-result[data-status="failed"], .h-result[data-status="ok"]', { timeout: 10000 });
  await page.waitForSelector('.h-result[data-status="failed"], .h-result[data-status="ok"]', { timeout: 30000 });
  await page.waitForSelector('.h-mark-label', { timeout: 5000 });
  await noStuckStates(page);
});
