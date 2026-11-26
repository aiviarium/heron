; Highlighting from the static grammar. `heron lsp` refines it with
; semantic tokens from the resolved tree.

(comment) @comment
(doc) @comment.documentation

(string) @string
(backtick) @string.special
(number) @number
(boolean) @boolean
(escape) @string.escape
(url) @string.special.url

[
  "let"
  "do"
  "if"
  "else"
  "for"
  "in"
  "return"
  "not"
  "and"
  "or"
  "is"
] @keyword

(declaration name: (path (identifier) @property .))
(value name: (path (identifier) @property .))
(parameter name: (identifier) @variable.parameter)
(named_argument name: (identifier) @variable.parameter)
(let name: (identifier) @variable)
(let_statement name: (identifier) @variable)
(attribute name: (identifier) @attribute)
(map_entry (path (identifier) @property))

(named_type (path (identifier) @type .))
(annotation "@" @attribute (path (identifier) @attribute))
(flags (identifier) @attribute.builtin)

(call callee: (path (identifier) @function.call .))
(invoke name: (path (identifier) @function.call .) (arguments))

(insert (identifier) @variable)
(insert (member) @variable.member)
(link (path) @markup.link)
(link (url) @markup.link.url)
(short_link) @markup.link
(tag (identifier) @tag)
(closing_tag (path (identifier) @tag))

(prose_line "|" @punctuation.special)
["{{" "}}"] @punctuation.special
["[" "](" ")" "#"] @punctuation.delimiter
["{" "}" "(" "[" "]"] @punctuation.bracket
["," "." ":"] @punctuation.delimiter
["=" "==" "!=" "<=" ">=" "+" "-" "*" "/" "%" "&" "?"] @operator
