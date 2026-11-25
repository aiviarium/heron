// tools/orders.ts
import { writeFile } from "node:fs/promises";
import { bind } from "../.heron/bindings";
import { toCsv } from "./csv";

export const exportOrders = bind.export_orders(async ({ pet_id }, ctx) => {
  const orders = await ctx.call("store.list_orders", { pet_id });
  const path = ctx.temp("orders.csv");
  await writeFile(path, toCsv(orders));
  return path;
});
