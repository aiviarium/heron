// Screenshots of the editor screen, dark and light, at 2x, into
// web/screenshots/app-*.png: the api file with an op under the cursor, a
// task run from the gutter, and the problems panel with a broken line.
const fs = require("node:fs");
const path = require("node:path");
const { run, ready, lspConnected, repo } = require("./harness.cjs");

const out = path.join(repo, "web/screenshots");
fs.mkdirSync(out, { recursive: true });

for (const theme of ["dark", "light"]) {
  run(
    `screenshots-${theme}`,
    async ({ page, url, context }) => {
      await context.addInitScript((th) => localStorage.setItem("heron.theme", th), theme);
      // A task's last run, so the gutter and the results have a record.
      await page.goto(url + "edit/services/store/tasks.heron");
      await ready(page);
      await lspConnected(page);
      await page.waitForSelector(".h-glyph");
      await page.click(".h-glyph");
      await page.waitForSelector('.h-mark-label', { timeout: 30000 });
      await page.waitForTimeout(400);
      await page.screenshot({ path: path.join(out, `app-tasks-${theme}.png`) });

      // The api with an op under the cursor.
      await page.click('.h-tree-row[data-path="specs/store/api.heron"]');
      await page.waitForFunction(() => window.heronDebug.current() === "specs/store/api.heron");
      const lines = (await page.evaluate(() => window.heronDebug.value())).split("\n");
      const n = lines.findIndex((l) => l.includes("place_order("));
      await page.click('[data-testid="editor"] .view-lines');
      await page.keyboard.press("Control+Home");
      for (let i = 0; i < n; i++) await page.keyboard.press("ArrowDown");
      await page.keyboard.press("End");
      await page.waitForFunction(() => document.querySelector('[data-testid="preview-pane"] .h-pane-path')?.textContent === "store.place_order");
      await page.waitForTimeout(900);
      await page.screenshot({ path: path.join(out, `app-editor-${theme}.png`) });

      // A problem: an unknown type, its underline and hover, and the panel.
      const m = lines.findIndex((l) => l.includes("add_pet(@body pet: Pet)"));
      await page.keyboard.press("Control+Home");
      for (let i = 0; i < m; i++) await page.keyboard.press("ArrowDown");
      await page.keyboard.press("End");
      for (let i = 0; i < "): op(Pet)".length; i++) await page.keyboard.press("ArrowLeft");
      await page.keyboard.type("Kind");
      await page.waitForSelector(".squiggly-error", { timeout: 5000 });
      await page.click('[data-testid="problem-counts"]');
      const marker = (await page.evaluate(() => window.heronDebug.markers())).find((x) => x.severity === "error");
      const at = await page.evaluate(([l, c]) => window.heronDebug.coords(l, c), [marker.line, marker.column + 2]);
      await page.mouse.move(at.x, at.y);
      await page.waitForSelector(".monaco-hover:not(.hidden)", { timeout: 5000 });
      await page.waitForTimeout(300);
      await page.screenshot({ path: path.join(out, `app-problems-${theme}.png`) });
    },
    { scale: 2, colorScheme: theme },
  );
}
