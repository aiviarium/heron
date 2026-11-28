/**
 * The tree-sitter grammar of Heron, for editors: highlighting, folding and
 * structural selection without starting the binary. It follows the surface
 * grammar on the Core format tab (docs/design/01-core-format.md) and is
 * tested against the same fixtures as the Go parser in internal/core/syntax.
 * Everything semantic comes from `heron lsp`.
 *
 * Prose is kept flat where the Go parser nests: a tag that spans `|` lines
 * is an opening and a closing tag around ordinary inline text.
 */

const PREC = {
  or: 1,
  and: 2,
  compare: 3,
  union: 4,
  add: 5,
  mul: 6,
  unary: 7,
  call: 8,
  field: 9,
};

const IDENT = /[A-Za-z_][A-Za-z0-9_]*/;

// The standard kinds that take their text as Verbatim: std/grammar's
// production and token, and std/doc's code and math. As the Go parser
// does, a `{{ }}` body of a node whose `:` slot or callee names one, by its
// last segment, is text: no tags, inserts, links or code spans. The names
// are keywords only where a name may stand, and read as identifiers.
const VERBATIM = ['production', 'token', 'code', 'math'];

const commaSep = (rule) => optional(seq(rule, repeat(seq(',', rule)), optional(',')));

module.exports = grammar({
  name: 'heron',

  word: ($) => $.identifier,

  externals: ($) => [
    $._line_end, // the newline that ends a `|` or `///` line
    $.string, // "…" or a run of three or more quotes, with $name and ${…} inside
    $.backtick, // `…` code or a raw string; three or more may span lines (fences)
    $._prose_bar, // a `|` that starts a prose line
    $._union_bar, // a `|` between two types on one line
    $._error_sentinel,
  ],

  extras: ($) => [/[ \t\r\n\f﻿]/, $.comment],

  conflicts: ($) => [
    [$.text_node, $.invoke],
    [$.enter, $.invoke],
    [$.scope, $.invoke],
    [$.path_expression, $.named_type],
    [$._member_path, $.scope],
    [$._member_path],
    [$.path],
    [$.parameter, $.path],
    [$._inline, $.link],
    [$._inline, $.insert],
    [$._expression, $._type],
    [$.named_type, $._expression],
    [$.named_type, $.call],
    [$.path_expression, $.call],
    [$.parenthesized, $.cast],
    [$.expression_statement],
    [$.prose_lines],
    [$.doc],
    [$._verbatim_path, $._member_path],
    [$._verbatim_path, $.path],
    [$._name, $._verbatim_path],
  ],

  rules: {
    source_file: ($) => repeat($._member),

    _member: ($) =>
      choice(
        $.doc,
        $.annotation,
        $.declaration,
        alias($.verbatim_declaration, $.declaration),
        alias($.verbatim_invoke, $.invoke),
        alias($.verbatim_text_node, $.text_node),
        $.value,
        $.enter,
        $.text_node,
        $.scope,
        $.invoke,
        $.set,
        $.let,
        $.action,
        $.prose_lines,
        $.prose_block,
      ),

    comment: (_) => token(choice('//', seq('//', /[^\/\n][^\n]*/), seq('////', /[^\n]*/))),

    // --- names ---

    identifier: (_) => IDENT,

    _name: ($) => choice($.identifier, alias($._verbatim_kind, $.identifier)),

    _verbatim_kind: (_) => choice(...VERBATIM),

    // A path whose last segment names a verbatim kind.
    _verbatim_path: ($) => seq(repeat(seq($._name, '.')), alias($._verbatim_kind, $.identifier)),

    // After a dot, a segment may be a use case's step, as `.3` or `.2a`.
    path: ($) => seq($._name, repeat(seq('.', choice($._name, alias($._step, $.identifier))))),

    _step: (_) => token.immediate(/[0-9]+[a-z]?/),

    // A member's path may have string segments, as in `"std/ci" = { … }`.
    _member_path: ($) =>
      seq(choice($._name, $.string), repeat(seq('.', choice($._name, $.string)))),

    // A flag is plain or qualified by its package, as `<tests.slow>`.
    flags: ($) => seq(token.immediate('<'), commaSep(seq($._name, repeat(seq(token.immediate('.'), $._name)))), '>'),

    // --- members ---

    annotation: ($) => seq('@', $.path, optional($.flags), optional($.arguments)),

    declaration: ($) =>
      prec.right(seq(
        field('name', alias($._member_path, $.path)),
        optional($.flags),
        optional($.arguments),
        optional('?'),
        ':',
        field('type', $._type),
        optional($._body),
      )),

    // A declaration, call or text node of a verbatim kind, with its text.
    verbatim_declaration: ($) =>
      prec.dynamic(1, seq(
        field('name', alias($._member_path, $.path)),
        optional($.flags),
        optional($.arguments),
        optional('?'),
        ':',
        field('type', alias($.verbatim_type, $.named_type)),
        alias($.verbatim_block, $.prose_block),
      )),

    verbatim_type: ($) => seq(alias($._verbatim_path, $.path), optional($.flags), optional($.arguments)),

    verbatim_invoke: ($) =>
      prec.dynamic(1, seq(
        field('name', alias($._verbatim_path, $.path)),
        optional($.flags),
        $.arguments,
        alias($.verbatim_block, $.prose_block),
      )),

    verbatim_text_node: ($) =>
      prec.dynamic(1, seq(field('name', alias($._verbatim_path, $.path)), alias($.verbatim_block, $.prose_block))),

    verbatim_block: ($) =>
      seq(
        '{{',
        repeat(choice(
          alias(token(prec(1, /([^}\\]|\\[^}]|\}[^}])+/)), $.text),
          alias(token(prec(2, '\\}}')), $.escape),
          alias(choice('}', '\\'), $.text),
        )),
        '}}',
      ),

    value: ($) =>
      seq(
        field('name', alias($._member_path, $.path)),
        optional($.flags),
        optional($.arguments),
        optional('?'),
        ':',
        field('type', $._type),
        '=',
        field('value', $._expression),
      ),

    enter: ($) => seq(field('name', alias($._member_path, $.path)), choice($.struct_body, $.do_block)),

    text_node: ($) => seq(field('name', alias($._member_path, $.path)), $.prose_block),

    scope: ($) => seq(optional(field('name', alias($._member_path, $.path))), '.', $.struct_body),

    invoke: ($) =>
      choice(
        prec.right(seq(
          field('name', alias($._member_path, $.path)),
          optional($.flags),
          $.arguments,
          optional(choice($._body, seq('=', $._expression))),
        )),
        // A bare name, such as an enum's variant.
        prec.dynamic(-1, seq(field('name', alias($._member_path, $.path)), optional($.flags))),
      ),

    set: ($) => seq(field('name', alias($._member_path, $.path)), '=', field('value', $._expression)),

    let: ($) => seq('let', field('name', $._name), optional(seq(':', $._type)), '=', $._expression),

    action: ($) => $.do_block,

    _body: ($) => choice($.struct_body, $.prose_block, $.do_block),

    struct_body: ($) => seq('{', repeat($._member), '}'),

    do_block: ($) => seq('do', $.code),

    arguments: ($) => seq('(', commaSep(choice($.parameter, $.named_argument, $._expression)), ')'),

    parameter: ($) =>
      seq(
        repeat(choice($.doc, $.annotation)),
        field('name', $._name),
        optional($.flags),
        optional('?'),
        ':',
        field('type', $._type),
        optional(seq('=', field('default', $._expression))),
      ),

    named_argument: ($) => seq(field('name', $._name), '=', $._expression),

    // --- code ---

    code: ($) => seq('{', repeat($._statement), '}'),

    _statement: ($) =>
      choice($.let_statement, $.assignment, $.return_statement, $.expression_statement),

    let_statement: ($) =>
      seq('let', field('name', $._name), optional(seq(':', $._type)), '=', $._expression),

    assignment: ($) => seq($.path, '=', $._expression),

    return_statement: ($) => prec.right(seq('return', optional($._expression))),

    expression_statement: ($) => seq($._expression, optional($.code)),

    // --- expressions ---

    _expression: ($) =>
      choice(
        $.number,
        $.boolean,
        $.string,
        $.backtick,
        $.path_expression,
        $.call,
        $.field_expression,
        $.array,
        $.map,
        $.parenthesized,
        $.cast,
        $.binary,
        $.unary,
        $.if_expression,
        $.for_expression,
        $.prose_lines,
        $.prose_block,
      ),

    number: (_) => /\d+(\.\d+)?([eE][+-]?\d+)?/,

    boolean: (_) => choice('true', 'false'),

    path_expression: ($) => $.path,

    call: ($) =>
      prec(PREC.call, seq(field('callee', choice($.path, $.field_expression, $.call, $.parenthesized)), optional($.flags), $.arguments)),

    field_expression: ($) =>
      prec(PREC.field, seq(choice($.call, $.parenthesized, $.array), '.', $._name)),

    array: ($) => seq('[', commaSep($._expression), ']'),

    map: ($) => seq('{', repeat(seq($.map_entry, optional(','))), '}'),

    // A key may be a string, as in `{ "fragile" = "yes" }`.
    map_entry: ($) => seq(alias($._member_path, $.path), '=', $._expression),

    parenthesized: ($) => seq('(', $._expression, ')'),

    cast: ($) => prec.right(PREC.unary, seq('(', $._type, ')', $._expression)),

    binary: ($) =>
      choice(
        ...[
          [PREC.or, 'or'],
          [PREC.and, 'and'],
          [PREC.compare, choice('==', '!=', '<', '<=', '>', '>=', 'in', 'is')],
          [PREC.add, choice('+', '-')],
          [PREC.mul, choice('*', '/', '%')],
        ].map(([p, op]) => prec.left(p, seq($._expression, field('operator', op), $._expression))),
        prec.left(PREC.union, seq($._expression, field('operator', alias($._union_bar, '|')), $._expression)),
      ),

    unary: ($) => prec(PREC.unary, seq(choice('not', '-'), $._expression)),

    if_expression: ($) =>
      prec.right(seq('if', field('condition', $._expression), $.code, optional(seq('else', choice($.code, $.if_expression))))),

    for_expression: ($) => seq('for', $._name, 'in', $._expression, $.code),

    // --- types ---

    _type: ($) => choice($.named_type, $.optional_type, $.union_type, $.intersection_type),

    named_type: ($) => prec.right(seq($.path, optional($.flags), optional($.arguments))),

    optional_type: ($) => seq($._type, '?'),

    union_type: ($) => prec.left(1, seq($._type, alias($._union_bar, '|'), $._type)),

    intersection_type: ($) => prec.left(2, seq($._type, '&', $._type)),

    // --- docs and prose ---

    doc: ($) => repeat1($.doc_line),

    // A `}` is text in a line, as when a code span that doesn't close on
    // its line holds one.
    doc_line: ($) => seq('///', repeat(choice($._inline, alias('}', $.text))), $._line_end),

    prose_lines: ($) => repeat1($.prose_line),

    prose_line: ($) => seq(alias($._prose_bar, '|'), optional($.list_marker), repeat(choice($._inline, alias('}', $.text))), $._line_end),

    // Markdown's marks in prose: a list item's marker at the start of a
    // line, and emphasis delimiters. The words between stay text.
    list_marker: (_) => token(prec(2, /([-*+]|[0-9]+[.)])[ \t]/)),

    emphasis_mark: (_) => token(prec(2, /\*\*|\*|~~/)),

    prose_block: ($) => seq('{{', repeat(choice($._inline, alias('}', $.text))), '}}'),

    _inline: ($) =>
      choice(
        $.text,
        $.escape,
        $.backtick,
        $.link,
        $.short_link,
        $.insert,
        $.tag,
        $.closing_tag,
        $.emphasis_mark,
        alias(choice('[', ']', '$', '<', '`', '\\', '~'), $.text),
      ),

    text: (_) => token(prec(1, /[^\\`\[\]$<}*~\r\n]+/)),

    escape: (_) => token(prec(2, /\\[$<\[|\]`\\*_#]|\\\}\}/)),

    // `[name]` or ``[`name`]``: a name or a dotted path of names alone, or
    // in single backticks. Each is one token, brackets included, so
    // bracketed text that isn't a name stays text; a link may start the
    // same way, `[name](`, so `[name](#path)` stays a link.
    short_link: ($) => choice($._short_name, $._short_code),

    _short_name: (_) => token(prec(3, /\[[A-Za-z_][A-Za-z0-9_]*(\.([A-Za-z_][A-Za-z0-9_]*|[0-9]+[a-z]?))*\]/)),

    _short_code: (_) => token(prec(3, /\[`[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*`\]/)),

    link: ($) =>
      seq(
        choice(
          seq('[', repeat(choice($.text, $.escape, $.backtick, $.insert, alias(choice('$', '<', '`', '\\'), $.text))), ']('),
          seq(alias(choice($._short_name, $._short_code), $.text), token.immediate(prec(5, '('))),
        ),
        choice(seq('#', optional($.path)), $.url),
        ')',
      ),

    url: (_) => /[^)\s#][^)\s]*/,

    insert: ($) =>
      choice(
        seq(
          '$',
          alias(token.immediate(prec(3, IDENT)), $.identifier),
          repeat(alias(token.immediate(prec(3, /\.[A-Za-z_][A-Za-z0-9_]*/)), $.member)),
          optional(seq(alias(token.immediate('('), '('), commaSep(choice($.named_argument, $._expression)), ')')),
        ),
        seq('${', $._expression, '}'),
      ),

    tag: ($) =>
      seq(
        '<',
        alias(token.immediate(prec(3, IDENT)), $.identifier),
        repeat(seq('.', $._name)),
        repeat($.attribute),
        choice('>', '/>'),
      ),

    closing_tag: ($) => seq('</', $.path, '>'),

    attribute: ($) =>
      seq(optional('@'), field('name', $._name), optional(seq('=', choice($.string, seq('{', $._expression, '}'))))),
  },
});
