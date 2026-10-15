; Authored for Drydock (not upstream). C# definitions and references the grammar's own tags.scm leaves out: structs,
; records and enums, plain calls, static member access (Type.Member) and the types of parameters and properties.

(struct_declaration
  name: (identifier) @name) @definition.class

(record_declaration
  name: (identifier) @name) @definition.class

(enum_declaration
  name: (identifier) @name) @definition.enum

(invocation_expression
  function: (identifier) @name) @reference.call

(member_access_expression
  expression: (identifier) @name) @reference.class

(parameter
  type: (identifier) @name) @reference.type

(property_declaration
  type: (identifier) @name) @reference.type

(object_creation_expression
  type: (generic_name
    (identifier) @name)) @reference.class
