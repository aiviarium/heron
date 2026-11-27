// Heron values as they cross the boundary, from the Interop tab's table:
// integers stay exact, bytes stay binary, and every value of Any is the
// Sparrow union Value, whose variant says its Heron type. The variants
// match internal/core/extension/protocol/bind.go.
import type { Facet, TypeRef } from "@aiviarium/sparrow";

/** A value of a union: the variant's name in `type`, as the Interop tab's tagged unions have it. */
export class Variant<T = unknown> {
  constructor(
    readonly type: string,
    readonly value: T,
  ) {}
  toJSON(): unknown {
    return { $variant: this.type, value: this.value };
  }
}

/** A reference to a node of the tree, by path; `ctx.node(ref.path)` resolves it. */
export class Ref {
  constructor(readonly path: string) {}
  toJSON(): unknown {
    return { $ref: this.path };
  }
  toString(): string {
    return this.path;
  }
}

/** A length of time, in whole milliseconds. */
export class Duration {
  constructor(readonly millis: number) {}
  toJSON(): unknown {
    return { $duration: this.millis };
  }
}

/** A decimal fraction, mantissa × 10^exp, so amounts of money stay exact. */
export class Decimal {
  constructor(
    readonly mantissa: bigint,
    readonly exp: number,
  ) {}
  static parse(s: string): Decimal {
    const m = /^(-?)(\d+)(?:\.(\d+))?$/.exec(s.trim());
    if (!m) throw new TypeError(`not a decimal: ${JSON.stringify(s)}`);
    const frac = m[3] ?? "";
    return new Decimal(BigInt(`${m[1]}${m[2]}${frac}`), -frac.length);
  }
  toString(): string {
    const neg = this.mantissa < 0n;
    let digits = (neg ? -this.mantissa : this.mantissa).toString();
    if (this.exp >= 0) return (neg ? "-" : "") + digits + "0".repeat(this.exp);
    const n = -this.exp;
    while (digits.length <= n) digits = "0" + digits;
    return (neg ? "-" : "") + digits.slice(0, digits.length - n) + "." + digits.slice(digits.length - n);
  }
  toJSON(): unknown {
    return { $decimal: this.toString() };
  }
}

/** An mdast root, as Prose values arrive; wrap one with `prose(tree)` to send it. */
export interface MdastRoot {
  type: "root";
  children: unknown[];
  [key: string]: unknown;
}

class ProseValue {
  constructor(readonly tree: MdastRoot) {}
  toJSON(): unknown {
    return { $prose: this.tree };
  }
}

/** Marks an mdast tree as a Prose value for sending. */
export function prose(tree: MdastRoot): unknown {
  return new ProseValue(tree);
}

/** A value of the Sparrow union Value, as the Sparrow codec represents it. */
export interface ValueUnion {
  variant: string;
  value: unknown;
}

const int64Min = -(2n ** 63n);
const int64Max = 2n ** 63n - 1n;
const uint64Max = 2n ** 64n - 1n;

function sortedEntries(o: Record<string, unknown>): [string, unknown][] {
  return Object.entries(o)
    .filter(([, v]) => v !== undefined)
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
}

/** A Heron value as the Value union. */
export function toValue(x: unknown): ValueUnion {
  const u = (variant: string, value: unknown): ValueUnion => ({ variant, value });
  if (x === null || x === undefined) return u("null", null);
  switch (typeof x) {
    case "boolean":
      return u("bool", x);
    case "number":
      return Number.isInteger(x) ? u("int", x) : u("float", x);
    case "bigint":
      if (x >= int64Min && x <= int64Max) return u("int", x);
      if (x > int64Max && x <= uint64Max) return u("uint", x);
      return u("bigint", x.toString());
    case "string":
      return u("string", x);
  }
  if (x instanceof Uint8Array) return u("bytes", x);
  if (x instanceof Decimal) return u("decimal", x.toString());
  if (x instanceof Date) return u("time", x.toISOString());
  if (x instanceof Duration) return u("duration", x.millis);
  if (x instanceof Ref) return u("ref", x.path);
  if (x instanceof Variant) return u("variant", { name: x.type, value: toValue(x.value) });
  if (x instanceof Set) return u("set", [...x].map(toValue));
  if (Array.isArray(x)) return u("list", x.map(toValue));
  if (x instanceof ProseValue) return u("prose", Object.fromEntries(sortedEntries(x.tree).map(([k, v]) => [k, toValue(v)])));
  if (x instanceof Map) return toValue(Object.fromEntries(x));
  if (typeof x === "object") return u("map", Object.fromEntries(sortedEntries(x as Record<string, unknown>).map(([k, v]) => [k, toValue(v)])));
  throw new TypeError(`a ${typeof x} has no Heron value`);
}

/** The Value union read back as a Heron value. */
export function fromValue(x: unknown): unknown {
  if (x === null || x === undefined) return null;
  const { variant, value } = x as ValueUnion;
  switch (variant) {
    case "null":
      return null;
    case "bool":
    case "int":
    case "float":
    case "string":
    case "bytes":
      return value;
    case "uint":
      return BigInt(value as number | bigint);
    case "bigint":
      return BigInt(value as string);
    case "decimal":
      return Decimal.parse(value as string);
    case "time":
      return new Date(value as string);
    case "duration":
      return new Duration(Number(value));
    case "ref":
      return new Ref(value as string);
    case "variant": {
      const v = value as { name: string; value: unknown };
      return new Variant(v.name, fromValue(v.value));
    }
    case "list":
      return (value as unknown[]).map(fromValue);
    case "set":
      return new Set((value as unknown[]).map(fromValue));
    case "map":
    case "prose":
      return Object.fromEntries(Object.entries(value as Record<string, unknown>).map(([k, v]) => [k, fromValue(v)]));
  }
  throw new TypeError(`a Value with variant ${String(variant)} this side doesn't know`);
}

/**
 * Converts between the protocol's messages, as protocol.gen.ts types them,
 * and the Sparrow codec's representation, by a facet's types: Any becomes
 * the Value union, everything else is already in its shape.
 */
export class Binder {
  private valueId = -1;
  constructor(readonly facet: Facet) {
    for (const t of facet.types) if (t.kind === "union" && t.name === "Value") this.valueId = t.id;
  }

  typeNamed(name: string): number | undefined {
    return this.facet.types.find((t) => t.name === name)?.id;
  }

  toWire(t: TypeRef, v: unknown): unknown {
    return this.walk(t, v, true);
  }

  fromWire(t: TypeRef, v: unknown): unknown {
    return this.walk(t, v, false);
  }

  private walk(t: TypeRef, v: unknown, out: boolean): unknown {
    if (t === this.valueId) return out ? toValue(v) : fromValue(v);
    if (v === null || v === undefined) return v;
    if (typeof t === "number") {
      const td = this.facet.types.find((x) => x.id === t);
      if (!td || td.kind !== "struct") return v;
      const o = v as Record<string, unknown>;
      const r: Record<string, unknown> = {};
      for (const f of td.fields) {
        if (o[f.name] === undefined) continue;
        r[f.name] = this.walk(f.type, o[f.name], out);
      }
      return r;
    }
    switch (t[0]) {
      case 0:
        return (v as unknown[]).map((e) => this.walk(t[1], e, out));
      case 1: {
        const entries = v instanceof Map ? [...v.entries()] : Object.entries(v as Record<string, unknown>);
        return Object.fromEntries(entries.filter(([, e]) => e !== undefined).map(([k, e]) => [k, this.walk(t[2], e, out)]));
      }
      case 2:
      case 3:
        return this.walk(t[1], v, out);
    }
    return v;
  }
}
