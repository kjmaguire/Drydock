; Authored for Drydock (not upstream). TypeScript and TSX definitions and references that the grammar's own tags.scm (and
; the JavaScript tags.scm it is combined with, as the grammar's tree-sitter.json does) leave out: type aliases, enums,
; heritage clauses and generic type references. Capture names follow the tree-sitter tags convention.

(type_alias_declaration
  name: (type_identifier) @name) @definition.type

(enum_declaration
  name: (identifier) @name) @definition.enum

(extends_clause
  value: (identifier) @name) @reference.class

(implements_clause
  (type_identifier) @name) @reference.implementation

(generic_type
  name: (type_identifier) @name) @reference.type

(type_arguments
  (type_identifier) @name) @reference.type

(program
  (lexical_declaration
    (variable_declarator
      name: (identifier) @name)) @definition.constant)

(export_statement
  declaration: (lexical_declaration
    (variable_declarator
      name: (identifier) @name)) @definition.constant)
