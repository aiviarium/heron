// The shell's slots: panels, gutter result providers, commands and
// renderers per kind. Every part registers through the same calls, so the
// shell never knows which parts exist.
import type { JSX } from "@solidjs/web";
import { createSignal } from "solid-js";
import type { NodeResult, OutlineNode } from "./api.gen.ts";

/** A view in a slot of the editor screen. */
export interface Panel {
  id: string;
  /** Its tab label, from the catalogue. */
  title: string;
  /** Where it shows: the bottom bar or under the preview. */
  slot: "bottom" | "preview";
  render: () => JSX.Element;
}

/** A mark in the gutter beside a node. */
export interface GutterMark {
  line: number;
  /** ok, failed, running, or none for an action only. */
  status?: "ok" | "failed" | "running";
  /** A short value shown beside the node, such as a duration. */
  label?: string;
  /** The hover text. */
  title?: string;
  /** Runs when the mark is clicked. */
  action?: () => void;
}

/** What a gutter provider sees for the file shown. */
export interface GutterContext {
  file: string;
  outline: OutlineNode[];
  results: NodeResult[];
}

/** Puts results in the gutter beside the nodes they belong to. */
export interface GutterProvider {
  id: string;
  provide: (ctx: GutterContext) => GutterMark[];
}

/** An action in the palette, with an optional shortcut such as "mod+s". */
export interface Command {
  id: string;
  title: string;
  group?: string;
  shortcut?: string;
  hint?: string;
  /** Whether it shows now; omitted means always. */
  when?: () => boolean;
  run: () => void | Promise<void>;
}

/**
 * Renders a node of a kind in the preview: it receives the server's HTML
 * for the node and may enhance it, such as with an island. A kind without
 * one shows the server's HTML as it is.
 */
export interface KindRenderer {
  kind: string;
  mount: (el: HTMLElement, node: { path: string; kind: string }) => void | (() => void);
}

function registry<T extends { id: string }>() {
  const [items, setItems] = createSignal<T[]>([], { ownedWrite: true });
  return {
    items,
    add(item: T): () => void {
      if (items().some((x) => x.id === item.id)) throw new Error(`"${item.id}" is registered twice`);
      setItems((xs) => [...xs, item]);
      return () => setItems((xs) => xs.filter((x) => x !== item));
    },
  };
}

const panels = registry<Panel>();
const gutters = registry<GutterProvider>();
const commands = registry<Command>();
const renderers = registry<KindRenderer & { id: string }>();

export const registerPanel = panels.add;
export const registerGutterProvider = gutters.add;
export const registerCommand = commands.add;
export const registerRenderer = (r: KindRenderer) => renderers.add({ ...r, id: r.kind });

export const allPanels = panels.items;
export const allGutterProviders = gutters.items;
export const allCommands = commands.items;
export const rendererFor = (kind: string) => renderers.items().find((r) => r.kind === kind);

/** The marks of every provider for a file. */
export function gutterMarks(ctx: GutterContext): GutterMark[] {
  return allGutterProviders().flatMap((p) => p.provide(ctx));
}
