// The sample site's renderers, registered per kind by the docs block's
// `renderers = [js("./docs/renderers.ts")]` and run through the JS host
// when the docs are generated. They only arrange what the generator hands
// them (prose, children, a tag's body and typed attributes): every word
// shown comes from the spec.
import { defineRenderer, escapeHtml } from "@heron/runtime";

/** A paragraph's inner HTML, for prose shown inline. */
const inline = (html = "") => html.replace(/^<p>([\s\S]*)<\/p>$/, "$1");

export default [
  defineRenderer("components.steps", (node) => {
    const start = typeof node.attrs?.start === "number" ? node.attrs.start : 1;
    return `<ol class="steps" id="${escapeHtml(node.anchor ?? "")}" start="${start}">${node.children ?? ""}</ol>`;
  }),

  defineRenderer("components.step", (node) => `<li class="step">${node.prose ?? ""}</li>`),

  // A tag in prose (`<status level="2">…</status>`) or a block in a slot:
  // the level arrives as a number, typed by the kind's `level: Int`.
  defineRenderer("components.status", (node) => {
    const level = typeof node.attrs?.level === "number" ? node.attrs.level : 1;
    const body = node.body ?? inline(node.prose);
    return { html: `<span class="status-badge" data-level="${level}">${body}</span>`, block: false };
  }),
];
