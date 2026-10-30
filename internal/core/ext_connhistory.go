package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/kjmaguire/drydock/internal/agents"
	"github.com/kjmaguire/drydock/internal/connections"
)

// The conformance history of an agent CLI's install and its capability matrix (decision P132). Every Test connection run is
// kept (connection_tests, the newest testRunsKept of each install); the Agents page lists them under Test results with the
// matrix: what the adapter declares, and over it what the newest run saw. Nothing of the CLI's output is kept: the steps are
// counted, and the first failed step's reason goes through the secret scanner first.

// testRunsKept is how many Test connection runs of one install are kept.
const testRunsKept = 30

func init() {
	RegisterExtension(Extension{Name: "connhistory", Start: func(ctx context.Context, c *Core) (map[string]Method, func(), error) {
		return map[string]Method{
			// connection.history is an install's Test connection runs, newest first, and its capability matrix
			"connection.history": bind(func(ctx context.Context, p struct {
				Adapter string `json:"adapter"`
				Distro  string `json:"distro"` // the install inside this WSL distro instead of the native one
			}) (any, error) {
				return c.ConnectionHistory(ctx, p.Adapter, p.Distro)
			}),
		}, nil, nil
	}})
}

// TestRun is one kept Test connection run.
type TestRun struct {
	ID         int64  `json:"id"`
	CLIVersion string `json:"cliVersion,omitempty"`
	Transport  string `json:"transport,omitempty"`
	OK         bool   `json:"ok"`
	// Prompt says the run also sent the tiny prompt (the only step that spends tokens).
	Prompt    bool   `json:"prompt"`
	Passed    int    `json:"passed"`
	Failed    int    `json:"failed"`
	Skipped   int    `json:"skipped"`
	Reason    string `json:"reason,omitempty"`
	ElapsedMs int64  `json:"elapsedMs"`
	At        int64  `json:"at"` // unix milliseconds
}

// ConnectionHistory is what connection.history answers.
type ConnectionHistory struct {
	Key  string    `json:"key"`
	Runs []TestRun `json:"runs"`
	// Matrix is the install's capability matrix; MatrixAt the time of the run whose cells it shows (0: never tested, the
	// adapter's declarations only).
	Matrix   map[string]connections.Cell `json:"matrix"`
	MatrixAt int64                       `json:"matrixAt,omitempty"`
}

// keepTestRun stores a Test connection run and drops the install's runs beyond the newest testRunsKept.
func (c *Core) keepTestRun(ctx context.Context, key string, r connections.Report, prompt bool) error {
	passed, failed, skipped := r.Counts()
	matrix, err := json.Marshal(r.Matrix)
	if err != nil || r.Matrix == nil {
		matrix = []byte("{}")
	}
	reason := r.FailureReason()
	if c.Scanner != nil {
		reason = c.Scanner.Redact(reason)
	}
	return c.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO connection_tests(conn_key, adapter, cli_version, transport, ok, prompt, passed, failed, skipped, reason, matrix, elapsed_ms, tested_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, key, r.Adapter, r.CLIVersion, r.Transport, r.OK, prompt, passed, failed, skipped, reason, string(matrix),
			r.Elapsed.Milliseconds(), time.Now().UnixMilli()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM connection_tests WHERE conn_key=? AND id NOT IN
			(SELECT id FROM connection_tests WHERE conn_key=? ORDER BY id DESC LIMIT ?)`, key, key, testRunsKept)
		return err
	})
}

// ConnectionHistory is an install's kept Test connection runs, newest first, and its capability matrix.
func (c *Core) ConnectionHistory(ctx context.Context, adapterID, distro string) (ConnectionHistory, error) {
	m, ok := c.Connections.Registry.Set.Get(adapterID)
	if !ok {
		return ConnectionHistory{}, fmt.Errorf("no agent CLI %q", adapterID)
	}
	key := adapterID
	if distro != "" {
		key = connections.WSLKey(adapterID, distro)
	}
	h := ConnectionHistory{Key: key, Runs: []TestRun{}}
	rows, err := c.DB.Reader().QueryContext(ctx, `SELECT id, cli_version, transport, ok, prompt, passed, failed, skipped, reason, matrix, elapsed_ms, tested_at
		FROM connection_tests WHERE conn_key=? ORDER BY id DESC LIMIT ?`, key, testRunsKept)
	if err != nil {
		return h, err
	}
	defer rows.Close()
	var last map[string]connections.Cell
	for rows.Next() {
		var r TestRun
		var matrix string
		if err := rows.Scan(&r.ID, &r.CLIVersion, &r.Transport, &r.OK, &r.Prompt, &r.Passed, &r.Failed, &r.Skipped, &r.Reason, &matrix, &r.ElapsedMs, &r.At); err != nil {
			return h, err
		}
		if h.MatrixAt == 0 {
			// the newest run's cells; a run that stopped before the matrix was filled has none, and an older one's stand
			var cells map[string]connections.Cell
			if json.Unmarshal([]byte(matrix), &cells) == nil && len(cells) > 0 {
				last, h.MatrixAt = cells, r.At
			}
		}
		h.Runs = append(h.Runs, r)
	}
	if err := rows.Err(); err != nil {
		return h, err
	}
	c.mu.Lock()
	ad := c.adapters[adapterID]
	c.mu.Unlock()
	var caps agents.Capabilities
	if ad != nil {
		caps = ad.Capabilities()
	}
	h.Matrix = connections.CardMatrix(m, caps, last)
	return h, nil
}
