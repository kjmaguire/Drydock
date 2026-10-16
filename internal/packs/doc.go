// Package packs implements Expert Packs (docs/expert-packs.md): attachable domain knowledge that is cheap to retrieve,
// always cited and usable offline.
//
// A pack is either indexed (documents ingested from local directories or fetched from URLs the human configured, chunked
// along headings, stored in SQLite and searched with FTS5 BM25), live (backed by an MCP docs server reached through the
// internal/mcp gateway, which is not duplicated here) or hybrid. Two modes use one retrieval function: CONSULT (a
// stateless one-shot that returns text assembled from retrieved chunks with citations and a confidence; no model is needed,
// an optional Answerer may synthesize later) and ATTACH (a few lines of instructions plus the search_pack tool).
//
// All pack content is untrusted data. It is scanned for secrets and injection patterns before it is indexed, marked with
// the same <untrusted> wrapper the gateway uses when it is delivered, and can never change permissions or routing.
// The same chunker, scanner and query builder serve internal/requirements and internal/skills.
package packs
