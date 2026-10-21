; Authored for Drydock (not upstream). Java definitions and references the grammar's own tags.scm leaves out: enums and
; records, the declared types of fields, parameters and locals, and static calls (Type.method()).

(enum_declaration
  name: (identifier) @name) @definition.enum

(record_declaration
  name: (identifier) @name) @definition.class

(field_declaration
  type: (type_identifier) @name) @reference.type

(formal_parameter
  type: (type_identifier) @name) @reference.type

(local_variable_declaration
  type: (type_identifier) @name) @reference.type

(method_invocation
  object: (identifier) @name) @reference.class
