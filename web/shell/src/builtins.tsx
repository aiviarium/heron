// The shell's own parts, registered through its slots: the commands, the
// problems panel and the run gutter.
import { For, Show } from "solid-js";
import { t } from "@heron/strings";
import { registerCommand, registerGutterProvider, registerPanel } from "./slots.ts";
import * as s from "./state.ts";
import { sourceName } from "@heron/editor";

export function ProblemsList() {
  return (
    <Show when={s.problems().length > 0} fallback={<div class="h-empty">{t("app.no_problems")}</div>}>
      <ul class="h-problems" data-testid="problems">
        <For each={s.problems()}>
          {(p) => (
            <li>
              <button type="button" class="h-problem" onClick={() => void s.openFile(p.file, { line: p.line, column: p.column })}>
                <span class={["h-sev", `h-sev-${p.severity}`]} />
                <span class="h-problem-msg">{p.message}</span>
                <span class="h-problem-src">{sourceName(p.source)}</span>
                <code class="h-problem-loc">
                  {p.file}:{p.line}:{p.column}
                </code>
              </button>
            </li>
          )}
        </For>
      </ul>
    </Show>
  );
}

let done = false;

export function registerBuiltins() {
  if (done) return;
  done = true;
  registerPanel({ id: "problems", title: t("app.problems"), slot: "bottom", render: () => <ProblemsList /> });

  registerGutterProvider({
    id: "run",
    provide: ({ outline, results }) =>
      outline
        .filter((n) => n.runnable)
        .map((n) => {
          const live = s.runs()[n.path];
          const last = results.find((r) => r.source === "run" && r.path === n.path);
          const status = (live?.status ?? last?.status) as "ok" | "failed" | "running" | undefined;
          const summary = live?.status === "running" ? t("app.status.running") : (live?.summary || last?.summary);
          return {
            line: n.line,
            status,
            label: status && summary ? `${status === "running" ? "" : statusWord(status) + " "}${summary}`.trim() : undefined,
            title: t("app.cmd.run_task", { task: n.path }),
            action: () => void s.runTask(n.path),
          };
        }),
  });

  const cmds = [
    { id: "save", title: t("app.cmd.save"), shortcut: "mod+s", run: () => s.save() },
    { id: "format", title: t("app.cmd.format"), shortcut: "shift+alt+f", run: () => s.format() },
    { id: "toggle-preview", title: t("app.cmd.toggle_preview"), shortcut: "mod+\\", run: () => s.setShowPreview(!s.showPreview()) },
    { id: "toggle-theme", title: t("app.cmd.toggle_theme"), run: () => s.setTheme(s.theme() === "dark" ? "light" : "dark") },
    { id: "next-problem", title: t("app.cmd.next_problem"), shortcut: "F8", run: () => s.nextProblem() },
    { id: "show-problems", title: t("app.cmd.show_problems"), shortcut: "mod+shift+m", run: () => s.setBottomPanel(s.bottomPanel() === "problems" ? null : "problems") },
    { id: "reconnect", title: t("app.cmd.reconnect"), run: () => s.lsp?.retry() },
  ];
  for (const c of cmds) registerCommand({ ...c, group: t("app.commands") });
}

function statusWord(st: string) {
  return st === "ok" ? t("app.status.ok") : st === "failed" ? t("app.status.failed") : t("app.status.running");
}
