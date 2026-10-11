package packs

import "github.com/kjmaguire/drydock/internal/fts"

// BuildMatch turns free text into safe FTS5 MATCH expressions (fts.BuildMatch: quoted terms, stop words dropped, a prefix star
// on the last term): all joins the terms with AND, any with OR, and terms lists them.
func BuildMatch(q string) (all, any string, terms []string) { return fts.BuildMatch(q) }
