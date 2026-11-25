// The acceptance tests' harness: the real `heron serve` on a fresh git copy
// of examples/petstore, and Chromium with an empty profile, failing on any
// console error, failed request or page error.
const { chromium } = require("playwright");
const { spawn, execFileSync } = require("node:child_process");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");

const repo = path.resolve(__dirname, "../../..");

/** Copies the sample into a temporary git repository. */
function sample() {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), "heron-e2e-"));
  const ws = path.join(dir, "petstore");
  fs.cpSync(path.join(repo, "examples/petstore"), ws, { recursive: true, filter: (p) => !p.includes("node_modules") });
  const git = (...a) => execFileSync("git", a, { cwd: ws, stdio: "ignore" });
  git("init", "-q", "-b", "main");
  git("add", "-A");
  git("-c", "user.name=e2e", "-c", "user.email=e2e@localhost", "commit", "-qm", "sample");
  return { dir, ws };
}

/** Starts heron serve on a workspace and resolves with its URL. */
function serve(heron, ws) {
  return new Promise((resolve, reject) => {
    const p = spawn(heron, ["serve", "--addr", "127.0.0.1:0"], { cwd: ws, stdio: ["ignore", "pipe", "pipe"] });
    let out = "";
    let err = "";
    p.stdout.on("data", (d) => {
      out += d;
      const m = out.match(/http:\/\/[^\s]+/);
      if (m) resolve({ url: m[0], proc: p, stderr: () => err });
    });
    p.stderr.on("data", (d) => (err += d));
    p.on("exit", (code) => reject(new Error(`heron serve exited ${code}: ${err}`)));
  });
}

/**
 * Runs a test: fn receives { page, url, ws, heron, problems() }. It fails on
 * console errors, page errors and failed requests unless `allow` matches.
 */
async function run(name, fn, opts = {}) {
  const heron = process.env.HERON || path.join(repo, ".heron-e2e-bin/heron");
  const { dir, ws } = sample();
  const server = await serve(heron, ws);
  const browser = await chromium.launch();
  const context = await browser.newContext({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: opts.scale ?? 1, colorScheme: opts.colorScheme ?? "dark" });
  const page = await context.newPage();
  const errors = [];
  const allow = opts.allow ?? (() => false);
  page.on("console", (m) => {
    if (m.type() === "error" && !allow(m.text())) errors.push("console: " + m.text());
  });
  page.on("pageerror", (e) => errors.push("page error: " + e.message));
  page.on("requestfailed", (r) => {
    const f = r.failure()?.errorText ?? "";
    // A stream the page itself aborts, or a socket closed at teardown, isn't a failure.
    if (f.includes("ERR_ABORTED")) return;
    errors.push(`request failed: ${r.url()} ${f}`);
  });
  page.on("response", (r) => {
    if (r.status() >= 400 && !allow(r.url())) errors.push(`HTTP ${r.status()}: ${r.url()}`);
  });
  const started = Date.now();
  let ok = true;
  try {
    await fn({ page, url: server.url, ws, heron, context, errors });
    if (errors.length) throw new Error("errors in the page:\n  " + errors.join("\n  "));
  } catch (e) {
    ok = false;
    console.error(`FAIL ${name}: ${e.stack ?? e}`);
    const shot = path.join(dir, "failure.png");
    await page.screenshot({ path: shot }).catch(() => {});
    console.error(`  screenshot: ${shot}`);
    if (server.stderr()) console.error("  server stderr:\n" + server.stderr());
  } finally {
    await browser.close();
    server.proc.kill();
    if (ok) fs.rmSync(dir, { recursive: true, force: true });
  }
  if (ok) console.log(`ok   ${name} (${((Date.now() - started) / 1000).toFixed(1)} s)`);
  else process.exitCode = 1;
}

/** Waits until the app has loaded the workspace and a file. */
async function ready(page) {
  await page.waitForSelector('[data-testid="editor"] .view-lines', { timeout: 15000 });
  await page.waitForFunction(() => window.heronDebug?.ready(), null, { timeout: 15000 });
}

/** Waits until the language server is connected. */
async function lspConnected(page) {
  await page.waitForSelector('[data-testid="lsp-status"][data-state="connected"]', { timeout: 15000 });
}

/** `heron check --json` on the workspace, by file. */
function check(heron, ws) {
  let out;
  try {
    out = execFileSync(heron, ["check", "--json"], { cwd: ws, encoding: "utf8" });
  } catch (e) {
    out = e.stdout;
  }
  return JSON.parse(out || "[]");
}

function assert(cond, msg) {
  if (!cond) throw new Error(msg);
}

/** Fails if a "Loading" label or an error banner stays on screen. */
async function noStuckStates(page) {
  await page.waitForTimeout(300);
  const banner = await page.$('[data-testid="error-banner"]');
  assert(!banner, "an error banner is shown: " + (banner && (await banner.innerText())));
  const body = await page.innerText("body");
  assert(!/\bLoading\b/.test(body), "a Loading label stays on screen");
  const st = await page.getAttribute('[data-testid="lsp-status"]', "data-state");
  assert(st !== "connecting", "the language server status is stuck on connecting");
}

module.exports = { run, ready, lspConnected, check, assert, noStuckStates, repo };
