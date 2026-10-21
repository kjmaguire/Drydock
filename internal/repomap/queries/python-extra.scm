; Authored for Drydock (not upstream). Python references the grammar's own tags.scm leaves out: base classes and names
; imported with "from module import name".

(class_definition
  superclasses: (argument_list
    (identifier) @name)) @reference.class

(import_from_statement
  name: (dotted_name
    (identifier) @name)) @reference.import

(import_from_statement
  name: (aliased_import
    name: (dotted_name
      (identifier) @name))) @reference.import
