// A transport, as the Interop tab writes one: it implements std/apis'
// Transport interface and sends an op's requests to the in-memory store.
import { defineTransport } from "@heron/runtime/apis";
import type { inventory } from "../.heron/types.ts";

export default defineTransport({
  async send(op, request, ctx) {
    const store = await ctx.resource<inventory.Store>("inventory.store");
    const { sku } = request as { sku: string };
    const item = store.items.find((i) => i.sku === sku);
    return { op: op.path, found: item !== undefined, item: item ?? null };
  },
});
