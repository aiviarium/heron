// Writing in the interface: every visible text of the editor screen comes
// from the spec (the sample repository and the standard packages it uses)
// or from the string catalogue. The code itself is the spec, so Monaco's
// text lines are left out; its widgets are not.
const fs = require("node:fs");
const path = require("node:path");
const { run, ready, lspConnected, assert, repo } = require("./harness.cjs");

function flatten(v, out = []) {
  if (typeof v === "string") out.push(v);
  else if (v && typeof v === "object") for (const [k, x] of Object.entries(v)) if (!k.startsWith("$")) flatten(x, out);
  return out;
}

function corpus(ws) {
  let text = "";
  const walk = (dir) => {
    for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
      if (e.name === "node_modules" || e.name === ".git") continue;
      const p = path.join(dir, e.name);
      if (e.isDirectory()) walk(p);
      else if (/\.(heron|json|ts)$/.test(e.name)) text += "\n" + fs.readFileSync(p, "utf8");
      text += "\n" + path.relative(ws, p);
    }
  };
  walk(ws);
  walk(path.join(repo, "std"));
  return text;
}

run("visible-text", async ({ page, url, ws }) => {
  const catalogue = flatten(JSON.parse(fs.readFileSync(path.join(repo, "web/strings/strings.json"), "utf8")));
  const fragments = [...new Set(catalogue.flatMap((s) => s.split(/\{[a-z_]+\}/)).map((s) => s.trim()).filter(Boolean))].sort((a, b) => b.length - a.length);
  const generated = await (await fetch(url + "api/v1/generated", { method: "POST", body: JSON.stringify({ overlays: {} }) })).json();
  const spec = corpus(ws) + "\n" + generated.files.map((f) => f.path + "\n" + f.text).join("\n");
  const lower = spec.toLowerCase();
  // The docs renderer titles a node from its name: get_pet is "Get pet".
  const fromSpec = (t) => spec.includes(t) || lower.includes(t.toLowerCase()) || lower.includes(t.toLowerCase().replace(/ /g, "_"));
  const allowed = (t) => {
    if (fromSpec(t)) return true;
    let rest = t;
    for (const f of fragments) rest = rest.split(f).join(" ");
    // What's left: numbers, punctuation, the branch, words of the spec.
    return rest.split(/[\s\d.,:;/()\-–—›·+#@%|\\]+/).every((w) => !w || w === "main" || fromSpec(w));
  };

  // The check itself must catch made-up text.
  for (const t of ["Saving will automatically update the preview", "Powered by Heron", "Let's get started!"]) assert(!allowed(t), "the check lets through " + t);

  const seen = new Map();
  const collect = async (where) => {
    const texts = await page.evaluate(() => {
      const out = [];
      const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
      for (let n = walker.nextNode(); n; n = walker.nextNode()) {
        const t = n.textContent.trim();
        if (!t) continue;
        const el = n.parentElement;
        if (el.closest(".view-lines, .margin-view-overlays, script, style")) continue;
        const r = el.getBoundingClientRect();
        const cs = getComputedStyle(el);
        if (r.width === 0 || r.height === 0 || cs.visibility === "hidden" || cs.display === "none") continue;
        out.push(t);
      }
      // Attributes a person reads: labels, placeholders and titles.
      for (const el of document.querySelectorAll("[aria-label], [placeholder], [title]")) {
        for (const a of ["aria-label", "placeholder", "title"]) {
          const v = el.getAttribute(a);
          if (v && !el.closest(".monaco-editor")) out.push(v.trim());
        }
      }
      return out;
    });
    for (const t of texts) if (!seen.has(t)) seen.set(t, where);
  };

  await page.goto(url);
  await ready(page);
  await lspConnected(page);
  await collect("entry point");
  for (const f of ["specs/store/api.heron", "specs/store/model.heron", "services/store/tasks.heron"]) {
    await page.click(`.h-tree-row[data-path="${f}"]`);
    await page.waitForFunction((f) => window.heronDebug.current() === f, f);
    await page.waitForTimeout(500);
    await collect(f);
  }
  await page.click('[data-testid="problem-counts"]');
  await collect("problems");
  await page.keyboard.press("Control+K");
  await page.waitForFunction(() => document.activeElement?.matches('[data-testid="palette"] input'));
  await collect("palette");
  await page.keyboard.type("zzzz");
  await collect("palette, no matches");
  // The first Escape clears the search, the second closes the palette.
  for (let i = 0; i < 3 && (await page.$('[data-testid="palette"]')); i++) await page.keyboard.press("Escape");
  await page.click('[aria-label="Theme"]');
  await collect("light theme");

  const bad = [...seen].filter(([t]) => !allowed(t));
  assert(bad.length === 0, "visible text from neither the spec nor the catalogue:\n  " + bad.map(([t, w]) => `${JSON.stringify(t)} (${w})`).join("\n  "));
});
