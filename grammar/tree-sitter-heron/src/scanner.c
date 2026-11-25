// The external scanner of the Heron grammar: the tokens a regular lexer
// can't find on its own. It keeps no state between tokens.
#include "tree_sitter/parser.h"

#include <stdbool.h>
#include <stdint.h>

enum TokenType { LINE_END, STRING, BACKTICK, PROSE_BAR, UNION_BAR, ERROR_SENTINEL };

void *tree_sitter_heron_external_scanner_create(void) { return NULL; }
void tree_sitter_heron_external_scanner_destroy(void *p) { (void)p; }
unsigned tree_sitter_heron_external_scanner_serialize(void *p, char *b) {
  (void)p;
  (void)b;
  return 0;
}
void tree_sitter_heron_external_scanner_deserialize(void *p, const char *b, unsigned n) {
  (void)p;
  (void)b;
  (void)n;
}

static void advance(TSLexer *lx) { lx->advance(lx, false); }
static void skip(TSLexer *lx) { lx->advance(lx, true); }

static unsigned run(TSLexer *lx, int32_t c) {
  unsigned n = 0;
  while (lx->lookahead == c) {
    advance(lx);
    n++;
  }
  return n;
}

static bool scan_string_body(TSLexer *lx, unsigned k, int depth);

// skip_interpolation consumes `${ … }` after the `${`, with nested braces
// and strings.
static bool skip_interpolation(TSLexer *lx, int depth) {
  int braces = 1;
  while (!lx->eof(lx)) {
    int32_t c = lx->lookahead;
    if (c == '"') {
      unsigned k = run(lx, '"');
      if (k == 2) continue; // ""
      if (!scan_string_body(lx, k, depth + 1)) return false;
      continue;
    }
    advance(lx);
    if (c == '{') braces++;
    if (c == '}' && --braces == 0) return true;
  }
  return false;
}

// scan_string_body consumes a string's content and its closing run, after
// an opening run of k quotes.
static bool scan_string_body(TSLexer *lx, unsigned k, int depth) {
  if (depth > 8) return false;
  while (!lx->eof(lx)) {
    int32_t c = lx->lookahead;
    if (c == '\\') {
      advance(lx);
      if (!lx->eof(lx)) advance(lx);
      continue;
    }
    if (c == '$') {
      advance(lx);
      if (lx->lookahead == '{') {
        advance(lx);
        if (!skip_interpolation(lx, depth)) return false;
      }
      continue;
    }
    if (c == '"') {
      unsigned n = run(lx, '"');
      if (k == 1 || n >= k) return true;
      continue;
    }
    if (c == '\n' && k == 1) return false;
    advance(lx);
  }
  return false;
}

// scan_backtick consumes a run of k backticks' content up to a run of the
// same length: on the same line for one or two, across lines for three or
// more, as fences and raw blocks do.
static bool scan_backtick(TSLexer *lx) {
  unsigned k = run(lx, '`');
  while (!lx->eof(lx)) {
    int32_t c = lx->lookahead;
    if (c == '`') {
      unsigned n = run(lx, '`');
      if (n == k) {
        lx->mark_end(lx);
        return true;
      }
      continue;
    }
    if (c == '\n' && k < 3) return false;
    advance(lx);
  }
  return false;
}

bool tree_sitter_heron_external_scanner_scan(void *payload, TSLexer *lx, const bool *valid) {
  (void)payload;
  if (valid[ERROR_SENTINEL]) return false;

  bool line_start = lx->get_column(lx) == 0;
  while (lx->lookahead == ' ' || lx->lookahead == '\t' || lx->lookahead == '\r' || lx->lookahead == 0xFEFF) skip(lx);

  if (valid[LINE_END] && (lx->lookahead == '\n' || lx->eof(lx))) {
    if (!lx->eof(lx)) advance(lx);
    lx->result_symbol = LINE_END;
    return true;
  }

  while (lx->lookahead == ' ' || lx->lookahead == '\t' || lx->lookahead == '\r' || lx->lookahead == '\n' ||
         lx->lookahead == '\f') {
    if (lx->lookahead == '\n') line_start = true;
    skip(lx);
  }

  switch (lx->lookahead) {
  case '|':
    if (valid[PROSE_BAR] && (line_start || !valid[UNION_BAR])) {
      advance(lx);
      lx->result_symbol = PROSE_BAR;
      return true;
    }
    if (valid[UNION_BAR]) {
      advance(lx);
      lx->result_symbol = UNION_BAR;
      return true;
    }
    return false;
  case '"':
    if (!valid[STRING]) return false;
    {
      unsigned k = run(lx, '"');
      if (k != 2 && !scan_string_body(lx, k, 0)) return false;
    }
    lx->result_symbol = STRING;
    return true;
  case '`':
    if (!valid[BACKTICK]) return false;
    if (!scan_backtick(lx)) return false;
    lx->result_symbol = BACKTICK;
    return true;
  }
  return false;
}
