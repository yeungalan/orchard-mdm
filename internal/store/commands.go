package store

import (
	"fmt"
	"strings"
)

// Command statuses.
const (
	StatusQueued             = "Queued"
	StatusSent               = "Sent"
	StatusAcknowledged       = "Acknowledged"
	StatusError              = "Error"
	StatusCommandFormatError = "CommandFormatError"
	StatusNotNow             = "NotNow"
	StatusCanceled           = "Canceled"
	StatusExpired            = "Expired"
)

// Command is an MDM command queued for a device.
type Command struct {
	UUID        string `json:"uuid"`
	DeviceID    string `json:"device_id"`
	RequestType string `json:"request_type"`
	Payload     []byte `json:"-"`
	Status      string `json:"status"`
	Result      []byte `json:"-"`
	ErrorText   string `json:"error_text"`
	CreatedAt   int64  `json:"created_at"`
	SentAt      int64  `json:"sent_at"`
	CompletedAt int64  `json:"completed_at"`
	CreatedBy   string `json:"created_by"`
	Source      string `json:"source"`
	Ref         string `json:"ref"`

	// DeviceName is filled by list queries for display.
	DeviceName string `json:"device_name,omitempty"`
}

const commandCols = `c.uuid, c.device_id, c.request_type, c.payload, c.status, c.result, c.error_text, c.created_at, c.sent_at, c.completed_at, c.created_by, c.source, c.ref`

func scanCommand(r scanner, withName bool) (*Command, error) {
	c := &Command{}
	dest := []any{&c.UUID, &c.DeviceID, &c.RequestType, &c.Payload, &c.Status, &c.Result, &c.ErrorText, &c.CreatedAt, &c.SentAt, &c.CompletedAt, &c.CreatedBy, &c.Source, &c.Ref}
	if withName {
		dest = append(dest, &c.DeviceName)
	}
	if err := r.Scan(dest...); err != nil {
		return nil, notFound(err)
	}
	return c, nil
}

// InsertCommand queues a command.
func (s *Store) InsertCommand(c *Command) error {
	if c.CreatedAt == 0 {
		c.CreatedAt = Now()
	}
	if c.Status == "" {
		c.Status = StatusQueued
	}
	_, err := s.db.Exec(`INSERT INTO commands(uuid, device_id, request_type, payload, status, created_at, created_by, source, ref) VALUES(?,?,?,?,?,?,?,?,?)`,
		c.UUID, c.DeviceID, c.RequestType, c.Payload, c.Status, c.CreatedAt, c.CreatedBy, c.Source, c.Ref)
	return err
}

// GetCommand loads a command.
func (s *Store) GetCommand(uuid string) (*Command, error) {
	return scanCommand(s.db.QueryRow(`SELECT `+commandCols+`, COALESCE(d.device_name,'') FROM commands c LEFT JOIN devices d ON d.udid=c.device_id WHERE c.uuid=?`, uuid), true)
}

// NextCommand returns the next command to deliver to a device. When skipNotNow
// is true, commands the device previously deferred are skipped (the device is
// still busy in the current session).
func (s *Store) NextCommand(udid string, skipNotNow bool) (*Command, error) {
	statuses := `('Queued','Sent','NotNow')`
	if skipNotNow {
		statuses = `('Queued','Sent')`
	}
	return scanCommand(s.db.QueryRow(`SELECT `+commandCols+` FROM commands c WHERE c.device_id=? AND c.status IN `+statuses+` ORDER BY c.created_at, c.rowid LIMIT 1`, udid), false)
}

// MarkCommandSent records delivery of a command.
func (s *Store) MarkCommandSent(uuid string) error {
	_, err := s.db.Exec(`UPDATE commands SET status='Sent', sent_at=? WHERE uuid=?`, Now(), uuid)
	return err
}

// CompleteCommand stores a device response to a command. It only updates
// commands that belong to the given device.
func (s *Store) CompleteCommand(udid, uuid, status string, result []byte, errText string) (*Command, error) {
	completed := Now()
	if status == StatusNotNow {
		completed = 0
	}
	res, err := s.db.Exec(`UPDATE commands SET status=?, result=?, error_text=?, completed_at=? WHERE uuid=? AND device_id=?`,
		status, result, errText, completed, uuid, udid)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return s.GetCommand(uuid)
}

// CancelCommand cancels a command that has not completed yet.
func (s *Store) CancelCommand(uuid string) (bool, error) {
	res, err := s.db.Exec(`UPDATE commands SET status='Canceled', completed_at=? WHERE uuid=? AND status IN ('Queued','NotNow','Sent')`, Now(), uuid)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// CancelDeviceCommands cancels every pending command for a device.
func (s *Store) CancelDeviceCommands(udid string) (int64, error) {
	res, err := s.db.Exec(`UPDATE commands SET status='Canceled', completed_at=? WHERE device_id=? AND status IN ('Queued','NotNow','Sent')`, Now(), udid)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// HasPendingCommand reports whether a device already has a pending command
// with the given request type (and ref, if non-empty).
func (s *Store) HasPendingCommand(udid, requestType, ref string) bool {
	q := `SELECT COUNT(*) FROM commands WHERE device_id=? AND request_type=? AND status IN ('Queued','Sent','NotNow')`
	args := []any{udid, requestType}
	if ref != "" {
		q += ` AND ref=?`
		args = append(args, ref)
	}
	var n int
	_ = s.db.QueryRow(q, args...).Scan(&n)
	return n > 0
}

// PendingCommandCount returns the number of undelivered commands for a device
// (all devices when udid is empty).
func (s *Store) PendingCommandCount(udid string) int {
	var n int
	if udid == "" {
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM commands WHERE status IN ('Queued','Sent','NotNow')`).Scan(&n)
	} else {
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM commands WHERE device_id=? AND status IN ('Queued','Sent','NotNow')`, udid).Scan(&n)
	}
	return n
}

// DevicesWithPendingCommands lists devices that have commands waiting and were
// last pushed before the given time.
func (s *Store) DevicesWithPendingCommands(pushedBefore int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT c.device_id FROM commands c JOIN devices d ON d.udid=c.device_id
		WHERE c.status IN ('Queued','Sent','NotNow') AND d.enrollment_status='enrolled' AND d.last_push<?`, pushedBefore)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// CommandFilter narrows ListCommands.
type CommandFilter struct {
	DeviceID    string
	Status      string // single status, "pending" for Queued/Sent/NotNow, "failed" for errors
	RequestType string
	Limit       int
	Offset      int
}

// ListCommands lists commands, newest first.
func (s *Store) ListCommands(f CommandFilter) ([]*Command, int, error) {
	var where []string
	var args []any
	if f.DeviceID != "" {
		where = append(where, `c.device_id=?`)
		args = append(args, f.DeviceID)
	}
	switch f.Status {
	case "":
	case "pending":
		where = append(where, `c.status IN ('Queued','Sent','NotNow')`)
	case "failed":
		where = append(where, `c.status IN ('Error','CommandFormatError')`)
	default:
		where = append(where, `c.status=?`)
		args = append(args, f.Status)
	}
	if f.RequestType != "" {
		where = append(where, `c.request_type=?`)
		args = append(args, f.RequestType)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM commands c`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT `+commandCols+`, COALESCE(d.device_name,'') FROM commands c LEFT JOIN devices d ON d.udid=c.device_id`+cond+
		fmt.Sprintf(` ORDER BY c.created_at DESC, c.rowid DESC LIMIT %d OFFSET %d`, limit, f.Offset), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Command
	for rows.Next() {
		c, err := scanCommand(rows, true)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	return out, total, rows.Err()
}

// ExpireCommands marks commands that stayed pending longer than maxAge seconds as expired.
func (s *Store) ExpireCommands(maxAge int64) (int64, error) {
	res, err := s.db.Exec(`UPDATE commands SET status='Expired', completed_at=? WHERE status IN ('Queued','Sent','NotNow') AND created_at<?`, Now(), Now()-maxAge)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// PruneCommands deletes completed commands older than maxAge seconds.
func (s *Store) PruneCommands(maxAge int64) (int64, error) {
	res, err := s.db.Exec(`DELETE FROM commands WHERE status NOT IN ('Queued','Sent','NotNow') AND created_at<?`, Now()-maxAge)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CommandStats returns counts per status for the last `since` seconds.
func (s *Store) CommandStats(since int64) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM commands WHERE created_at>=? GROUP BY status`, Now()-since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// ---- events & audit ----

// Event is an entry in the activity feed.
type Event struct {
	ID         int64  `json:"id"`
	TS         int64  `json:"ts"`
	DeviceID   string `json:"device_id"`
	Type       string `json:"type"`
	Level      string `json:"level"`
	Message    string `json:"message"`
	Details    string `json:"details"`
	DeviceName string `json:"device_name,omitempty"`
}

// InsertEvent records an activity event.
func (s *Store) InsertEvent(e *Event) error {
	if e.TS == 0 {
		e.TS = Now()
	}
	if e.Level == "" {
		e.Level = "info"
	}
	res, err := s.db.Exec(`INSERT INTO events(ts, device_id, type, level, message, details) VALUES(?,?,?,?,?,?)`, e.TS, e.DeviceID, e.Type, e.Level, e.Message, e.Details)
	if err != nil {
		return err
	}
	e.ID, _ = res.LastInsertId()
	return nil
}

// ListEvents returns recent events, optionally for one device.
func (s *Store) ListEvents(deviceID, typePrefix string, limit, offset int) ([]*Event, error) {
	var where []string
	var args []any
	if deviceID != "" {
		where = append(where, `e.device_id=?`)
		args = append(args, deviceID)
	}
	if typePrefix != "" {
		where = append(where, `e.type LIKE ?`)
		args = append(args, typePrefix+"%")
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT e.id, e.ts, e.device_id, e.type, e.level, e.message, e.details, COALESCE(d.device_name,'') FROM events e LEFT JOIN devices d ON d.udid=e.device_id`+cond+
		fmt.Sprintf(` ORDER BY e.id DESC LIMIT %d OFFSET %d`, limit, offset), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Event{}
	for rows.Next() {
		e := &Event{}
		if err := rows.Scan(&e.ID, &e.TS, &e.DeviceID, &e.Type, &e.Level, &e.Message, &e.Details, &e.DeviceName); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// PruneEvents removes events older than maxAge seconds.
func (s *Store) PruneEvents(maxAge int64) {
	_, _ = s.db.Exec(`DELETE FROM events WHERE ts<?`, Now()-maxAge)
}

// AuditEntry is an administrative action record.
type AuditEntry struct {
	ID      int64  `json:"id"`
	TS      int64  `json:"ts"`
	Actor   string `json:"actor"`
	Action  string `json:"action"`
	Target  string `json:"target"`
	Details string `json:"details"`
	IP      string `json:"ip"`
}

// InsertAudit records an admin action.
func (s *Store) InsertAudit(a *AuditEntry) error {
	if a.TS == 0 {
		a.TS = Now()
	}
	_, err := s.db.Exec(`INSERT INTO audit_log(ts, actor, action, target, details, ip) VALUES(?,?,?,?,?,?)`, a.TS, a.Actor, a.Action, a.Target, a.Details, a.IP)
	return err
}

// ListAudit returns audit entries, newest first.
func (s *Store) ListAudit(q string, limit, offset int) ([]*AuditEntry, int, error) {
	cond := ""
	var args []any
	if q != "" {
		cond = ` WHERE actor LIKE ? OR action LIKE ? OR target LIKE ? OR details LIKE ?`
		like := "%" + q + "%"
		args = []any{like, like, like, like}
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit_log`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, ts, actor, action, target, details, ip FROM audit_log`+cond+fmt.Sprintf(` ORDER BY id DESC LIMIT %d OFFSET %d`, limit, offset), args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*AuditEntry{}
	for rows.Next() {
		a := &AuditEntry{}
		if err := rows.Scan(&a.ID, &a.TS, &a.Actor, &a.Action, &a.Target, &a.Details, &a.IP); err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}
