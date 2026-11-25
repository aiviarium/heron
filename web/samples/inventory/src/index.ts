// The TS side of the inventory package. Each export is bound to its Heron
// declaration through the generated `bind`, so `tsc` checks its arguments,
// result and instance types against package.heron.
import { readFileSync } from "node:fs";
import type { LintFinding } from "@heron/runtime";
import { bind } from "../.heron/bindings.ts";
import type { inventory } from "../.heron/types.ts";

/** Turns a name into a slug. */
export const slugify = bind.inventory.slugify(({ text }) =>
  text
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, ""),
);

/** Counts an item in the store, reading the store through ctx.resource. */
export const count = bind.inventory.count(async ({ sku }, ctx) => {
  const store = await ctx.resource<inventory.Store>("inventory.store");
  return store.items.find((i) => i.sku === sku)?.count ?? 0;
});

/** Describes an item, calling an op and a Heron func back through ctx.call. */
export const describe = bind.inventory.describe(async ({ sku }, ctx) => {
  const found = await ctx.call<inventory.Lookup>("inventory.shelf.find", { sku });
  if (!found.item) return `${sku}: not stocked`;
  return ctx.call<string>("inventory.label", { name: found.item.name, count: found.item.count });
});

/** An in-memory store, seeded with two items; snapshots are copies. */
export const store = bind.inventory.store({
  setup: () => ({
    items: [
      { sku: "kib-1", name: "Kibble", count: 12 },
      { sku: "toy-7", name: "Squeaky toy", count: 3 },
    ],
  }),
  reset(s) {
    s.items.splice(0, s.items.length);
  },
  snapshot: (s) => structuredClone(s.items),
  restore(s, snap) {
    s.items.splice(0, s.items.length, ...(snap as inventory.Item[]));
  },
  fork: (s) => ({ items: structuredClone(s.items) }),
});

/** Reports nodes whose docs or prose still say TODO, with a fix that drops the marker. */
export const noTodo = bind.inventory.no_todo({
  run(nodes) {
    return nodes
      .filter((n) => n.prose?.some((p) => p.includes("TODO")))
      .map((n) => {
        const finding: LintFinding = { node: n.path, message: `${n.name ?? n.path} still says TODO`, severity: "warning" };
        if (n.file && n.start !== undefined && n.end !== undefined) {
          const src = Buffer.from(readFileSync(n.file));
          const at = src.subarray(n.start, n.end).indexOf("TODO: ");
          if (at >= 0) {
            const start = n.start + at;
            finding.fixes = [{ title: "drop the TODO marker", edits: [{ file: n.file, start, end: start + "TODO: ".length, new: "" }] }];
          }
        }
        return finding;
      });
  },
});

/** Writes a catalog of the tree's types, as a generation adapter. */
export const catalog = bind.inventory.catalog({
  generate(tree) {
    const lines: string[] = [];
    const walk = (n: typeof tree) => {
      if (n.kind === "types.type") lines.push(`- ${n.path}`);
      for (const c of n.children ?? []) walk(c);
    };
    walk(tree);
    return [{ path: "catalog.md", content: `# Types\n\n${lines.join("\n")}\n`, ignored: true }];
  },
});
