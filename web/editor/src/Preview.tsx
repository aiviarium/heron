// The preview: the current node rendered as the docs render it, with its
// results below. A failed render keeps the last page and shows the error in
// place.
import { For, Show, createEffect } from "solid-js";
import { t } from "@heron/strings";

export interface PreviewResult {
  source: string;
  path: string;
  status: string;
  summary: string;
  time?: string;
  output?: string[];
}

export interface PreviewProps {
  html: string;
  path: string;
  kind: string;
  error: string | null;
  results: PreviewResult[];
  /** Enhances the rendered node, such as with a kind's island. */
  mount?: (el: HTMLElement) => void | (() => void);
  onRun?: (path: string) => void;
  runnable?: boolean;
}

export function Preview(props: PreviewProps) {
  let body!: HTMLDivElement;
  let cleanup: void | (() => void);
  createEffect(
    () => props.html,
    (html) => {
      if (typeof cleanup === "function") cleanup();
      body.innerHTML = html;
      cleanup = props.mount?.(body);
    },
  );
  return (
    <div class="h-preview" data-testid="preview">
      <Show when={props.error}>
        {(e) => (
          <div class="h-preview-error" role="status" data-testid="preview-error">
            {e()}
          </div>
        )}
      </Show>
      <div class="h-preview-body h-docs" ref={body} data-node={props.path} data-kind={props.kind} />
      <section class="h-preview-results" data-testid="preview-results">
        <div class="h-section-head">
          <span class="h-group-label">{t("app.results")}</span>
          <Show when={props.runnable && props.path}>
            <button type="button" class="h-button h-size-sm" onClick={() => props.onRun?.(props.path)} data-testid="preview-run">
              {t("app.run")}
            </button>
          </Show>
        </div>
        <Show when={props.results.length > 0} fallback={<div class="h-empty">{t("app.no_results")}</div>}>
          <ul class="h-results">
            <For each={props.results}>
              {(r) => (
                <li class="h-result" data-status={r.status} data-path={r.path}>
                  <div class="h-result-row">
                    <span class={["h-result-status", `h-status-${r.status}`]}>{statusText(r.status)}</span>
                    <code class="h-result-path">{r.path}</code>
                    <span class="h-result-summary">{r.summary}</span>
                  </div>
                  <Show when={r.output && r.output.length > 0}>
                    <pre class="h-result-output" data-testid="run-output">{r.output!.join("\n")}</pre>
                  </Show>
                </li>
              )}
            </For>
          </ul>
        </Show>
      </section>
    </div>
  );
}

function statusText(s: string): string {
  if (s === "ok") return t("app.status.ok");
  if (s === "failed") return t("app.status.failed");
  if (s === "running") return t("app.status.running");
  return s;
}
