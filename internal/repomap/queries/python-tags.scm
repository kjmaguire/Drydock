; Copied unchanged from github.com/tree-sitter/tree-sitter-python v0.25.0, queries/tags.scm.
; MIT License, Copyright (c) 2016 Max Brunsfeld. See THIRD_PARTY_NOTICES.md.

(module (expression_statement (assignment left: (identifier) @name) @definition.constant))

(class_definition
  name: (identifier) @name) @definition.class

(function_definition
  name: (identifier) @name) @definition.function

(call
  function: [
      (identifier) @name
      (attribute
        attribute: (identifier) @name)
  ]) @reference.call
