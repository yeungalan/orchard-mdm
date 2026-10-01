package store

import (
	"database/sql"
	"encoding/json"
)

// ---- groups ----

// GroupRule is one condition of a dynamic group.
type GroupRule struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// GroupRules describes dynamic membership.
type GroupRules struct {
	Match string      `json:"match"` // all|any
	Rules []GroupRule `json:"rules"`
}

// Group is a set of devices that items can be assigned to.
type Group struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Kind        string      `json:"kind"` // static|dynamic|all
	Rules       *GroupRules `json:"rules,omitempty"`
	CreatedAt   int64       `json:"created_at"`
	UpdatedAt   int64       `json:"updated_at"`
	MemberCount int         `json:"member_count"`
}

const groupCols = `g.id, g.name, g.description, g.kind, g.rules, g.created_at, g.updated_at, (SELECT COUNT(*) FROM group_members m WHERE m.group_id=g.id)`

func scanGroup(r scanner) (*Group, error) {
	g := &Group{}
	var rules string
	if err := r.Scan(&g.ID, &g.Name, &g.Description, &g.Kind, &rules, &g.CreatedAt, &g.UpdatedAt, &g.MemberCount); err != nil {
		return nil, notFound(err)
	}
	if rules != "" {
		g.Rules = &GroupRules{}
		_ = json.Unmarshal([]byte(rules), g.Rules)
	}
	return g, nil
}

// ListGroups returns all groups.
func (s *Store) ListGroups() ([]*Group, error) {
	rows, err := s.db.Query(`SELECT ` + groupCols + ` FROM groups g ORDER BY CASE g.kind WHEN 'all' THEN 0 ELSE 1 END, g.name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGroup loads a group.
func (s *Store) GetGroup(id int64) (*Group, error) {
	return scanGroup(s.db.QueryRow(`SELECT `+groupCols+` FROM groups g WHERE g.id=?`, id))
}

// AllDevicesGroupID returns the id of the built-in "All Devices" group.
func (s *Store) AllDevicesGroupID() int64 {
	var id int64
	_ = s.db.QueryRow(`SELECT id FROM groups WHERE kind='all' LIMIT 1`).Scan(&id)
	return id
}

// CreateGroup inserts a group.
func (s *Store) CreateGroup(g *Group) error {
	g.CreatedAt, g.UpdatedAt = Now(), Now()
	rules := ""
	if g.Rules != nil {
		rules = mustJSON(g.Rules)
	}
	res, err := s.db.Exec(`INSERT INTO groups(name, description, kind, rules, created_at, updated_at) VALUES(?,?,?,?,?,?)`, g.Name, g.Description, g.Kind, rules, g.CreatedAt, g.UpdatedAt)
	if err != nil {
		return err
	}
	g.ID, _ = res.LastInsertId()
	return nil
}

// UpdateGroup saves a group.
func (s *Store) UpdateGroup(g *Group) error {
	g.UpdatedAt = Now()
	rules := ""
	if g.Rules != nil {
		rules = mustJSON(g.Rules)
	}
	_, err := s.db.Exec(`UPDATE groups SET name=?, description=?, kind=?, rules=?, updated_at=? WHERE id=?`, g.Name, g.Description, g.Kind, rules, g.UpdatedAt, g.ID)
	return err
}

// DeleteGroup removes a group (and its assignments by cascade).
func (s *Store) DeleteGroup(id int64) error {
	_, err := s.db.Exec(`DELETE FROM groups WHERE id=? AND kind<>'all'`, id)
	return err
}

// GroupMemberIDs returns device ids in a group.
func (s *Store) GroupMemberIDs(groupID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT device_id FROM group_members WHERE group_id=?`, groupID)
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

// AddGroupMembers adds devices to a static group.
func (s *Store) AddGroupMembers(groupID int64, udids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, u := range udids {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO group_members(group_id, device_id) SELECT ?, udid FROM devices WHERE udid=?`, groupID, u); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// RemoveGroupMembers removes devices from a group.
func (s *Store) RemoveGroupMembers(groupID int64, udids []string) error {
	if len(udids) == 0 {
		return nil
	}
	args := append([]any{groupID}, stringsToAny(udids)...)
	_, err := s.db.Exec(`DELETE FROM group_members WHERE group_id=? AND device_id IN (`+placeholders(len(udids))+`)`, args...)
	return err
}

// SetGroupMembers replaces the full membership of a group and reports whether it changed.
func (s *Store) SetGroupMembers(groupID int64, udids []string) (bool, error) {
	current, err := s.GroupMemberIDs(groupID)
	if err != nil {
		return false, err
	}
	want := map[string]bool{}
	for _, u := range udids {
		want[u] = true
	}
	have := map[string]bool{}
	var remove []string
	for _, u := range current {
		have[u] = true
		if !want[u] {
			remove = append(remove, u)
		}
	}
	var add []string
	for u := range want {
		if !have[u] {
			add = append(add, u)
		}
	}
	if len(add) == 0 && len(remove) == 0 {
		return false, nil
	}
	if err := s.RemoveGroupMembers(groupID, remove); err != nil {
		return false, err
	}
	return true, s.AddGroupMembers(groupID, add)
}

// DeviceGroups returns groups a device belongs to.
func (s *Store) DeviceGroups(udid string) ([]*Group, error) {
	rows, err := s.db.Query(`SELECT `+groupCols+` FROM groups g JOIN group_members gm ON gm.group_id=g.id WHERE gm.device_id=? ORDER BY g.name COLLATE NOCASE`, udid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// DeviceGroupIDs returns group ids of a device.
func (s *Store) DeviceGroupIDs(udid string) ([]int64, error) {
	rows, err := s.db.Query(`SELECT group_id FROM group_members WHERE device_id=?`, udid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ---- profiles ----

// Profile is a configuration profile managed by Orchard.
type Profile struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Identifier   string          `json:"identifier"`
	Description  string          `json:"description"`
	Source       string          `json:"source"` // builder|upload
	Payloads     json.RawMessage `json:"payloads,omitempty"`
	Raw          []byte          `json:"-"`
	Version      int64           `json:"version"`
	Removable    bool            `json:"removable"`
	Scope        string          `json:"scope"`
	PayloadTypes []string        `json:"payload_types"`
	CreatedAt    int64           `json:"created_at"`
	UpdatedAt    int64           `json:"updated_at"`
}

const profileCols = `id, name, identifier, description, source, payloads, raw, version, removable, scope, payload_types, created_at, updated_at`

func scanProfile(r scanner) (*Profile, error) {
	p := &Profile{}
	var payloads, pt string
	var removable int
	if err := r.Scan(&p.ID, &p.Name, &p.Identifier, &p.Description, &p.Source, &payloads, &p.Raw, &p.Version, &removable, &p.Scope, &pt, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	if payloads != "" {
		p.Payloads = json.RawMessage(payloads)
	}
	p.Removable = removable == 1
	p.PayloadTypes = unmarshalJSON[[]string](pt)
	if p.PayloadTypes == nil {
		p.PayloadTypes = []string{}
	}
	return p, nil
}

// ListProfiles returns all profiles.
func (s *Store) ListProfiles() ([]*Profile, error) {
	rows, err := s.db.Query(`SELECT ` + profileCols + ` FROM profiles ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Profile{}
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProfile loads a profile.
func (s *Store) GetProfile(id int64) (*Profile, error) {
	return scanProfile(s.db.QueryRow(`SELECT `+profileCols+` FROM profiles WHERE id=?`, id))
}

// GetProfileByIdentifier loads a profile by PayloadIdentifier.
func (s *Store) GetProfileByIdentifier(ident string) (*Profile, error) {
	return scanProfile(s.db.QueryRow(`SELECT `+profileCols+` FROM profiles WHERE identifier=?`, ident))
}

// CreateProfile inserts a profile.
func (s *Store) CreateProfile(p *Profile) error {
	p.CreatedAt, p.UpdatedAt = Now(), Now()
	if p.Version == 0 {
		p.Version = 1
	}
	res, err := s.db.Exec(`INSERT INTO profiles(name, identifier, description, source, payloads, raw, version, removable, scope, payload_types, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Identifier, p.Description, p.Source, string(p.Payloads), p.Raw, p.Version, boolInt(p.Removable), p.Scope, mustJSON(p.PayloadTypes), p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	return nil
}

// UpdateProfile saves a profile.
func (s *Store) UpdateProfile(p *Profile) error {
	p.UpdatedAt = Now()
	_, err := s.db.Exec(`UPDATE profiles SET name=?, identifier=?, description=?, source=?, payloads=?, raw=?, version=?, removable=?, scope=?, payload_types=?, updated_at=? WHERE id=?`,
		p.Name, p.Identifier, p.Description, p.Source, string(p.Payloads), p.Raw, p.Version, boolInt(p.Removable), p.Scope, mustJSON(p.PayloadTypes), p.UpdatedAt, p.ID)
	return err
}

// DeleteProfile removes a profile and its assignments.
func (s *Store) DeleteProfile(id int64) error {
	_, err := s.db.Exec(`DELETE FROM profiles WHERE id=?`, id)
	if err == nil {
		_, err = s.db.Exec(`DELETE FROM assignments WHERE item_type='profile' AND item_id=?`, id)
	}
	return err
}

// ---- apps ----

// App is an application in the catalog.
type App struct {
	ID               int64           `json:"id"`
	Kind             string          `json:"kind"` // appstore|vpp|enterprise
	Name             string          `json:"name"`
	BundleID         string          `json:"bundle_id"`
	ITunesID         int64           `json:"itunes_id"`
	Version          string          `json:"version"`
	IconURL          string          `json:"icon_url"`
	Seller           string          `json:"seller"`
	Description      string          `json:"description"`
	IPAFile          string          `json:"-"`
	IPASize          int64           `json:"ipa_size"`
	FileSecret       string          `json:"-"`
	Config           json.RawMessage `json:"config,omitempty"`
	Attributes       json.RawMessage `json:"attributes,omitempty"`
	RemoveOnUnenroll bool            `json:"remove_on_unenroll"`
	PreventBackup    bool            `json:"prevent_backup"`
	UseVPP           bool            `json:"use_vpp"`
	TakeManagement   bool            `json:"take_management"`
	CreatedAt        int64           `json:"created_at"`
	UpdatedAt        int64           `json:"updated_at"`
}

const appCols = `id, kind, name, bundle_id, itunes_id, version, icon_url, seller, description, ipa_file, ipa_size, file_secret, config, attributes, remove_on_unenroll, prevent_backup, use_vpp, take_management, created_at, updated_at`

func scanApp(r scanner) (*App, error) {
	a := &App{}
	var cfg, attrs string
	var rm, pb, vpp, tm int
	if err := r.Scan(&a.ID, &a.Kind, &a.Name, &a.BundleID, &a.ITunesID, &a.Version, &a.IconURL, &a.Seller, &a.Description, &a.IPAFile, &a.IPASize, &a.FileSecret,
		&cfg, &attrs, &rm, &pb, &vpp, &tm, &a.CreatedAt, &a.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	if cfg != "" {
		a.Config = json.RawMessage(cfg)
	}
	if attrs != "" {
		a.Attributes = json.RawMessage(attrs)
	}
	a.RemoveOnUnenroll, a.PreventBackup, a.UseVPP, a.TakeManagement = rm == 1, pb == 1, vpp == 1, tm == 1
	return a, nil
}

// ListApps returns the app catalog.
func (s *Store) ListApps() ([]*App, error) {
	rows, err := s.db.Query(`SELECT ` + appCols + ` FROM apps ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*App{}
	for rows.Next() {
		a, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetApp loads an app.
func (s *Store) GetApp(id int64) (*App, error) {
	return scanApp(s.db.QueryRow(`SELECT `+appCols+` FROM apps WHERE id=?`, id))
}

// CreateApp inserts an app.
func (s *Store) CreateApp(a *App) error {
	a.CreatedAt, a.UpdatedAt = Now(), Now()
	res, err := s.db.Exec(`INSERT INTO apps(kind, name, bundle_id, itunes_id, version, icon_url, seller, description, ipa_file, ipa_size, file_secret, config, attributes, remove_on_unenroll, prevent_backup, use_vpp, take_management, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.Kind, a.Name, a.BundleID, a.ITunesID, a.Version, a.IconURL, a.Seller, a.Description, a.IPAFile, a.IPASize, a.FileSecret, string(a.Config), string(a.Attributes),
		boolInt(a.RemoveOnUnenroll), boolInt(a.PreventBackup), boolInt(a.UseVPP), boolInt(a.TakeManagement), a.CreatedAt, a.UpdatedAt)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

// UpdateApp saves an app.
func (s *Store) UpdateApp(a *App) error {
	a.UpdatedAt = Now()
	_, err := s.db.Exec(`UPDATE apps SET kind=?, name=?, bundle_id=?, itunes_id=?, version=?, icon_url=?, seller=?, description=?, ipa_file=?, ipa_size=?, file_secret=?, config=?, attributes=?,
		remove_on_unenroll=?, prevent_backup=?, use_vpp=?, take_management=?, updated_at=? WHERE id=?`,
		a.Kind, a.Name, a.BundleID, a.ITunesID, a.Version, a.IconURL, a.Seller, a.Description, a.IPAFile, a.IPASize, a.FileSecret, string(a.Config), string(a.Attributes),
		boolInt(a.RemoveOnUnenroll), boolInt(a.PreventBackup), boolInt(a.UseVPP), boolInt(a.TakeManagement), a.UpdatedAt, a.ID)
	return err
}

// DeleteApp removes an app and its assignments.
func (s *Store) DeleteApp(id int64) error {
	_, err := s.db.Exec(`DELETE FROM apps WHERE id=?`, id)
	if err == nil {
		_, err = s.db.Exec(`DELETE FROM assignments WHERE item_type='app' AND item_id=?`, id)
	}
	if err == nil {
		_, err = s.db.Exec(`DELETE FROM device_app_state WHERE app_id=?`, id)
	}
	return err
}

// ---- declarations (DDM) ----

// Declaration is a Declarative Device Management declaration.
type Declaration struct {
	ID          int64           `json:"id"`
	Identifier  string          `json:"identifier"`
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Payload     json.RawMessage `json:"payload"`
	ServerToken string          `json:"server_token"`
	CreatedAt   int64           `json:"created_at"`
	UpdatedAt   int64           `json:"updated_at"`
}

const declCols = `id, identifier, type, name, description, payload, server_token, created_at, updated_at`

func scanDecl(r scanner) (*Declaration, error) {
	d := &Declaration{}
	var payload string
	if err := r.Scan(&d.ID, &d.Identifier, &d.Type, &d.Name, &d.Description, &payload, &d.ServerToken, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	d.Payload = json.RawMessage(payload)
	return d, nil
}

// ListDeclarations returns all declarations.
func (s *Store) ListDeclarations() ([]*Declaration, error) {
	rows, err := s.db.Query(`SELECT ` + declCols + ` FROM declarations ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Declaration{}
	for rows.Next() {
		d, err := scanDecl(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDeclaration loads a declaration by id.
func (s *Store) GetDeclaration(id int64) (*Declaration, error) {
	return scanDecl(s.db.QueryRow(`SELECT `+declCols+` FROM declarations WHERE id=?`, id))
}

// GetDeclarationByIdentifier loads a declaration by identifier.
func (s *Store) GetDeclarationByIdentifier(ident string) (*Declaration, error) {
	return scanDecl(s.db.QueryRow(`SELECT `+declCols+` FROM declarations WHERE identifier=?`, ident))
}

// CreateDeclaration inserts a declaration.
func (s *Store) CreateDeclaration(d *Declaration) error {
	d.CreatedAt, d.UpdatedAt = Now(), Now()
	res, err := s.db.Exec(`INSERT INTO declarations(identifier, type, name, description, payload, server_token, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		d.Identifier, d.Type, d.Name, d.Description, string(d.Payload), d.ServerToken, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	return nil
}

// UpdateDeclaration saves a declaration.
func (s *Store) UpdateDeclaration(d *Declaration) error {
	d.UpdatedAt = Now()
	_, err := s.db.Exec(`UPDATE declarations SET identifier=?, type=?, name=?, description=?, payload=?, server_token=?, updated_at=? WHERE id=?`,
		d.Identifier, d.Type, d.Name, d.Description, string(d.Payload), d.ServerToken, d.UpdatedAt, d.ID)
	return err
}

// DeleteDeclaration removes a declaration and its assignments.
func (s *Store) DeleteDeclaration(id int64) error {
	_, err := s.db.Exec(`DELETE FROM declarations WHERE id=?`, id)
	if err == nil {
		_, err = s.db.Exec(`DELETE FROM assignments WHERE item_type='declaration' AND item_id=?`, id)
	}
	return err
}

// DeclarationStatus is a device's report about one declaration.
type DeclarationStatus struct {
	Identifier  string `json:"identifier"`
	Active      bool   `json:"active"`
	Valid       string `json:"valid"`
	ServerToken string `json:"server_token"`
	Reasons     any    `json:"reasons,omitempty"`
	UpdatedAt   int64  `json:"updated_at"`
}

// UpsertDeclarationStatus stores a device's declaration status.
func (s *Store) UpsertDeclarationStatus(udid string, st DeclarationStatus) error {
	_, err := s.db.Exec(`INSERT INTO device_declaration_status(device_id, identifier, active, valid, server_token, reasons, updated_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(device_id, identifier) DO UPDATE SET active=excluded.active, valid=excluded.valid, server_token=excluded.server_token, reasons=excluded.reasons, updated_at=excluded.updated_at`,
		udid, st.Identifier, boolInt(st.Active), st.Valid, st.ServerToken, mustJSON(st.Reasons), Now())
	return err
}

// ListDeclarationStatus returns the declaration statuses of a device.
func (s *Store) ListDeclarationStatus(udid string) ([]DeclarationStatus, error) {
	rows, err := s.db.Query(`SELECT identifier, active, valid, server_token, reasons, updated_at FROM device_declaration_status WHERE device_id=? ORDER BY identifier`, udid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeclarationStatus{}
	for rows.Next() {
		var st DeclarationStatus
		var active int
		var reasons string
		if err := rows.Scan(&st.Identifier, &active, &st.Valid, &st.ServerToken, &reasons, &st.UpdatedAt); err != nil {
			return nil, err
		}
		st.Active = active == 1
		if reasons != "" {
			st.Reasons = unmarshalJSON[any](reasons)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ---- compliance policies ----

// CompliancePolicy defines rules a device must satisfy.
type CompliancePolicy struct {
	ID          int64           `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Enabled     bool            `json:"enabled"`
	Rules       json.RawMessage `json:"rules"`
	Actions     json.RawMessage `json:"actions"`
	GraceHours  int             `json:"grace_hours"`
	CreatedAt   int64           `json:"created_at"`
	UpdatedAt   int64           `json:"updated_at"`
}

const policyCols = `id, name, description, enabled, rules, actions, grace_hours, created_at, updated_at`

func scanPolicy(r scanner) (*CompliancePolicy, error) {
	p := &CompliancePolicy{}
	var en int
	var rules, actions string
	if err := r.Scan(&p.ID, &p.Name, &p.Description, &en, &rules, &actions, &p.GraceHours, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	p.Enabled = en == 1
	p.Rules, p.Actions = json.RawMessage(rules), json.RawMessage(actions)
	return p, nil
}

// ListCompliancePolicies returns all policies.
func (s *Store) ListCompliancePolicies() ([]*CompliancePolicy, error) {
	rows, err := s.db.Query(`SELECT ` + policyCols + ` FROM compliance_policies ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*CompliancePolicy{}
	for rows.Next() {
		p, err := scanPolicy(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetCompliancePolicy loads a policy.
func (s *Store) GetCompliancePolicy(id int64) (*CompliancePolicy, error) {
	return scanPolicy(s.db.QueryRow(`SELECT `+policyCols+` FROM compliance_policies WHERE id=?`, id))
}

// CreateCompliancePolicy inserts a policy.
func (s *Store) CreateCompliancePolicy(p *CompliancePolicy) error {
	p.CreatedAt, p.UpdatedAt = Now(), Now()
	res, err := s.db.Exec(`INSERT INTO compliance_policies(name, description, enabled, rules, actions, grace_hours, created_at, updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		p.Name, p.Description, boolInt(p.Enabled), string(p.Rules), string(p.Actions), p.GraceHours, p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	return nil
}

// UpdateCompliancePolicy saves a policy.
func (s *Store) UpdateCompliancePolicy(p *CompliancePolicy) error {
	p.UpdatedAt = Now()
	_, err := s.db.Exec(`UPDATE compliance_policies SET name=?, description=?, enabled=?, rules=?, actions=?, grace_hours=?, updated_at=? WHERE id=?`,
		p.Name, p.Description, boolInt(p.Enabled), string(p.Rules), string(p.Actions), p.GraceHours, p.UpdatedAt, p.ID)
	return err
}

// DeleteCompliancePolicy removes a policy.
func (s *Store) DeleteCompliancePolicy(id int64) error {
	_, err := s.db.Exec(`DELETE FROM compliance_policies WHERE id=?`, id)
	if err == nil {
		_, err = s.db.Exec(`DELETE FROM assignments WHERE item_type='compliance' AND item_id=?`, id)
	}
	return err
}

// ---- assignments ----

// Assignment links a profile/app/declaration/compliance policy to a group.
type Assignment struct {
	ID        int64  `json:"id"`
	ItemType  string `json:"item_type"`
	ItemID    int64  `json:"item_id"`
	GroupID   int64  `json:"group_id"`
	Intent    string `json:"intent"`
	CreatedAt int64  `json:"created_at"`
	GroupName string `json:"group_name,omitempty"`
}

// ListAssignments filters assignments by item type/id or group (zero values ignored).
func (s *Store) ListAssignments(itemType string, itemID, groupID int64) ([]*Assignment, error) {
	q := `SELECT a.id, a.item_type, a.item_id, a.group_id, a.intent, a.created_at, COALESCE(g.name,'') FROM assignments a LEFT JOIN groups g ON g.id=a.group_id WHERE 1=1`
	var args []any
	if itemType != "" {
		q += ` AND a.item_type=?`
		args = append(args, itemType)
	}
	if itemID > 0 {
		q += ` AND a.item_id=?`
		args = append(args, itemID)
	}
	if groupID > 0 {
		q += ` AND a.group_id=?`
		args = append(args, groupID)
	}
	rows, err := s.db.Query(q+` ORDER BY a.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Assignment{}
	for rows.Next() {
		a := &Assignment{}
		if err := rows.Scan(&a.ID, &a.ItemType, &a.ItemID, &a.GroupID, &a.Intent, &a.CreatedAt, &a.GroupName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpsertAssignment creates or updates an assignment.
func (s *Store) UpsertAssignment(a *Assignment) error {
	a.CreatedAt = Now()
	_, err := s.db.Exec(`INSERT INTO assignments(item_type, item_id, group_id, intent, created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(item_type, item_id, group_id) DO UPDATE SET intent=excluded.intent`, a.ItemType, a.ItemID, a.GroupID, a.Intent, a.CreatedAt)
	if err != nil {
		return err
	}
	return s.db.QueryRow(`SELECT id FROM assignments WHERE item_type=? AND item_id=? AND group_id=?`, a.ItemType, a.ItemID, a.GroupID).Scan(&a.ID)
}

// DeleteAssignment removes an assignment.
func (s *Store) DeleteAssignment(id int64) (*Assignment, error) {
	a := &Assignment{}
	err := s.db.QueryRow(`SELECT id, item_type, item_id, group_id, intent, created_at FROM assignments WHERE id=?`, id).
		Scan(&a.ID, &a.ItemType, &a.ItemID, &a.GroupID, &a.Intent, &a.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	_, err = s.db.Exec(`DELETE FROM assignments WHERE id=?`, id)
	return a, err
}

// DeviceAssignments returns every assignment that applies to a device through its groups.
func (s *Store) DeviceAssignments(udid string) ([]*Assignment, error) {
	rows, err := s.db.Query(`SELECT a.id, a.item_type, a.item_id, a.group_id, a.intent, a.created_at, COALESCE(g.name,'') FROM assignments a
		JOIN group_members gm ON gm.group_id=a.group_id LEFT JOIN groups g ON g.id=a.group_id WHERE gm.device_id=? ORDER BY a.id`, udid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Assignment{}
	for rows.Next() {
		a := &Assignment{}
		if err := rows.Scan(&a.ID, &a.ItemType, &a.ItemID, &a.GroupID, &a.Intent, &a.CreatedAt, &a.GroupName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ---- per-device desired state ----

// ProfileState tracks a managed profile on a device.
type ProfileState struct {
	DeviceID    string `json:"device_id"`
	ProfileID   int64  `json:"profile_id"`
	Identifier  string `json:"identifier"`
	Version     int64  `json:"version"`
	Status      string `json:"status"` // pending|installed|failed|removing
	CommandUUID string `json:"command_uuid"`
	Error       string `json:"error"`
	UpdatedAt   int64  `json:"updated_at"`
	DeviceName  string `json:"device_name,omitempty"`
	ProfileName string `json:"profile_name,omitempty"`
}

// SetProfileState upserts the state of a profile on a device.
func (s *Store) SetProfileState(st *ProfileState) error {
	st.UpdatedAt = Now()
	_, err := s.db.Exec(`INSERT INTO device_profile_state(device_id, profile_id, identifier, version, status, command_uuid, error, updated_at) VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(device_id, profile_id) DO UPDATE SET identifier=excluded.identifier, version=excluded.version, status=excluded.status, command_uuid=excluded.command_uuid, error=excluded.error, updated_at=excluded.updated_at`,
		st.DeviceID, st.ProfileID, st.Identifier, st.Version, st.Status, st.CommandUUID, st.Error, st.UpdatedAt)
	return err
}

// DeleteProfileState removes tracking for a profile on a device.
func (s *Store) DeleteProfileState(udid string, profileID int64) error {
	_, err := s.db.Exec(`DELETE FROM device_profile_state WHERE device_id=? AND profile_id=?`, udid, profileID)
	return err
}

// ListProfileStates returns profile states filtered by device or profile.
func (s *Store) ListProfileStates(udid string, profileID int64) ([]*ProfileState, error) {
	q := `SELECT s.device_id, s.profile_id, s.identifier, s.version, s.status, s.command_uuid, s.error, s.updated_at, COALESCE(d.device_name,''), COALESCE(p.name, s.identifier)
		FROM device_profile_state s LEFT JOIN devices d ON d.udid=s.device_id LEFT JOIN profiles p ON p.id=s.profile_id WHERE 1=1`
	var args []any
	if udid != "" {
		q += ` AND s.device_id=?`
		args = append(args, udid)
	}
	if profileID > 0 {
		q += ` AND s.profile_id=?`
		args = append(args, profileID)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ProfileState{}
	for rows.Next() {
		st := &ProfileState{}
		if err := rows.Scan(&st.DeviceID, &st.ProfileID, &st.Identifier, &st.Version, &st.Status, &st.CommandUUID, &st.Error, &st.UpdatedAt, &st.DeviceName, &st.ProfileName); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// AppState tracks a managed app on a device.
type AppState struct {
	DeviceID    string `json:"device_id"`
	AppID       int64  `json:"app_id"`
	BundleID    string `json:"bundle_id"`
	Intent      string `json:"intent"`
	Status      string `json:"status"` // pending|installing|installed|failed|removing|removed
	CommandUUID string `json:"command_uuid"`
	Error       string `json:"error"`
	Attempts    int    `json:"attempts"`
	UpdatedAt   int64  `json:"updated_at"`
	DeviceName  string `json:"device_name,omitempty"`
	AppName     string `json:"app_name,omitempty"`
}

// SetAppState upserts the state of an app on a device.
func (s *Store) SetAppState(st *AppState) error {
	st.UpdatedAt = Now()
	_, err := s.db.Exec(`INSERT INTO device_app_state(device_id, app_id, bundle_id, intent, status, command_uuid, error, attempts, updated_at) VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(device_id, app_id) DO UPDATE SET bundle_id=excluded.bundle_id, intent=excluded.intent, status=excluded.status, command_uuid=excluded.command_uuid, error=excluded.error, attempts=excluded.attempts, updated_at=excluded.updated_at`,
		st.DeviceID, st.AppID, st.BundleID, st.Intent, st.Status, st.CommandUUID, st.Error, st.Attempts, st.UpdatedAt)
	return err
}

// DeleteAppState removes tracking for an app on a device.
func (s *Store) DeleteAppState(udid string, appID int64) error {
	_, err := s.db.Exec(`DELETE FROM device_app_state WHERE device_id=? AND app_id=?`, udid, appID)
	return err
}

// ListAppStates returns app states filtered by device or app.
func (s *Store) ListAppStates(udid string, appID int64) ([]*AppState, error) {
	q := `SELECT s.device_id, s.app_id, s.bundle_id, s.intent, s.status, s.command_uuid, s.error, s.attempts, s.updated_at, COALESCE(d.device_name,''), COALESCE(a.name, s.bundle_id)
		FROM device_app_state s LEFT JOIN devices d ON d.udid=s.device_id LEFT JOIN apps a ON a.id=s.app_id WHERE 1=1`
	var args []any
	if udid != "" {
		q += ` AND s.device_id=?`
		args = append(args, udid)
	}
	if appID > 0 {
		q += ` AND s.app_id=?`
		args = append(args, appID)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AppState{}
	for rows.Next() {
		st := &AppState{}
		if err := rows.Scan(&st.DeviceID, &st.AppID, &st.BundleID, &st.Intent, &st.Status, &st.CommandUUID, &st.Error, &st.Attempts, &st.UpdatedAt, &st.DeviceName, &st.AppName); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ClearDeviceState forgets the tracked profile/app/declaration state of a device.
func (s *Store) ClearDeviceState(udid string) error {
	for _, q := range []string{`DELETE FROM device_profile_state WHERE device_id=?`, `DELETE FROM device_app_state WHERE device_id=?`, `DELETE FROM device_declaration_status WHERE device_id=?`} {
		if _, err := s.db.Exec(q, udid); err != nil {
			return err
		}
	}
	return nil
}

// FindStateByCommand returns the profile or app state that a command belongs to.
func (s *Store) FindProfileStateByCommand(cmdUUID string) (*ProfileState, error) {
	st := &ProfileState{}
	err := s.db.QueryRow(`SELECT device_id, profile_id, identifier, version, status, command_uuid, error, updated_at FROM device_profile_state WHERE command_uuid=?`, cmdUUID).
		Scan(&st.DeviceID, &st.ProfileID, &st.Identifier, &st.Version, &st.Status, &st.CommandUUID, &st.Error, &st.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return st, nil
}

// FindAppStateByCommand returns the app state a command belongs to.
func (s *Store) FindAppStateByCommand(cmdUUID string) (*AppState, error) {
	st := &AppState{}
	err := s.db.QueryRow(`SELECT device_id, app_id, bundle_id, intent, status, command_uuid, error, attempts, updated_at FROM device_app_state WHERE command_uuid=?`, cmdUUID).
		Scan(&st.DeviceID, &st.AppID, &st.BundleID, &st.Intent, &st.Status, &st.CommandUUID, &st.Error, &st.Attempts, &st.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return st, nil
}

var _ = sql.ErrNoRows
