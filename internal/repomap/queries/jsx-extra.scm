; Authored for Drydock (not upstream). JSX: a component used as an element is a reference to it. Added to the queries of
; TSX and of JavaScript (whose grammar parses JSX); decision D5 noted that no upstream tags query covers TSX.

(jsx_opening_element
  name: (identifier) @name) @reference.class

(jsx_self_closing_element
  name: (identifier) @name) @reference.class
