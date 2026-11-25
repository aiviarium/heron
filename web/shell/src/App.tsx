// The editor screen: title bar, file tree with the outline, the code with
// its breadcrumb and gutter, the preview with its results, the bottom
// panels and the status bar.
import { For, Show, createEffect, createMemo, onSettled } from "solid-js";
import { CommandPalette, FileTree, Icon, IconButton, Kbd, type PaletteItem } from "@heron/components";
import { Editor, Preview, monaco, type PreviewResult } from "@heron/editor";
import { t } from "@heron/strings";
import { allCommands, allPanels, gutterMarks, rendererFor } from "./extensions.ts";
import * as s from "./state.ts";
import { enhance } from "./docs.ts";

const base = (p: string) => p.split("/").pop() ?? p;

/** A tab's label: the file name, with its folder when another open file shares the name. */
function tabLabel(p: string, open: string[]): string {
  const clean = (x: string) => x.replace(/^generated:/, "").replace(/^external:.*?:\/*/, "");
  const name = base(clean(p));
  const same = open.filter((o) => o !== p && base(clean(o)) === name);
  if (same.length === 0) return name;
  const parts = clean(p).split("/");
  return parts.length > 1 ? parts.slice(-2).join("/") : name;
}

function TitleBar() {
  return (
    <header class="h-titlebar" data-testid="titlebar">
      <div class="h-workspace">
        <span class="h-logo" aria-hidden="true" />
        <span class="h-workspace-name">{s.workspace()?.name}</span>
      </div>
      <nav class="h-tabs" aria-label={t("app.files")}>
        <For each={s.openFiles()}>
          {(p) => (
            <div class={["h-tab", { "h-tab-current": s.current() === p }]} data-path={p}>
              <button type="button" class="h-tab-open" onClick={() => void s.openFile(p)} title={p}>
                {tabLabel(p, s.openFiles())}
                <Show when={!s.isGenerated(p) && (s.ready() && (s.models.tick(), s.models.get(p)?.dirty()))}>
                  <span class="h-dirty">
                    <Icon name="dot" />
                  </span>
                </Show>
              </button>
              <button type="button" class="h-tab-close" aria-label={t("app.close")} onClick={() => s.closeFile(p)}>
                <Icon name="close" />
              </button>
            </div>
          )}
        </For>
      </nav>
      <div class="h-actions">
        <button type="button" class="h-search" onClick={() => s.setPaletteOpen(true)} data-testid="open-palette">
          <Icon name="search" />
          <span>{t("app.search")}</span>
          <Kbd keys="mod+k" />
        </button>
        <IconButton icon="format" label={t("app.format")} onClick={() => void s.format()} shortcut="Shift+Alt+F" />
        <IconButton icon="save" label={t("app.save")} onClick={() => void s.save()} shortcut="Meta+S" />
        <IconButton icon="preview" label={t("app.preview")} pressed={s.showPreview()} onClick={() => s.setShowPreview(!s.showPreview())} />
        <IconButton icon="theme" label={t("app.theme")} onClick={() => s.setTheme(s.theme() === "dark" ? "light" : "dark")} />
      </div>
    </header>
  );
}

function Sidebar() {
  const files = createMemo(() => s.workspace()?.files ?? []);
  const fileCounts = (p: string) => {
    const c = s.counts(p);
    return c.errors || c.warnings ? c : undefined;
  };
  return (
    <aside class="h-sidebar" data-testid="sidebar">
      <section class="h-side-section h-files">
        <div class="h-section-head">
          <span class="h-group-label">{t("app.files")}</span>
        </div>
        <Show when={files().length > 0}>
        <FileTree
          label={t("app.files")}
          paths={files()}
          current={s.current()}
          counts={fileCounts}
          dirty={(p) => s.ready() && (s.models.tick(), !!s.models.get(p)?.dirty())}
          onOpen={(p) => void s.openFile(p)}
        />
        </Show>
        <Show when={s.generated().length > 0}>
          <div class="h-section-head">
            <span class="h-group-label">{t("app.generated")}</span>
          </div>
          <ul class="h-list" data-testid="generated">
            <For each={s.generated()}>
              {(g) => (
                <li>
                  <button
                    type="button"
                    class={["h-row", { "h-row-current": s.current() === "generated:" + g.path }]}
                    onClick={() => void s.openFile("generated:" + g.path)}
                  >
                    <code>{g.path}</code>
                  </button>
                </li>
              )}
            </For>
          </ul>
        </Show>
      </section>
      <section class="h-side-section h-outline" data-testid="outline">
        <div class="h-section-head">
          <span class="h-group-label">{t("app.outline")}</span>
        </div>
        <Show when={s.outline().length > 0} fallback={<div class="h-empty">{t("app.no_nodes")}</div>}>
          <ul class="h-list">
            <For each={s.outline()}>
              {(n) => (
                <li>
                  <button
                    type="button"
                    class={["h-row", "h-outline-row", { "h-row-current": crumbs().some((c) => c.path === n.path) }]}
                    style={{ "padding-left": `${8 + n.depth * 12}px` }}
                    onClick={() => void s.openFile(s.current()!, { line: n.line, column: 1 })}
                    data-path={n.path}
                    data-depth={n.depth}
                  >
                    <span class="h-outline-name">{n.name}</span>
                    <span class="h-outline-kind">{n.kind}</span>
                  </button>
                </li>
              )}
            </For>
          </ul>
        </Show>
      </section>
    </aside>
  );
}

/** The nodes around the cursor, outermost first. */
const crumbs = () => {
  const line = s.cursor().line;
  return s.outline().filter((n) => n.line <= line && line <= n.end_line).sort((a, b) => a.depth - b.depth);
};

function Breadcrumb() {
  return (
    <div class="h-breadcrumb" data-testid="breadcrumb">
      <For each={(s.current() ?? "").replace(/^generated:/, "").replace(/^external:.*?:\/*/, "").split("/")}>
        {(seg, i) => (
          <>
            <Show when={i() > 0}>
              <span class="h-crumb-sep">/</span>
            </Show>
            <span class="h-crumb-file">{seg}</span>
          </>
        )}
      </For>
      <For each={crumbs()}>
        {(n) => (
          <>
            <span class="h-crumb-sep">›</span>
            <button type="button" class="h-crumb" onClick={() => void s.openFile(s.current()!, { line: n.line, column: 1 })}>
              {n.name}
            </button>
          </>
        )}
      </For>
      <Show when={s.currentModel()?.readOnly}>
        <span class="h-crumb-note">{t("app.read_only")}</span>
      </Show>
    </div>
  );
}

function PreviewPane() {
  const results = createMemo<PreviewResult[]>(() => {
    const p = s.preview();
    const path = p?.path ?? "";
    const out: PreviewResult[] = s.results().map((r) => ({ ...r, time: r.time }));
    for (const [task, run] of Object.entries(s.runs())) {
      if (path && task !== path && !task.startsWith(path + ".")) continue;
      const i = out.findIndex((r) => r.source === "run" && r.path === task);
      const live: PreviewResult = { source: "run", path: task, status: run.status, summary: run.summary, output: run.output };
      if (i >= 0) out[i] = { ...out[i], ...live, summary: run.summary || out[i].summary };
      else out.push(live);
    }
    return out;
  });
  const generatedText = createMemo(() => {
    const c = s.current();
    return c && s.isGenerated(c) ? s.generated().find((g) => "generated:" + g.path === c) : undefined;
  });
  return (
    <section class="h-preview-pane" data-testid="preview-pane">
      <div class="h-pane-head">
        <span class="h-group-label">{t("app.preview")}</span>
        <code class="h-pane-path">{generatedText()?.path ?? s.preview()?.path}</code>
      </div>
      <Show
        when={!generatedText()}
        fallback={<pre class="h-generated" data-testid="generated-preview">{generatedText()!.text}</pre>}
      >
        <Show when={s.preview() || s.previewError()} fallback={<div class="h-empty">{t("app.no_nodes")}</div>}>
          <Preview
            html={s.preview()?.html ?? ""}
            path={s.preview()?.path ?? ""}
            kind={s.preview()?.kind ?? ""}
            error={s.previewError()}
            results={results()}
            runnable={s.preview()?.kind === "task"}
            onRun={(p) => void s.runTask(p)}
            mount={(el) => {
              enhance(el);
              const kind = s.preview()?.kind ?? "";
              return rendererFor(kind)?.mount(el, { path: s.preview()?.path ?? "", kind });
            }}
          />
        </Show>
      </Show>
    </section>
  );
}

function BottomPanel() {
  const panel = createMemo(() => allPanels().find((p) => p.slot === "bottom" && p.id === s.bottomPanel()));
  return (
    <Show when={panel()}>
      {(p) => (
        <section class="h-bottom" data-testid="bottom-panel">
          <div class="h-pane-head">
            <div class="h-bottom-tabs" role="tablist">
              <For each={allPanels().filter((x) => x.slot === "bottom")}>
                {(x) => (
                  <button type="button" role="tab" aria-selected={x.id === p().id ? "true" : "false"} class="h-bottom-tab" onClick={() => s.setBottomPanel(x.id)}>
                    {x.title}
                  </button>
                )}
              </For>
            </div>
            <IconButton icon="close" label={t("app.close")} onClick={() => s.setBottomPanel(null)} />
          </div>
          <div class="h-bottom-body">{p().render()}</div>
        </section>
      )}
    </Show>
  );
}

function LspStatus() {
  const st = createMemo(() => (s.ready() ? s.lsp.status() : { state: "connecting" as const, attempt: 0, reason: undefined }));
  return (
    <span class={["h-lsp", `h-lsp-${st().state}`]} data-testid="lsp-status" data-state={st().state}>
      <span class="h-lsp-dot" />
      <span>
        {st().state === "connected"
          ? t("app.lsp.connected")
          : st().state === "connecting"
            ? t("app.lsp.connecting")
            : st().state === "reconnecting"
              ? t("app.lsp.reconnecting", { reason: st().reason ?? "" })
              : t("app.lsp.failed", { reason: st().reason ?? "" })}
      </span>
      <Show when={st().state === "failed"}>
        <button type="button" class="h-link-button" onClick={() => s.lsp.retry()}>
          {t("app.retry")}
        </button>
      </Show>
    </span>
  );
}

function StatusBar() {
  const c = createMemo(() => s.counts());
  return (
    <footer class="h-statusbar" data-testid="statusbar">
      <span class="h-branch">
        <Icon name="branch" />
        <span>{s.workspace()?.branch}</span>
      </span>
      <button type="button" class="h-status-problems" onClick={() => s.setBottomPanel(s.bottomPanel() === "problems" ? null : "problems")} data-testid="problem-counts">
        <span class={["h-count", { "h-error": c().errors > 0 }]}>{c().errors === 1 ? t("app.error_one") : t("app.errors", { n: c().errors })}</span>
        <span class={["h-count", { "h-warn": c().warnings > 0 }]}>{c().warnings === 1 ? t("app.warning_one") : t("app.warnings", { n: c().warnings })}</span>
      </button>
      <span class="h-spacer" />
      <LspStatus />
      <span class="h-cursor" data-testid="cursor">
        {t("app.line_col", { line: s.cursor().line, col: s.cursor().column })}
      </span>
    </footer>
  );
}

function Palette() {
  const items = createMemo<PaletteItem[]>(() => {
    const cmds = allCommands()
      .filter((c) => !c.when || c.when())
      .map((c) => ({ id: "cmd:" + c.id, title: c.title, group: c.group ?? t("app.commands"), shortcut: c.shortcut, hint: c.hint }));
    const files = (s.workspace()?.files ?? []).map((f) => ({ id: "file:" + f, title: base(f), hint: f, group: t("app.files") }));
    const tasks = s.outline().filter((n) => n.runnable).map((n) => ({ id: "run:" + n.path, title: t("app.cmd.run_task", { task: n.path }), group: t("app.commands") }));
    return [...files, ...cmds, ...tasks];
  });
  return (
    <CommandPalette
      open={s.paletteOpen()}
      onOpenChange={(o) => s.setPaletteOpen(o)}
      items={items()}
      onRun={(id) => {
        if (id.startsWith("file:")) void s.openFile(id.slice(5));
        else if (id.startsWith("run:")) void s.runTask(id.slice(4));
        else void allCommands().find((c) => "cmd:" + c.id === id)?.run();
      }}
    />
  );
}

function shortcutMatches(e: KeyboardEvent, keys: string) {
  const parts = keys.toLowerCase().split("+");
  const key = parts.pop()!;
  const mod = parts.includes("mod");
  return (
    e.key.toLowerCase() === key &&
    (e.metaKey || e.ctrlKey) === mod &&
    e.shiftKey === parts.includes("shift") &&
    e.altKey === parts.includes("alt")
  );
}

export function App() {
  const marks = createMemo(() => {
    const c = s.current();
    if (!c || s.isGenerated(c)) return [];
    return gutterMarks({ file: c, outline: s.outline(), results: s.allResults() });
  });

  createEffect(
    () => s.ready() && s.models.tick(),
    () => s.schedule(),
  );

  onSettled(() => {
    const onKey = (e: KeyboardEvent) => {
      if (shortcutMatches(e, "mod+k") || shortcutMatches(e, "mod+p")) {
        e.preventDefault();
        s.setPaletteOpen(true);
        return;
      }
      if (s.paletteOpen()) return;
      for (const c of allCommands()) {
        if (c.shortcut && shortcutMatches(e, c.shortcut)) {
          e.preventDefault();
          void c.run();
          return;
        }
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  });

  return (
    <div class={["h-app", { "h-no-preview": !s.showPreview() }]}>
      <TitleBar />
      <Show when={s.loadError()}>
        {(e) => (
          <div class="h-banner" role="alert" data-testid="error-banner">
            {e()}
          </div>
        )}
      </Show>
      <main class="h-main">
        <Sidebar />
        <section class="h-code" data-testid="code">
          <Breadcrumb />
          <Editor
            model={s.currentModel()?.model ?? null}
            readOnly={!!s.currentModel()?.readOnly}
            theme={s.theme()}
            marks={marks()}
            reveal={s.reveal()}
            onReady={(ed) => {
              s.setEditor(ed);
              // Monaco's own ⌘K chords give way to the palette.
              ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyK, () => s.setPaletteOpen(true));
              ed.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => void s.save());
            }}
            onCursor={(line, column, offset) => {
              s.setCursor({ line, column, offset });
              s.schedule();
            }}
          />
          <BottomPanel />
        </section>
        <Show when={s.showPreview()}>
          <PreviewPane />
        </Show>
      </main>
      <StatusBar />
      <Palette />
    </div>
  );
}
