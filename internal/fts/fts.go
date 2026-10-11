// Package fts builds safe SQLite FTS5 queries from free text, for every full-text search in Drydock (expert packs, memory).
package fts

import (
	"regexp"
	"strings"
)

// BuildMatch turns free text into FTS5 MATCH expressions that cannot inject FTS5 syntax (docs/expert-packs.md section 5; also
// memory search):
// the text is split on non-word characters, every term is quoted, identifiers that contain '.', '_' or '-' such as
// wrangler.toml become phrase queries, the last term gets a prefix star, and terms are joined with AND (all) and OR (any).
// Common English stop words are dropped unless nothing else remains, so "how do I deploy a worker" does not match everything.
// Terms lists the normalized terms (phrases joined by a space), used for confidence and highlighting.
func BuildMatch(q string) (all, any string, terms []string) {
	toks := wordRe.FindAllString(strings.ToLower(q), -1)
	var keep, stopped []string
	seen := map[string]bool{}
	for _, t := range toks {
		t = strings.Trim(t, "._-")
		phrase := sepRe.ReplaceAllString(t, " ")
		phrase = strings.TrimSpace(phrase)
		if phrase == "" || seen[phrase] {
			continue
		}
		seen[phrase] = true
		if len([]rune(phrase)) < 2 && !isDigits(phrase) {
			continue
		}
		if stopWords[phrase] {
			stopped = append(stopped, phrase)
			continue
		}
		keep = append(keep, phrase)
	}
	if len(keep) == 0 {
		keep = stopped
	}
	if len(keep) > 12 {
		keep = keep[:12]
	}
	if len(keep) == 0 {
		return "", "", nil
	}
	quoted := make([]string, len(keep))
	for i, k := range keep {
		quoted[i] = `"` + strings.ReplaceAll(k, `"`, "") + `"`
	}
	quoted[len(quoted)-1] += "*"
	return strings.Join(quoted, " AND "), strings.Join(quoted, " OR "), keep
}

var (
	wordRe = regexp.MustCompile(`[\p{L}\p{N}]+(?:[._-][\p{L}\p{N}]+)*`)
	sepRe  = regexp.MustCompile(`[._-]+`)
)

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

var stopWords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`a an and are as at be but by can could do does did for from has have how i if in into is it its
		me my of on or our should so than that the their then there these they this to was we were what when where which who why will
		with would you your about use using used get`) {
		m[w] = true
	}
	return m
}()
