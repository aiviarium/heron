// File switching: click through every file in several orders; the markers
// shown must equal `heron check`'s, with no false "already defined". A
// broken file joins the sample, so there is something to compare.
const fs = require("node:fs");
const path = require("node:path");
const { run, ready, lspConnected, check, assert } = require("./harness.cjs");

function shuffle(xs, seed) {
  const out = [...xs];
  let s = seed;
  for (let i = out.length - 1; i > 0; i--) {
    s = (s * 1103515245 + 12345) % 2147483648;
    const j = s % (i + 1);
    [out[i], out[j]] = [out[j], out[i]];
  }
  return out;
}

const key = (d) => `${d.file}:${d.line}:${d.column}:${d.severity}:${d.message}`;

run("file-switching", async ({ page, url, ws, heron }) => {
  // An unknown name in the tasks file, which heron check reports.
  const tasks = path.join(ws, "services/store/tasks.heron");
  fs.writeFileSync(tasks, fs.readFileSync(tasks, "utf8").replace('run("pnpm", args = ["--filter", "store", "lint"])', 'run("pnpm", args = missing_args)'));
  const want = check(heron, ws)
    .flatMap((r) => r.diagnostics)
    .map((d) => ({ file: d.file, line: d.line, column: d.column, severity: typeof d.severity === "string" ? (d.severity === "hint" ? "info" : d.severity) : (["error", "warning", "info"][d.severity] ?? "info"), message: d.message }));
  assert(want.length > 0, "heron check finds nothing in the broken sample");

  await page.goto(url);
  await ready(page);
  await lspConnected(page);
  const files = await page.$$eval('.h-tree [role="treeitem"]:not([aria-expanded]) > .h-tree-row', (rows) => rows.map((r) => r.dataset.path));
  for (const order of [files, [...files].reverse(), shuffle(files, 7), shuffle(files, 42)]) {
    for (const f of order) {
      await page.click(`.h-tree-row[data-path="${f}"]`);
      await page.waitForFunction((f) => window.heronDebug.current() === f, f);
      await page.waitForTimeout(60);
    }
  }
  // Every file has been opened; give the server a moment to publish.
  await page.waitForTimeout(1500);
  const got = (await page.evaluate(() => window.heronDebug.markers())).filter((m) => m.file);
  const already = got.filter((m) => /already (defined|declared)/i.test(m.message));
  assert(already.length === 0, "false already-defined markers: " + JSON.stringify(already));
  const g = new Set(got.map(key));
  const w = new Set(want.map(key));
  const missing = [...w].filter((k) => !g.has(k));
  const extra = [...g].filter((k) => !w.has(k));
  assert(missing.length === 0 && extra.length === 0, `markers differ from heron check:\n  missing ${JSON.stringify(missing)}\n  extra ${JSON.stringify(extra)}`);
  // One model per file, never reused.
  const models = await page.evaluate(() => window.heronDebug.models());
  assert(new Set(models).size === models.length, "a model URI repeats");
  // The tree's counts match too.
  const count = await page.innerText('.h-tree-row[data-path="services/store/tasks.heron"] .h-counts').catch(() => "");
  assert(count.trim() === String(want.filter((d) => d.file === "services/store/tasks.heron" && d.severity === "error").length), "tree count: " + count);
});
