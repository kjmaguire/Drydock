; Authored for Drydock (not upstream). Rust references the grammar's own tags.scm leaves out: path calls (module::f(),
; Type::new()), names brought in with use, and type references.

(call_expression
  function: (scoped_identifier
    name: (identifier) @name)) @reference.call

(call_expression
  function: (scoped_identifier
    path: (identifier) @name)) @reference.class

(use_declaration
  argument: (scoped_identifier
    name: (identifier) @name)) @reference.import

(type_identifier) @name @reference.type
