//go:build !cgo

package repomap

import "context"

// A build without CGO has no tree-sitter parser (decision D5, P62): Available reports false and Map returns ErrNoParser.

const parserBuilt = false

// Languages lists the languages this build parses: none.
func Languages() []string { return nil }

func parserVersion() string { return "" }

func extractTags(context.Context, string, []byte) ([]Tag, error) { return nil, ErrNoParser }
