package projectio

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// Unclassified compares a migrated database with the allow-list and returns one line per problem: a table that is neither
// exported nor excluded, a column of an exported table that is neither exported nor skipped (with a reason), or an entry in
// the lists that the schema does not have. An empty result means every table and column was decided explicitly. The test
// suite runs it against the real migrations, which is what makes a future migration fail the build until someone decides.
func Unclassified(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, COALESCE(sql,'') FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	live := map[string]bool{}
	virtual := map[string]bool{}
	for rows.Next() {
		var n, s string
		if err := rows.Scan(&n, &s); err != nil {
			rows.Close()
			return nil, err
		}
		live[n] = true
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(s)), "CREATE VIRTUAL TABLE") {
			virtual[n] = true
		}
	}
	rows.Close()
	shadow := func(n string) bool {
		for v := range virtual {
			for _, sfx := range []string{"_data", "_idx", "_content", "_docsize", "_config"} {
				if n == v+sfx {
					return true
				}
			}
		}
		return false
	}
	var problems []string
	exported := map[string]Table{}
	for _, t := range Tables {
		if _, dup := exported[t.Name]; dup {
			problems = append(problems, fmt.Sprintf("table %s is listed twice in Tables", t.Name))
		}
		exported[t.Name] = t
		if _, both := ExcludedTables[t.Name]; both {
			problems = append(problems, fmt.Sprintf("table %s is both exported and excluded", t.Name))
		}
	}
	var names []string
	for n := range live {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if n == "goose_db_version" || shadow(n) {
			continue
		}
		_, e := exported[n]
		_, x := ExcludedTables[n]
		if !e && !x {
			problems = append(problems, fmt.Sprintf("table %q is neither in projectio.Tables nor in projectio.ExcludedTables: decide whether a project export carries it (add it with an explicit column list) or exclude it with a reason", n))
		}
	}
	for n, why := range ExcludedTables {
		if !live[n] {
			problems = append(problems, fmt.Sprintf("ExcludedTables lists %q, which no migration creates", n))
		}
		if why == "" {
			problems = append(problems, fmt.Sprintf("ExcludedTables entry %q has no reason", n))
		}
	}
	for _, t := range Tables {
		if !live[t.Name] {
			problems = append(problems, fmt.Sprintf("Tables lists %q, which no migration creates", t.Name))
			continue
		}
		cols := map[string]bool{}
		cr, err := db.QueryContext(ctx, fmt.Sprintf(`SELECT name FROM pragma_table_info('%s')`, t.Name))
		if err != nil {
			return nil, err
		}
		for cr.Next() {
			var c string
			if err := cr.Scan(&c); err != nil {
				cr.Close()
				return nil, err
			}
			cols[c] = true
		}
		cr.Close()
		covered := map[string]bool{}
		for _, c := range t.Columns {
			if !cols[c] {
				problems = append(problems, fmt.Sprintf("%s: exported column %q does not exist", t.Name, c))
			}
			covered[c] = true
		}
		for c, why := range t.Skipped {
			if !cols[c] {
				problems = append(problems, fmt.Sprintf("%s: skipped column %q does not exist", t.Name, c))
			}
			if covered[c] {
				problems = append(problems, fmt.Sprintf("%s: column %q is both exported and skipped", t.Name, c))
			}
			if why == "" {
				problems = append(problems, fmt.Sprintf("%s: skipped column %q has no reason", t.Name, c))
			}
			covered[c] = true
		}
		var missing []string
		for c := range cols {
			if !covered[c] {
				missing = append(missing, c)
			}
		}
		sort.Strings(missing)
		for _, c := range missing {
			problems = append(problems, fmt.Sprintf("%s: column %q is neither exported nor skipped: a migration added it; decide explicitly (Columns, or Skipped with a reason)", t.Name, c))
		}
	}
	return problems, nil
}
