// Heron values as they cross the boundary, from the Interop tab's table:
// integers stay exact, bytes stay binary, and Heron's type tags become CBOR
// tags, the standard ones where they exist. The tag numbers match
// internal/core/extension/protocol/values.go.
import { decode as cborDecode, encode as cborEncode, type ITag, type TagDecoder, type TagNumber } from "cbor2";

export const TAG_VARIANT = 1214603777;
export const TAG_DURATION = 1214603778;
export const TAG_PROSE = 1214603779;
export const TAG_REF = 1214603780;
export const TAG_SET = 258;
export const TAG_DECIMAL = 4;

/** A value of a union: the variant's name in `type`, as the Interop tab's tagged unions have it. */
export class Variant<T = unknown> {
  constructor(
    readonly type: string,
    readonly value: T,
  ) {}
  toCBOR(): [TagNumber, unknown] {
    return [TAG_VARIANT, [this.type, this.value]];
  }
  toJSON(): unknown {
    return { $variant: this.type, value: this.value };
  }
}

/** A reference to a node of the tree, by path; `ctx.node(ref.path)` resolves it. */
export class Ref {
  constructor(readonly path: string) {}
  toCBOR(): [TagNumber, unknown] {
    return [TAG_REF, this.path];
  }
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
  toCBOR(): [TagNumber, unknown] {
    return [TAG_DURATION, this.millis];
  }
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
  toCBOR(): [TagNumber, unknown] {
    return [TAG_DECIMAL, [this.exp, this.mantissa]];
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
  toCBOR(): [TagNumber, unknown] {
    return [TAG_PROSE, this.tree];
  }
  toJSON(): unknown {
    return { $prose: this.tree };
  }
}

/** Marks an mdast tree as a Prose value for sending. */
export function prose(tree: MdastRoot): unknown {
  return new ProseValue(tree);
}

const tags = new Map<TagNumber, TagDecoder>([
  [TAG_VARIANT, (t: ITag) => {
    const [name, value] = t.contents as [string, unknown];
    return new Variant(name, value);
  }],
  [TAG_DURATION, (t: ITag) => new Duration(Number(t.contents))],
  [TAG_PROSE, (t: ITag) => t.contents],
  [TAG_REF, (t: ITag) => new Ref(String(t.contents))],
  [TAG_DECIMAL, (t: ITag) => {
    const [exp, m] = t.contents as [number | bigint, number | bigint];
    return new Decimal(BigInt(m), Number(exp));
  }],
]);

/** An encoding of frames: CBOR, or JSON for reading the traffic. */
export interface Wire {
  name: "cbor" | "json";
  encode(v: unknown): Uint8Array;
  decode(b: Uint8Array): unknown;
}

export const cbor: Wire = {
  name: "cbor",
  encode: (v) => cborEncode(v),
  decode: (b) => cborDecode(b, { tags }),
};

const utf8 = new TextEncoder();
const fromUtf8 = new TextDecoder();

export const json: Wire = {
  name: "json",
  encode: (v) =>
    utf8.encode(
      JSON.stringify(v, (_k, x) => {
        if (typeof x === "bigint") return Number(x);
        if (x instanceof Set) return { $set: [...x] };
        if (x instanceof Uint8Array) return Buffer.from(x).toString("base64");
        return x;
      }),
    ),
  decode: (b) => JSON.parse(fromUtf8.decode(b)),
};

export function wireByName(name: string | undefined): Wire {
  if (!name || name === "cbor") return cbor;
  if (name === "json") return json;
  throw new Error(`unknown wire ${JSON.stringify(name)}: use cbor or json`);
}
