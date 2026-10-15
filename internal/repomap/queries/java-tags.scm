; Copied unchanged from github.com/tree-sitter/tree-sitter-java v0.23.5, queries/tags.scm.
; MIT License, Copyright (c) 2017 Ayman Nadeem. See THIRD_PARTY_NOTICES.md.

(class_declaration
  name: (identifier) @name) @definition.class

(method_declaration
  name: (identifier) @name) @definition.method

(method_invocation
  name: (identifier) @name
  arguments: (argument_list) @reference.call)

(interface_declaration
  name: (identifier) @name) @definition.interface

(type_list
  (type_identifier) @name) @reference.implementation

(object_creation_expression
  type: (type_identifier) @name) @reference.class

(superclass (type_identifier) @name) @reference.class
