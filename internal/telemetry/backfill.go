package telemetry

// "Also send what was recorded before" (decision P121): when a person turns OTLP sending on they may choose to send the spans and
// metrics the sink recorded earlier as well. It is off unless chosen, and bounded twice: a date (no earlier than the sink's
// retention window keeps rows) and a number of rows (the newest ones, at most MaxBackfillRows). The rows go through the same
// queue, batching, retries and encoders as anything recorded now, so the allowlist is applied again. A row a collector already accepted (otlp_sent_at) is never sent twice.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// maxIDs is the newest row of each table: the rows "recorded before" sending was turned on are those up to here.
func (s *Sink) maxIDs(ctx context.Context) (watermark, error) {
	var w watermark
	r := s.db.Reader()
	if err := r.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM telemetry_spans`).Scan(&w.spans); err != nil {
		return w, err
	}
	err := r.QueryRowContext(ctx, `SELECT COALESCE(MAX(id),0) FROM telemetry_metrics`).Scan(&w.metrics)
	return w, err
}

// checkBackfill bounds a person's choice: a date within the retention window and not in the future, and a row cap.
func (x *Exporter) checkBackfill(b Backfill) (Backfill, error) {
	if !x.s.Enabled() {
		return b, errors.New("telemetry is off, so nothing is sent: turn telemetry on first")
	}
	now := x.s.opt.Now()
	oldest := now.Add(-x.s.opt.Retention).UnixMilli()
	switch {
	case b.SinceMS <= 0:
		return b, errors.New("give the date to send from (what was recorded since then)")
	case b.SinceMS > now.UnixMilli():
		return b, errors.New("the date to send from is in the future")
	case b.SinceMS < oldest:
		return b, fmt.Errorf("telemetry keeps %d days, so the date to send from must be within the last %d days", int(x.s.opt.Retention.Hours()/24), int(x.s.opt.Retention.Hours()/24))
	}
	if b.MaxRows == 0 {
		b.MaxRows = DefaultBackfillRows
	}
	if b.MaxRows < 1 || b.MaxRows > MaxBackfillRows {
		return b, fmt.Errorf("send at most %d rows recorded before (between 1 and %d)", MaxBackfillRows, MaxBackfillRows)
	}
	return b, nil
}

type backfillRow struct {
	at int64
	q  queued
}

// queueBackfill queues the newest b.MaxRows rows recorded since b.SinceMS, up to through, that were never sent, oldest first, and
// records what it did.
func (x *Exporter) queueBackfill(ctx context.Context, b Backfill, through watermark, by string) error {
	r := x.s.db.Reader()
	var total int
	var n1, n2 int
	if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_spans WHERE otlp_sent_at IS NULL AND id<=? AND end_ms>=?`, through.spans, b.SinceMS).Scan(&n1); err != nil {
		return err
	}
	if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM telemetry_metrics WHERE otlp_sent_at IS NULL AND id<=? AND ts>=?`, through.metrics, b.SinceMS).Scan(&n2); err != nil {
		return err
	}
	total = n1 + n2
	var rows []backfillRow
	sr, err := r.QueryContext(ctx, `SELECT id,trace_id,span_id,parent_id,name,start_ms,end_ms,status,attrs FROM telemetry_spans
		WHERE otlp_sent_at IS NULL AND id<=? AND end_ms>=? ORDER BY end_ms DESC, id DESC LIMIT ?`, through.spans, b.SinceMS, b.MaxRows)
	if err != nil {
		return err
	}
	for sr.Next() {
		var id int64
		var sp Span
		var attrs string
		if err := sr.Scan(&id, &sp.TraceID, &sp.SpanID, &sp.ParentID, &sp.Name, &sp.StartMS, &sp.EndMS, &sp.Status, &attrs); err != nil {
			sr.Close()
			return err
		}
		_ = json.Unmarshal([]byte(attrs), &sp.Attrs) // the row was sanitized when stored; the encoder applies the allowlist again
		rows = append(rows, backfillRow{at: sp.EndMS, q: queued{id: id, span: &sp}})
	}
	sr.Close()
	if err := sr.Err(); err != nil {
		return err
	}
	mr, err := r.QueryContext(ctx, `SELECT id,ts,name,kind,unit,value,attrs FROM telemetry_metrics
		WHERE otlp_sent_at IS NULL AND id<=? AND ts>=? ORDER BY ts DESC, id DESC LIMIT ?`, through.metrics, b.SinceMS, b.MaxRows)
	if err != nil {
		return err
	}
	for mr.Next() {
		var id int64
		var m Metric
		var attrs string
		if err := mr.Scan(&id, &m.TimeMS, &m.Name, &m.Kind, &m.Unit, &m.Value, &attrs); err != nil {
			mr.Close()
			return err
		}
		_ = json.Unmarshal([]byte(attrs), &m.Attrs)
		rows = append(rows, backfillRow{at: m.TimeMS, q: queued{id: id, metric: &m}})
	}
	mr.Close()
	if err := mr.Err(); err != nil {
		return err
	}
	// the newest MaxRows of both tables together, then oldest first, as they were recorded
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at > rows[j].at })
	if len(rows) > b.MaxRows {
		rows = rows[:b.MaxRows]
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at < rows[j].at })
	queuedN := 0
	for _, row := range rows {
		if x.offer(row.q) {
			queuedN++
		}
	}
	res := BackfillResult{At: x.s.opt.Now().UnixMilli(), SinceMS: b.SinceMS, MaxRows: b.MaxRows, Queued: queuedN, Left: total - queuedN, By: by}
	raw, _ := json.Marshal(res)
	x.mu.Lock()
	x.backfill = &res
	x.mu.Unlock()
	return x.s.db.WriteTx(ctx, func(tx *sql.Tx) error { return x.s.put(ctx, tx, keyOTLPBackfill, string(raw)) })
}
