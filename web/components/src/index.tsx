// The shared UI: UI SDK's behaviours (dialog, command, tree, resizable…)
// composed into Heron's components and styled from the one token set. The
// app, the editor and the docs islands all build from these.
import type { JSX } from "@solidjs/web";
import { For, Show, createMemo } from "solid-js";
import { Command, CommandEmpty, CommandGroup, CommandGroupLabel, CommandInput, CommandItem, CommandList } from "@ui-sdk/solid/command";
import { Dialog, DialogBackdrop, DialogContent, DialogTitle } from "@ui-sdk/solid/dialog";
import { Tree, TreeGroup, TreeItem, TreeItemIndicator, TreeItemLabel, treeFromPaths, type TreeItemData } from "@ui-sdk/solid/tree";
import { t } from "@heron/strings";

export { Resizable, ResizableHandle, ResizablePanel } from "@ui-sdk/solid/resizable";

const paths: Record<string, string> = {
  search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
  chevron: '<path d="m9 18 6-6-6-6"/>',
  file: '<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/>',
  play: '<path d="M7 5v14l11-7z"/>',
  save: '<path d="M5 4h11l3 3v13H5z"/><path d="M8 4v5h7V4"/><path d="M8 20v-6h8v6"/>',
  format: '<path d="M4 6h16"/><path d="M8 12h12"/><path d="M4 18h16"/>',
  preview: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M12 4v16"/>',
  theme: '<circle cx="12" cy="12" r="8"/><path d="M12 4a8 8 0 0 0 0 16z" fill="currentColor"/>',
  close: '<path d="M6 6l12 12M18 6 6 18"/>',
  branch: '<circle cx="6" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="8" r="2"/><path d="M6 7v10"/><path d="M18 10c0 4-6 3-11 7"/>',
  dot: '<circle cx="12" cy="12" r="4" fill="currentColor" stroke="none"/>',
};

export type IconName = keyof typeof paths;

/** A 16 px stroke icon; decorative unless given a label. */
export function Icon(props: { name: IconName; label?: string; class?: string }) {
  return (
    <svg
      class={["h-icon", props.class]}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="1.75"
      stroke-linecap="round"
      stroke-linejoin="round"
      aria-hidden={props.label ? undefined : "true"}
      aria-label={props.label}
      role={props.label ? "img" : undefined}
      innerHTML={paths[props.name]}
    />
  );
}

/** A grey button; `primary` fills it with the text colour, once per view. */
export function Button(props: {
  children: JSX.Element;
  onClick?: (e: MouseEvent) => void;
  primary?: boolean;
  size?: "sm" | "md" | "lg";
  title?: string;
  disabled?: boolean;
  "data-testid"?: string;
}) {
  return (
    <button
      type="button"
      class={["h-button", `h-size-${props.size ?? "md"}`, { "h-primary": !!props.primary }]}
      onClick={(e) => props.onClick?.(e)}
      title={props.title}
      disabled={props.disabled}
      data-testid={props["data-testid"]}
    >
      {props.children}
    </button>
  );
}

/** A transparent icon button with an accessible name. */
export function IconButton(props: { icon: IconName; label: string; onClick?: (e: MouseEvent) => void; pressed?: boolean; shortcut?: string }) {
  return (
    <button
      type="button"
      class={["h-icon-button", { "h-pressed": !!props.pressed }]}
      aria-label={props.label}
      aria-pressed={props.pressed === undefined ? undefined : props.pressed ? "true" : "false"}
      aria-keyshortcuts={props.shortcut}
      onClick={(e) => props.onClick?.(e)}
    >
      <Icon name={props.icon} />
    </button>
  );
}

const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform);

/** Keys of a shortcut such as "mod+k", for the current platform. */
export function Kbd(props: { keys: string }) {
  const parts = createMemo(() =>
    props.keys
      .split("+")
      .map((k) =>
        k === "mod" || k === "shift" || k === "alt" ? t(`app.key.${k}${mac ? "_mac" : ""}`) : k.length === 1 ? k.toUpperCase() : k,
      ),
  );
  return (
    <kbd class="h-kbd">
      <For each={parts()}>{(p) => <span>{p}</span>}</For>
    </kbd>
  );
}

/** A palette entry. `group` names its section. */
export interface PaletteItem {
  id: string;
  title: string;
  group: string;
  hint?: string;
  shortcut?: string;
}

/** The ⌘K palette: a dialog holding UI SDK's command list. */
export function CommandPalette(props: { open: boolean; onOpenChange: (open: boolean) => void; items: PaletteItem[]; onRun: (id: string) => void }) {
  const groups = createMemo(() => {
    const m = new Map<string, PaletteItem[]>();
    for (const it of props.items) m.set(it.group, [...(m.get(it.group) ?? []), it]);
    return [...m.entries()];
  });
  return (
    <Dialog open={props.open} onOpenChange={(o: boolean) => props.onOpenChange(o)}>
      <DialogBackdrop class="h-backdrop" />
      <DialogContent class="h-palette" data-testid="palette" initialFocus=".h-palette-input input">
        <DialogTitle class="h-visually-hidden">{t("app.palette")}</DialogTitle>
        <Command
          onAction={(id: string) => {
            props.onOpenChange(false);
            props.onRun(id);
          }}
        >
          <div class="h-palette-input">
            <Icon name="search" />
            <CommandInput aria-label={t("app.palette_input")} placeholder={t("app.palette_input")} />
          </div>
          <CommandEmpty class="h-palette-empty">{t("app.no_matches")}</CommandEmpty>
          <CommandList class="h-palette-list">
            <For each={groups()}>
              {([name, items]) => (
                <CommandGroup>
                  <CommandGroupLabel class="h-group-label">{name}</CommandGroupLabel>
                  <For each={items}>
                    {(it) => (
                      <CommandItem value={it.id} keywords={[it.title, it.hint ?? ""]} class="h-palette-item">
                        <span class="h-palette-title">{it.title}</span>
                        <Show when={it.hint}>
                          <span class="h-palette-hint">{it.hint}</span>
                        </Show>
                        <Show when={it.shortcut}>{(k) => <Kbd keys={k()} />}</Show>
                      </CommandItem>
                    )}
                  </For>
                </CommandGroup>
              )}
            </For>
          </CommandList>
        </Command>
      </DialogContent>
    </Dialog>
  );
}

/** Counts shown beside a tree row. */
export interface Counts {
  errors: number;
  warnings: number;
}

function Rows(props: { items: TreeItemData[]; counts: (path: string) => Counts | undefined; dirty: (path: string) => boolean }) {
  return (
    <For each={props.items}>
      {(item) => (
        <TreeItem value={item.value} class="h-tree-item">
          <TreeItemLabel class="h-tree-row" data-path={item.value}>
            <Show when={item.children} fallback={<span class="h-tree-spacer" />}>
              <TreeItemIndicator class="h-tree-chevron">
                <Icon name="chevron" />
              </TreeItemIndicator>
            </Show>
            <span class="h-tree-name">{item.label}</span>
            <Show when={props.dirty(item.value)}>
              <span class="h-dirty" title={t("app.unsaved")}>
                <Icon name="dot" />
              </span>
            </Show>
            <Show when={!item.children && props.counts(item.value)}>
              {(c) => (
                <span class="h-counts">
                  <Show when={c().errors > 0}>
                    <span class="h-count h-error" title={c().errors === 1 ? t("app.error_one") : t("app.errors", { n: c().errors })}>
                      {c().errors}
                    </span>
                  </Show>
                  <Show when={c().warnings > 0}>
                    <span class="h-count h-warn" title={c().warnings === 1 ? t("app.warning_one") : t("app.warnings", { n: c().warnings })}>
                      {c().warnings}
                    </span>
                  </Show>
                </span>
              )}
            </Show>
          </TreeItemLabel>
          <Show when={item.children}>
            {(children) => (
              <TreeGroup class="h-tree-group">
                <Rows items={children()} counts={props.counts} dirty={props.dirty} />
              </TreeGroup>
            )}
          </Show>
        </TreeItem>
      )}
    </For>
  );
}

/** A file tree from paths, with problem counts per file. */
export function FileTree(props: {
  label: string;
  paths: string[];
  current: string | null;
  counts: (path: string) => Counts | undefined;
  dirty?: (path: string) => boolean;
  onOpen: (path: string) => void;
}) {
  const items = createMemo(() => treeFromPaths(props.paths));
  const files = createMemo(() => new Set(props.paths));
  const folders = createMemo(() => {
    const out: string[] = [];
    const walk = (xs: TreeItemData[]) => {
      for (const x of xs) if (x.children) (out.push(x.value), walk(x.children));
    };
    walk(items());
    return out;
  });
  return (
    <Tree
      class="h-tree"
      aria-label={props.label}
      items={items()}
      current={props.current}
      value={props.current}
      defaultExpanded={folders()}
      activation="manual"
      expandOnPress
      onAction={(v: string) => {
        if (files().has(v)) props.onOpen(v);
      }}
    >
      <Rows items={items()} counts={props.counts} dirty={props.dirty ?? (() => false)} />
    </Tree>
  );
}
