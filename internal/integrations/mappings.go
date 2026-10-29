package integrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Mappings (decision P114, integration_mappings): what a vendor's values mean in Drydock, chosen by a person per assignment.
//
//   - status: one of the vendor's states (a Linear workflow state, a Notion status option, Sentry's resolved or unresolved) and
//     a Drydock task status. to_vendor: a linked task entering that status proposes the vendor write (status sync); from_vendor:
//     the vendor's state is offered to the person as a change to the task (never applied by itself); both.
//   - property: which Notion property of a data source's typed rows fills a Drydock field (state, priority, assignee, labels).
//
// Mappings are data a person typed or picked from the vendor's own lists; no item text can create or change one.

// Mapping scopes and directions.
const (
	MapStatus     = "status"
	MapProperty   = "property"
	DirToVendor   = "to_vendor"
	DirFromVendor = "from_vendor"
	DirBoth       = "both"
)

// EventStatusUpstream says the vendor moved an item that has a task; EventStatusApplied that the person applied the mapped
// status to the task; EventMappingsChanged that a person changed an assignment's mappings.
const (
	EventStatusUpstream  = "integration.status.upstream"
	EventStatusApplied   = "integration.status.applied"
	EventMappingsChanged = "integration.mappings.changed"
)

// TaskStatuses are the Drydock task statuses a status mapping may name (tasks.Status values; the integrations package does not
// import the task service).
var TaskStatuses = []string{"READY", "CLAIMED", "IN_PROGRESS", "BLOCKED", "WAITING_HUMAN", "REVIEW", "VERIFICATION", "COMPLETED", "FAILED", "CANCELLED"}

// propertyFields are the Drydock fields a Notion property can fill.
var propertyFields = []string{"state", "priority", "assignee", "labels"}

// sentryStates are the only states a Sentry status mapping can name (the issue's status; ignored is never written, it hides a signal).
var sentryStates = map[string]bool{"resolved": true, "unresolved": true}

// Mapping is one row of integration_mappings.
type Mapping struct {
	AssignmentID string `json:"assignmentId"`
	Scope        string `json:"scope"`
	// Remote is the vendor's value (status) or the Drydock field (property).
	Remote     string `json:"remote"`
	RemoteName string `json:"remoteName,omitempty"`
	// Local is the Drydock task status (status) or the Notion property name (property).
	Local     string `json:"local"`
	Direction string `json:"direction"`
	CreatedBy string `json:"createdBy"`
	Created   int64  `json:"created"`
}

// StatusSuggestion is a change the vendor's state offers the person for a linked task.
type StatusSuggestion struct {
	Upstream   string `json:"upstream"`   // the vendor's state
	To         string `json:"to"`         // the mapped task status
	TaskStatus string `json:"taskStatus"` // the task's status now
}

// ---- store ----

// ListMappings returns an assignment's mappings of a scope ("" = all), ordered.
func (s *Store) ListMappings(ctx context.Context, assignmentID, scope string) ([]Mapping, error) {
	q, args := `SELECT assignment_id,scope,remote_id,remote_name,local,direction,created_by,created_at FROM integration_mappings WHERE assignment_id=?`, []any{assignmentID}
	if scope != "" {
		q += ` AND scope=?`
		args = append(args, scope)
	}
	rows, err := s.DB.Reader().QueryContext(ctx, q+` ORDER BY scope, remote_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mapping{}
	for rows.Next() {
		var m Mapping
		if err := rows.Scan(&m.AssignmentID, &m.Scope, &m.Remote, &m.RemoteName, &m.Local, &m.Direction, &m.CreatedBy, &m.Created); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReplaceMappings replaces every mapping of one scope of an assignment in one transaction.
func (s *Store) ReplaceMappings(ctx context.Context, assignmentID, scope string, ms []Mapping) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM integration_mappings WHERE assignment_id=? AND scope=?`, assignmentID, scope); err != nil {
			return err
		}
		for _, m := range ms {
			if _, err := tx.ExecContext(ctx, `INSERT INTO integration_mappings(assignment_id,scope,remote_id,remote_name,local,direction,created_by,created_at) VALUES(?,?,?,?,?,?,?,?)`,
				assignmentID, scope, m.Remote, m.RemoteName, m.Local, m.Direction, m.CreatedBy, s.now()); err != nil {
				return err
			}
		}
		return nil
	})
}

// ForgetEtags clears the vendor version markers of an assignment's items and its watermark, so the next sync lists and reads
// every item again (the cursor of an interrupted sweep is kept).
func (s *Store) ForgetEtags(ctx context.Context, assignmentID string) error {
	return s.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE external_items SET etag='' WHERE assignment_id=?`, assignmentID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE integration_cursors SET watermark=0 WHERE scope=?`, assignmentID)
		return err
	})
}

// ItemsByTask returns every item linked to a task (an import links one; link_task rules may link more).
func (s *Store) ItemsByTask(ctx context.Context, taskID string) ([]Item, error) {
	rows, err := s.DB.Reader().QueryContext(ctx, `SELECT `+itemCols+` FROM external_items WHERE task_id=? ORDER BY imported_at, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		it, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ---- service ----

// MappingsReq replaces one scope of an assignment's mappings. Only a human can send it.
type MappingsReq struct {
	AssignmentID string    `json:"assignmentId"`
	Scope        string    `json:"scope"`
	Mappings     []Mapping `json:"mappings"`
}

func cleanValue(v string, max int) (string, error) {
	v = strings.TrimSpace(v)
	if cl, _ := stripUnsafe(v); cl != v || strings.ContainsAny(v, "\n\r\t") {
		return "", errf(ErrBadRequest, "a mapping contains control or invisible characters")
	}
	if len([]rune(v)) > max {
		return "", errf(ErrBadRequest, "a mapping value is longer than %d characters", max)
	}
	return v, nil
}

// SetMappings replaces the mappings of one scope of an assignment.
func (s *Service) SetMappings(ctx context.Context, by string, r MappingsReq) ([]Mapping, error) {
	if err := requireHuman(by); err != nil {
		return nil, err
	}
	a, err := s.st.GetAssignment(ctx, r.AssignmentID)
	if err != nil {
		return nil, err
	}
	conn, err := s.st.GetConnection(ctx, a.IntegrationID)
	if err != nil {
		return nil, err
	}
	if len(r.Mappings) > 50 {
		return nil, errf(ErrBadRequest, "at most 50 mappings")
	}
	out := make([]Mapping, 0, len(r.Mappings))
	seenRemote, seenLocal := map[string]bool{}, map[string]bool{}
	switch r.Scope {
	case MapStatus:
		switch conn.Kind {
		case KindLinear, KindNotion, KindSentry:
		default:
			return nil, errf(ErrBadRequest, "%s has no status to map", vendor(conn.Kind))
		}
		for _, m := range r.Mappings {
			remote, err := cleanValue(m.Remote, 80)
			if err != nil {
				return nil, err
			}
			if remote == "" {
				return nil, errf(ErrBadRequest, "a status mapping needs the %s status", vendor(conn.Kind))
			}
			if conn.Kind == KindSentry {
				remote = strings.ToLower(remote)
				if !sentryStates[remote] {
					return nil, errf(ErrBadRequest, "a Sentry status mapping names resolved or unresolved (an ignore is never written by Drydock on its own)")
				}
			}
			local := strings.ToUpper(strings.TrimSpace(m.Local))
			if !containsStr(TaskStatuses, local) {
				return nil, errf(ErrBadRequest, "%q is not a task status (use one of %s)", m.Local, strings.Join(TaskStatuses, ", "))
			}
			dir := m.Direction
			if dir == "" {
				dir = DirBoth
			}
			if dir != DirToVendor && dir != DirFromVendor && dir != DirBoth {
				return nil, errf(ErrBadRequest, "direction must be to_vendor, from_vendor or both")
			}
			key := strings.ToLower(remote)
			if seenRemote[key] {
				return nil, errf(ErrBadRequest, "the status %q is mapped twice", remote)
			}
			seenRemote[key] = true
			if dir != DirFromVendor {
				// a task entering a status must name one vendor state to write
				if seenLocal[local] {
					return nil, errf(ErrBadRequest, "the task status %s is sent to %s by two mappings; keep one", local, vendor(conn.Kind))
				}
				seenLocal[local] = true
			}
			name, _ := cleanValue(m.RemoteName, 80)
			out = append(out, Mapping{AssignmentID: a.ID, Scope: MapStatus, Remote: remote, RemoteName: name, Local: local, Direction: dir, CreatedBy: by})
		}
	case MapProperty:
		if conn.Kind != KindNotion {
			return nil, errf(ErrBadRequest, "only a Notion data source has typed properties to map")
		}
		for _, m := range r.Mappings {
			field := strings.ToLower(strings.TrimSpace(m.Remote))
			if !containsStr(propertyFields, field) {
				return nil, errf(ErrBadRequest, "%q is not a field a property can fill (use %s)", m.Remote, strings.Join(propertyFields, ", "))
			}
			prop, err := cleanValue(m.Local, 100)
			if err != nil {
				return nil, err
			}
			if prop == "" {
				return nil, errf(ErrBadRequest, "name the Notion property that fills %s", field)
			}
			if seenRemote[field] {
				return nil, errf(ErrBadRequest, "%s is filled by two properties; keep one", field)
			}
			seenRemote[field] = true
			out = append(out, Mapping{AssignmentID: a.ID, Scope: MapProperty, Remote: field, Local: prop, Direction: DirFromVendor, CreatedBy: by})
		}
	default:
		return nil, errf(ErrBadRequest, "scope must be status or property")
	}
	if err := s.st.ReplaceMappings(ctx, a.ID, r.Scope, out); err != nil {
		return nil, err
	}
	if r.Scope == MapProperty {
		// a row is rebuilt from its properties only when it is read again: forget the version markers, so the next sync reads
		// every row of the assignment with the new mapping instead of skipping the unchanged ones
		_ = s.st.ForgetEtags(ctx, a.ID)
	}
	s.emit(ctx, EventMappingsChanged, a.ProjectID, "", map[string]any{"integrationId": conn.ID, "assignmentId": a.ID, "scope": r.Scope, "count": len(out), "by": by})
	return s.st.ListMappings(ctx, a.ID, r.Scope)
}

// Mappings lists an assignment's mappings.
func (s *Service) Mappings(ctx context.Context, assignmentID string) ([]Mapping, error) {
	if _, err := s.st.GetAssignment(ctx, assignmentID); err != nil {
		return nil, err
	}
	return s.st.ListMappings(ctx, assignmentID, "")
}

// MappingField is a field the mapping editor can offer: a vendor state, or a Notion property with its type and options.
type MappingField struct {
	Name    string   `json:"name"`
	Type    string   `json:"type,omitempty"`
	Options []string `json:"options,omitempty"`
	// Property is the Notion property a status option belongs to.
	Property string `json:"property,omitempty"`
}

// mappingSource is an adapter that can list the values a mapping may name (Linear's team states, a Notion data source's
// properties).
type mappingSource interface {
	MappingFields(ctx context.Context, a *API, cfg map[string]string, scope string) ([]MappingField, error)
}

// MappingFields reads from the vendor what the mapping editor can offer for an assignment (read-only).
func (s *Service) MappingFields(ctx context.Context, assignmentID, scope string) ([]MappingField, error) {
	a, err := s.st.GetAssignment(ctx, assignmentID)
	if err != nil {
		return nil, err
	}
	conn, err := s.st.GetConnection(ctx, a.IntegrationID)
	if err != nil {
		return nil, err
	}
	if conn.Kind == KindSentry && scope == MapStatus {
		return []MappingField{{Name: "resolved"}, {Name: "unresolved"}}, nil
	}
	ad, _ := AdapterFor(conn.Kind)
	src, ok := ad.(mappingSource)
	if !ok {
		return []MappingField{}, nil
	}
	api, fail := s.api(ctx, conn)
	if fail != nil {
		return nil, s.scrubFailure(fail)
	}
	cctx, cancel := context.WithTimeout(ctx, s.o.CallTimeout)
	defer cancel()
	out, err := src.MappingFields(cctx, api, a.Config, scope)
	if err != nil {
		var f *Failure
		if !errors.As(err, &f) {
			f = Classify(conn.Kind, err, "")
		}
		return nil, s.scrubFailure(f)
	}
	for i := range out {
		out[i].Name = s.san.Line(ctx, out[i].Name, 100).Text
		out[i].Property = s.san.Line(ctx, out[i].Property, 100).Text
		for j := range out[i].Options {
			out[i].Options[j] = s.san.Line(ctx, out[i].Options[j], 100).Text
		}
	}
	return out, nil
}

func (s *Service) scrubFailure(f *Failure) error {
	return &Failure{Code: f.Code, Message: s.scrub(f.Message), RetryAfter: f.RetryAfter, Cause: f.Cause}
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// applyPropMapping fills a typed row's state, priority, assignee and labels from its properties: the person's property
// mappings, else the row's first status property (state) and first people property (assignee). It runs before the item is
// cleaned, so every value still passes the sanitizer.
func applyPropMapping(raw *RawItem, pm []Mapping) {
	if len(raw.Props) == 0 {
		return
	}
	if raw.Meta == nil {
		raw.Meta = map[string]any{}
	}
	use := map[string]string{}
	for _, m := range pm {
		use[m.Remote] = m.Local
	}
	firstOf := func(typ string) string {
		names := make([]string, 0, len(raw.Props))
		for n, p := range raw.Props {
			if p.Type == typ {
				names = append(names, n)
			}
		}
		sort.Strings(names)
		if len(names) == 0 {
			return ""
		}
		return names[0]
	}
	if _, ok := use["state"]; !ok {
		use["state"] = firstOf("status")
	}
	if _, ok := use["assignee"]; !ok {
		use["assignee"] = firstOf("people")
	}
	if p, ok := raw.Props[use["state"]]; ok && p.Text != "" {
		raw.State = p.Text
	}
	if p, ok := raw.Props[use["assignee"]]; ok {
		raw.Assignee = p.Text
	}
	if p, ok := raw.Props[use["priority"]]; ok {
		raw.Priority = p.Text
	}
	if p, ok := raw.Props[use["labels"]]; ok && p.Text != "" {
		raw.Labels = nil
		for _, l := range strings.Split(p.Text, ",") {
			if l = strings.TrimSpace(l); l != "" {
				raw.Labels = append(raw.Labels, l)
			}
		}
	}
	if prop := use["state"]; prop != "" {
		if p, ok := raw.Props[prop]; ok {
			raw.Meta["statusProperty"] = prop
			raw.Meta["statusType"] = p.Type
		}
	}
}

// ---- status sync: Drydock to the vendor ----

const statusSyncSeqKey = "integrations.statussync.seq"

func (s *Service) setting(ctx context.Context, key string) (string, bool) {
	var v string
	if err := s.o.DB.Reader().QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return "", false
	}
	return v, true
}

func (s *Service) putSetting(ctx context.Context, key, value string) error {
	return s.o.DB.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
		return err
	})
}

// statusOp is the outbound operation and parameters that move an item to a vendor state.
func statusOp(kind, remote string, it Item) (op string, params map[string]string, ok bool) {
	switch kind {
	case KindLinear:
		return "set_state", map[string]string{"state": remote}, true
	case KindNotion:
		prop, _ := it.Meta["statusProperty"].(string)
		typ, _ := it.Meta["statusType"].(string)
		if prop == "" {
			return "", nil, false // not a typed row with a status property
		}
		return "set_status", map[string]string{"state": remote, "property": prop, "type": typ}, true
	case KindSentry:
		switch strings.ToLower(remote) {
		case "resolved":
			return "resolve", map[string]string{}, true
		case "unresolved":
			return "unresolve", map[string]string{}, true
		}
	}
	return "", nil, false
}

// StatusSync reads the task status changes recorded since the last call and, for every item linked to such a task whose
// assignment maps that status to a vendor state (to_vendor or both), proposes the vendor write through the ordinary outbox:
// it waits for a person's approval unless the person turned on the status_sync auto-approve class for that connection and
// project. An item already in the mapped state gets nothing. It returns how many writes it proposed. The first call only
// remembers where the event log is: changes from before status sync existed are not replayed.
func (s *Service) StatusSync(ctx context.Context) int {
	lk := s.lock("statussync")
	if !lk.TryLock() {
		return 0
	}
	defer lk.Unlock()
	last := int64(-1)
	if v, ok := s.setting(ctx, statusSyncSeqKey); ok {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			last = n
		}
	}
	if last < 0 {
		var max sql.NullInt64
		_ = s.o.DB.Reader().QueryRowContext(ctx, `SELECT MAX(seq) FROM events`).Scan(&max)
		_ = s.putSetting(ctx, statusSyncSeqKey, strconv.FormatInt(max.Int64, 10))
		return 0
	}
	rows, err := s.o.DB.Reader().QueryContext(ctx, `SELECT seq, task_id, data FROM events WHERE seq>? AND task_id<>'' AND type LIKE 'task.%' ORDER BY seq LIMIT 200`, last)
	if err != nil {
		return 0
	}
	type change struct {
		seq      int64
		task, to string
	}
	var changes []change
	for rows.Next() {
		var c change
		var data []byte
		if rows.Scan(&c.seq, &c.task, &data) != nil {
			continue
		}
		var d struct {
			To string `json:"to"`
		}
		_ = json.Unmarshal(data, &d)
		c.to = d.To
		changes = append(changes, c)
	}
	rows.Close()
	n := 0
	for _, c := range changes {
		if ctx.Err() != nil {
			break
		}
		if c.to != "" {
			n += s.syncTaskStatus(ctx, c.seq, c.task, c.to)
		}
		_ = s.putSetting(ctx, statusSyncSeqKey, strconv.FormatInt(c.seq, 10))
	}
	return n
}

func (s *Service) syncTaskStatus(ctx context.Context, seq int64, taskID, to string) int {
	items, err := s.st.ItemsByTask(ctx, taskID)
	if err != nil {
		return 0
	}
	n := 0
	for _, it := range items {
		if it.AssignmentID == "" {
			continue
		}
		asg, err := s.st.GetAssignment(ctx, it.AssignmentID)
		if err != nil || !asg.Enabled {
			continue
		}
		conn, err := s.st.GetConnection(ctx, it.IntegrationID)
		if err != nil || !conn.Enabled {
			continue
		}
		ms, err := s.st.ListMappings(ctx, asg.ID, MapStatus)
		if err != nil {
			continue
		}
		for _, m := range ms {
			if m.Direction == DirFromVendor || m.Local != to {
				continue
			}
			if strings.EqualFold(it.State, m.Remote) {
				continue // already there (the vendor's change may be what moved the task)
			}
			op, params, ok := statusOp(conn.Kind, m.Remote, it)
			if !ok {
				continue
			}
			_, perr := s.propose(ctx, "integration:status-sync", ProposeReq{IntegrationID: conn.ID, Op: op, Target: it.ExternalID, TaskID: taskID, ProjectID: asg.ProjectID, Params: params},
				proposeOpts{class: ClassStatusSync, nonce: fmt.Sprintf("seq:%d", seq)})
			if perr != nil {
				s.o.Logf("integrations: status sync of task %s to %s: %v", taskID, vendor(conn.Kind), s.scrub(perr.Error()))
				continue
			}
			n++
		}
	}
	return n
}

// ---- status sync: the vendor to Drydock (offered, never applied by itself) ----

// suggestStatus fills the status suggestion of linked items: the vendor's state maps (from_vendor or both) to a task status the
// task is not in. A finished task (completed, failed, cancelled) gets none.
func (s *Service) suggestStatus(ctx context.Context, its []Item) {
	if s.o.TaskStatus == nil {
		return
	}
	cache := map[string][]Mapping{}
	for i := range its {
		it := &its[i]
		if it.TaskID == "" || it.AssignmentID == "" || it.State == "" {
			continue
		}
		ms, ok := cache[it.AssignmentID]
		if !ok {
			ms, _ = s.st.ListMappings(ctx, it.AssignmentID, MapStatus)
			cache[it.AssignmentID] = ms
		}
		for _, m := range ms {
			if m.Direction == DirToVendor || !strings.EqualFold(m.Remote, it.State) {
				continue
			}
			cur, err := s.o.TaskStatus(ctx, it.TaskID)
			if err != nil || cur == m.Local || cur == "COMPLETED" || cur == "FAILED" || cur == "CANCELLED" {
				break
			}
			it.StatusSuggestion = &StatusSuggestion{Upstream: it.State, To: m.Local, TaskStatus: cur}
			break
		}
	}
}

// ApplyStatus moves an item's task to the status its vendor state maps to. Only a human can do it: imported work waits for a
// person (decision P37), and the task's own state machine still decides whether the move is allowed.
func (s *Service) ApplyStatus(ctx context.Context, by, itemID string) (Item, error) {
	if err := requireHuman(by); err != nil {
		return Item{}, err
	}
	it, err := s.st.GetItem(ctx, itemID)
	if err != nil {
		return Item{}, err
	}
	its := []Item{it}
	s.suggestStatus(ctx, its)
	sg := its[0].StatusSuggestion
	if sg == nil {
		return its[0], errf(ErrBadRequest, "there is no status change to apply for this item")
	}
	if s.o.MoveTask == nil {
		return its[0], errors.New("integrations: no task mover is configured")
	}
	if err := s.o.MoveTask(ctx, it.TaskID, sg.To, by); err != nil {
		return its[0], err
	}
	s.emit(ctx, EventStatusApplied, it.ProjectID, it.TaskID, map[string]any{"integrationId": it.IntegrationID, "itemId": it.ID, "to": sg.To, "by": by})
	out, err := s.st.GetItem(ctx, itemID)
	if err == nil {
		l := []Item{out}
		s.suggestStatus(ctx, l)
		out = l[0]
	}
	return out, err
}
