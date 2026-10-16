; Authored for Drydock (not upstream). JavaScript references and top-level constants that the grammar's own tags.scm leaves
; out: the class a class extends, and const/let declarations at the top of a module or exported.

(class_heritage
  (identifier) @name) @reference.class

(program
  (lexical_declaration
    (variable_declarator
      name: (identifier) @name)) @definition.constant)

(export_statement
  declaration: (lexical_declaration
    (variable_declarator
      name: (identifier) @name)) @definition.constant)
