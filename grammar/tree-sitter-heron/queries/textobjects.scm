; Structural selection: a member with its docs and annotations, its body,
; a call's arguments, a parameter, a block of code and a run of prose.

(declaration) @class.outer
(declaration (struct_body) @class.inner)
(enter) @class.outer
(enter (struct_body) @class.inner)
(invoke) @call.outer
(invoke (arguments) @call.inner)
(call) @call.outer
(call (arguments) @call.inner)
(value) @assignment.outer
(value value: (_) @assignment.inner)
(set) @assignment.outer
(set value: (_) @assignment.inner)
(parameter) @parameter.outer
(named_argument) @parameter.outer
(do_block) @function.outer
(code) @function.inner
(prose_lines) @comment.outer
(prose_block) @comment.outer
(doc) @comment.outer
(comment) @comment.outer
(link) @link.outer
(tag) @tag.outer
