package store

import (
	"strings"
)

// ---- enrollment tokens ----

// EnrollmentToken is a shareable enrollment link with defaults for the
// devices that enroll through it.
type EnrollmentToken struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	Token        string  `json:"token"`
	Ownership    string  `json:"ownership"`
	GroupIDs     []int64 `json:"group_ids"`
	AssignedUser string  `json:"assigned_user"`
	MaxUses      int     `json:"max_uses"`
	Uses         int     `json:"uses"`
	ExpiresAt    int64   `json:"expires_at"`
	Enabled      bool    `json:"enabled"`
	CreatedAt    int64   `json:"created_at"`
}

const etCols = `id, name, token, ownership, group_ids, assigned_user, max_uses, uses, expires_at, enabled, created_at`

func scanET(r scanner) (*EnrollmentToken, error) {
	t := &EnrollmentToken{}
	var groups string
	var en int
	if err := r.Scan(&t.ID, &t.Name, &t.Token, &t.Ownership, &groups, &t.AssignedUser, &t.MaxUses, &t.Uses, &t.ExpiresAt, &en, &t.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	t.GroupIDs = unmarshalJSON[[]int64](groups)
	if t.GroupIDs == nil {
		t.GroupIDs = []int64{}
	}
	t.Enabled = en == 1
	return t, nil
}

// Usable reports whether the token can still be used to enroll.
func (t *EnrollmentToken) Usable() bool {
	if !t.Enabled {
		return false
	}
	if t.ExpiresAt > 0 && t.ExpiresAt < Now() {
		return false
	}
	if t.MaxUses > 0 && t.Uses >= t.MaxUses {
		return false
	}
	return true
}

// ListEnrollmentTokens returns all tokens.
func (s *Store) ListEnrollmentTokens() ([]*EnrollmentToken, error) {
	rows, err := s.db.Query(`SELECT ` + etCols + ` FROM enrollment_tokens ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*EnrollmentToken{}
	for rows.Next() {
		t, err := scanET(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetEnrollmentToken loads a token by id.
func (s *Store) GetEnrollmentToken(id int64) (*EnrollmentToken, error) {
	return scanET(s.db.QueryRow(`SELECT `+etCols+` FROM enrollment_tokens WHERE id=?`, id))
}

// GetEnrollmentTokenByValue loads a token by its secret value.
func (s *Store) GetEnrollmentTokenByValue(tok string) (*EnrollmentToken, error) {
	if tok == "" {
		return nil, ErrNotFound
	}
	return scanET(s.db.QueryRow(`SELECT `+etCols+` FROM enrollment_tokens WHERE token=?`, tok))
}

// CreateEnrollmentToken inserts a token.
func (s *Store) CreateEnrollmentToken(t *EnrollmentToken) error {
	t.CreatedAt = Now()
	if t.Token == "" {
		t.Token = randomHex(16)
	}
	res, err := s.db.Exec(`INSERT INTO enrollment_tokens(name, token, ownership, group_ids, assigned_user, max_uses, uses, expires_at, enabled, created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		t.Name, t.Token, t.Ownership, mustJSON(t.GroupIDs), t.AssignedUser, t.MaxUses, t.Uses, t.ExpiresAt, boolInt(t.Enabled), t.CreatedAt)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

// UpdateEnrollmentToken saves a token.
func (s *Store) UpdateEnrollmentToken(t *EnrollmentToken) error {
	_, err := s.db.Exec(`UPDATE enrollment_tokens SET name=?, ownership=?, group_ids=?, assigned_user=?, max_uses=?, expires_at=?, enabled=? WHERE id=?`,
		t.Name, t.Ownership, mustJSON(t.GroupIDs), t.AssignedUser, t.MaxUses, t.ExpiresAt, boolInt(t.Enabled), t.ID)
	return err
}

// IncrementEnrollmentTokenUse bumps the use counter.
func (s *Store) IncrementEnrollmentTokenUse(id int64) {
	_, _ = s.db.Exec(`UPDATE enrollment_tokens SET uses=uses+1 WHERE id=?`, id)
}

// DeleteEnrollmentToken removes a token.
func (s *Store) DeleteEnrollmentToken(id int64) error {
	_, err := s.db.Exec(`DELETE FROM enrollment_tokens WHERE id=?`, id)
	return err
}

// ---- SCEP challenges / issued certificates ----

// CreateChallenge stores a one-time SCEP challenge.
func (s *Store) CreateChallenge(challenge, purpose, ref string, ttl int64) error {
	now := Now()
	_, err := s.db.Exec(`INSERT INTO scep_challenges(challenge, purpose, ref, created_at, expires_at) VALUES(?,?,?,?,?)`, challenge, purpose, ref, now, now+ttl)
	return err
}

// ConsumeChallenge validates and marks a challenge used. It returns the purpose and ref.
func (s *Store) ConsumeChallenge(challenge string) (purpose, ref string, ok bool) {
	if challenge == "" {
		return "", "", false
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", "", false
	}
	defer tx.Rollback()
	var expires, used int64
	if err := tx.QueryRow(`SELECT purpose, ref, expires_at, used_at FROM scep_challenges WHERE challenge=?`, challenge).Scan(&purpose, &ref, &expires, &used); err != nil {
		return "", "", false
	}
	if used != 0 || expires < Now() {
		return "", "", false
	}
	if _, err := tx.Exec(`UPDATE scep_challenges SET used_at=? WHERE challenge=?`, Now(), challenge); err != nil {
		return "", "", false
	}
	if tx.Commit() != nil {
		return "", "", false
	}
	return purpose, ref, true
}

// IssuedCert records a certificate signed by the Orchard CA.
type IssuedCert struct {
	Serial    string `json:"serial"`
	SHA256    string `json:"sha256"`
	Subject   string `json:"subject"`
	NotBefore int64  `json:"not_before"`
	NotAfter  int64  `json:"not_after"`
	Purpose   string `json:"purpose"`
	Ref       string `json:"ref"`
	DeviceID  string `json:"device_id"`
	Revoked   bool   `json:"revoked"`
	CreatedAt int64  `json:"created_at"`
}

// InsertIssuedCert records a certificate.
func (s *Store) InsertIssuedCert(c *IssuedCert) error {
	c.CreatedAt = Now()
	_, err := s.db.Exec(`INSERT INTO issued_certs(serial, sha256, subject, not_before, not_after, purpose, ref, device_id, revoked, created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`,
		c.Serial, c.SHA256, c.Subject, c.NotBefore, c.NotAfter, c.Purpose, c.Ref, c.DeviceID, boolInt(c.Revoked), c.CreatedAt)
	return err
}

// GetIssuedCertBySHA looks up a certificate by fingerprint.
func (s *Store) GetIssuedCertBySHA(sha string) (*IssuedCert, error) {
	c := &IssuedCert{}
	var rev int
	err := s.db.QueryRow(`SELECT serial, sha256, subject, not_before, not_after, purpose, ref, device_id, revoked, created_at FROM issued_certs WHERE sha256=?`, sha).
		Scan(&c.Serial, &c.SHA256, &c.Subject, &c.NotBefore, &c.NotAfter, &c.Purpose, &c.Ref, &c.DeviceID, &rev, &c.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	c.Revoked = rev == 1
	return c, nil
}

// SetIssuedCertDevice binds an issued certificate to a device.
func (s *Store) SetIssuedCertDevice(sha, udid string) {
	_, _ = s.db.Exec(`UPDATE issued_certs SET device_id=? WHERE sha256=?`, udid, sha)
}

// RevokeIssuedCert marks a certificate revoked.
func (s *Store) RevokeIssuedCert(sha string) {
	_, _ = s.db.Exec(`UPDATE issued_certs SET revoked=1 WHERE sha256=?`, sha)
}

// ListIssuedCerts returns recently issued certificates.
func (s *Store) ListIssuedCerts(limit int) ([]*IssuedCert, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT serial, sha256, subject, not_before, not_after, purpose, ref, device_id, revoked, created_at FROM issued_certs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*IssuedCert{}
	for rows.Next() {
		c := &IssuedCert{}
		var rev int
		if err := rows.Scan(&c.Serial, &c.SHA256, &c.Subject, &c.NotBefore, &c.NotAfter, &c.Purpose, &c.Ref, &c.DeviceID, &rev, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.Revoked = rev == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---- DEP / ADE ----

// DEPServer is an Automated Device Enrollment server token.
type DEPServer struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	CertPEM           string `json:"-"`
	KeyPEM            string `json:"-"`
	ConsumerKey       string `json:"-"`
	ConsumerSecret    string `json:"-"`
	AccessToken       string `json:"-"`
	AccessSecret      string `json:"-"`
	AccessTokenExpiry int64  `json:"access_token_expiry"`
	ServerName        string `json:"server_name"`
	ServerUUID        string `json:"server_uuid"`
	OrgName           string `json:"org_name"`
	OrgEmail          string `json:"org_email"`
	OrgPhone          string `json:"org_phone"`
	OrgAddress        string `json:"org_address"`
	OrgID             string `json:"org_id"`
	AdminID           string `json:"admin_id"`
	Cursor            string `json:"-"`
	LastSync          int64  `json:"last_sync"`
	LastError         string `json:"last_error"`
	DefaultProfileID  int64  `json:"default_profile_id"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
	HasToken          bool   `json:"has_token"`
	DeviceCount       int    `json:"device_count"`
}

const depCols = `s.id, s.name, s.cert_pem, s.key_pem, s.consumer_key, s.consumer_secret, s.access_token, s.access_secret, s.access_token_expiry, s.server_name, s.server_uuid,
	s.org_name, s.org_email, s.org_phone, s.org_address, s.org_id, s.admin_id, s.cursor, s.last_sync, s.last_error, s.default_profile_id, s.created_at, s.updated_at,
	(SELECT COUNT(*) FROM dep_devices dd WHERE dd.dep_server_id=s.id)`

func scanDEP(r scanner) (*DEPServer, error) {
	d := &DEPServer{}
	if err := r.Scan(&d.ID, &d.Name, &d.CertPEM, &d.KeyPEM, &d.ConsumerKey, &d.ConsumerSecret, &d.AccessToken, &d.AccessSecret, &d.AccessTokenExpiry, &d.ServerName, &d.ServerUUID,
		&d.OrgName, &d.OrgEmail, &d.OrgPhone, &d.OrgAddress, &d.OrgID, &d.AdminID, &d.Cursor, &d.LastSync, &d.LastError, &d.DefaultProfileID, &d.CreatedAt, &d.UpdatedAt, &d.DeviceCount); err != nil {
		return nil, notFound(err)
	}
	d.HasToken = d.ConsumerKey != "" && d.AccessToken != ""
	return d, nil
}

// ListDEPServers returns ADE servers.
func (s *Store) ListDEPServers() ([]*DEPServer, error) {
	rows, err := s.db.Query(`SELECT ` + depCols + ` FROM dep_servers s ORDER BY s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DEPServer{}
	for rows.Next() {
		d, err := scanDEP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// GetDEPServer loads an ADE server.
func (s *Store) GetDEPServer(id int64) (*DEPServer, error) {
	return scanDEP(s.db.QueryRow(`SELECT `+depCols+` FROM dep_servers s WHERE s.id=?`, id))
}

// CreateDEPServer inserts an ADE server.
func (s *Store) CreateDEPServer(d *DEPServer) error {
	d.CreatedAt, d.UpdatedAt = Now(), Now()
	res, err := s.db.Exec(`INSERT INTO dep_servers(name, cert_pem, key_pem, created_at, updated_at) VALUES(?,?,?,?,?)`, d.Name, d.CertPEM, d.KeyPEM, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return err
	}
	d.ID, _ = res.LastInsertId()
	return nil
}

// UpdateDEPServer saves an ADE server.
func (s *Store) UpdateDEPServer(d *DEPServer) error {
	d.UpdatedAt = Now()
	_, err := s.db.Exec(`UPDATE dep_servers SET name=?, cert_pem=?, key_pem=?, consumer_key=?, consumer_secret=?, access_token=?, access_secret=?, access_token_expiry=?,
		server_name=?, server_uuid=?, org_name=?, org_email=?, org_phone=?, org_address=?, org_id=?, admin_id=?, cursor=?, last_sync=?, last_error=?, default_profile_id=?, updated_at=? WHERE id=?`,
		d.Name, d.CertPEM, d.KeyPEM, d.ConsumerKey, d.ConsumerSecret, d.AccessToken, d.AccessSecret, d.AccessTokenExpiry,
		d.ServerName, d.ServerUUID, d.OrgName, d.OrgEmail, d.OrgPhone, d.OrgAddress, d.OrgID, d.AdminID, d.Cursor, d.LastSync, d.LastError, d.DefaultProfileID, d.UpdatedAt, d.ID)
	return err
}

// DeleteDEPServer removes an ADE server and its device records.
func (s *Store) DeleteDEPServer(id int64) error {
	_, err := s.db.Exec(`DELETE FROM dep_servers WHERE id=?`, id)
	return err
}

// DEPProfile is an ADE enrollment (Setup Assistant) profile definition.
type DEPProfile struct {
	ID        int64          `json:"id"`
	Name      string         `json:"name"`
	Config    map[string]any `json:"config"`
	GroupIDs  []int64        `json:"group_ids"`
	CreatedAt int64          `json:"created_at"`
	UpdatedAt int64          `json:"updated_at"`
}

func scanDEPProfile(r scanner) (*DEPProfile, error) {
	p := &DEPProfile{}
	var cfg, groups string
	if err := r.Scan(&p.ID, &p.Name, &cfg, &groups, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, notFound(err)
	}
	p.Config = unmarshalJSON[map[string]any](cfg)
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	p.GroupIDs = unmarshalJSON[[]int64](groups)
	if p.GroupIDs == nil {
		p.GroupIDs = []int64{}
	}
	return p, nil
}

// ListDEPProfiles returns ADE profiles.
func (s *Store) ListDEPProfiles() ([]*DEPProfile, error) {
	rows, err := s.db.Query(`SELECT id, name, config, group_ids, created_at, updated_at FROM dep_profiles ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DEPProfile{}
	for rows.Next() {
		p, err := scanDEPProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetDEPProfile loads an ADE profile.
func (s *Store) GetDEPProfile(id int64) (*DEPProfile, error) {
	return scanDEPProfile(s.db.QueryRow(`SELECT id, name, config, group_ids, created_at, updated_at FROM dep_profiles WHERE id=?`, id))
}

// CreateDEPProfile inserts an ADE profile.
func (s *Store) CreateDEPProfile(p *DEPProfile) error {
	p.CreatedAt, p.UpdatedAt = Now(), Now()
	res, err := s.db.Exec(`INSERT INTO dep_profiles(name, config, group_ids, created_at, updated_at) VALUES(?,?,?,?,?)`, p.Name, mustJSON(p.Config), mustJSON(p.GroupIDs), p.CreatedAt, p.UpdatedAt)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	return nil
}

// UpdateDEPProfile saves an ADE profile.
func (s *Store) UpdateDEPProfile(p *DEPProfile) error {
	p.UpdatedAt = Now()
	_, err := s.db.Exec(`UPDATE dep_profiles SET name=?, config=?, group_ids=?, updated_at=? WHERE id=?`, p.Name, mustJSON(p.Config), mustJSON(p.GroupIDs), p.UpdatedAt, p.ID)
	return err
}

// DeleteDEPProfile removes an ADE profile.
func (s *Store) DeleteDEPProfile(id int64) error {
	_, err := s.db.Exec(`DELETE FROM dep_profiles WHERE id=?`, id)
	return err
}

// GetDEPProfileUpload returns the Apple profile UUID for a profile on a server.
func (s *Store) GetDEPProfileUpload(profileID, serverID int64) (uuid, hash string) {
	_ = s.db.QueryRow(`SELECT profile_uuid, config_hash FROM dep_profile_uploads WHERE dep_profile_id=? AND dep_server_id=?`, profileID, serverID).Scan(&uuid, &hash)
	return
}

// SetDEPProfileUpload records an uploaded profile.
func (s *Store) SetDEPProfileUpload(profileID, serverID int64, uuid, hash string) error {
	_, err := s.db.Exec(`INSERT INTO dep_profile_uploads(dep_profile_id, dep_server_id, profile_uuid, config_hash, uploaded_at) VALUES(?,?,?,?,?)
		ON CONFLICT(dep_profile_id, dep_server_id) DO UPDATE SET profile_uuid=excluded.profile_uuid, config_hash=excluded.config_hash, uploaded_at=excluded.uploaded_at`,
		profileID, serverID, uuid, hash, Now())
	return err
}

// DEPProfileIDByUUID maps an Apple profile UUID back to our profile id.
func (s *Store) DEPProfileIDByUUID(uuid string) int64 {
	var id int64
	_ = s.db.QueryRow(`SELECT dep_profile_id FROM dep_profile_uploads WHERE profile_uuid=?`, uuid).Scan(&id)
	return id
}

// DEPDevice is a device assigned to one of our ADE servers in Apple Business Manager.
type DEPDevice struct {
	SerialNumber       string `json:"serial_number"`
	DEPServerID        int64  `json:"dep_server_id"`
	Model              string `json:"model"`
	Description        string `json:"description"`
	Color              string `json:"color"`
	OS                 string `json:"os"`
	DeviceFamily       string `json:"device_family"`
	AssetTag           string `json:"asset_tag"`
	ProfileStatus      string `json:"profile_status"`
	ProfileUUID        string `json:"profile_uuid"`
	ProfileAssignTime  string `json:"profile_assign_time"`
	ProfilePushTime    string `json:"profile_push_time"`
	DeviceAssignedDate string `json:"device_assigned_date"`
	DeviceAssignedBy   string `json:"device_assigned_by"`
	OpType             string `json:"op_type"`
	OpDate             string `json:"op_date"`
	AssignedProfileID  int64  `json:"assigned_profile_id"`
	UpdatedAt          int64  `json:"updated_at"`
	EnrolledUDID       string `json:"enrolled_udid,omitempty"`
	ServerName         string `json:"server_name,omitempty"`
}

// UpsertDEPDevice stores an ADE device record.
func (s *Store) UpsertDEPDevice(d *DEPDevice) error {
	d.UpdatedAt = Now()
	_, err := s.db.Exec(`INSERT INTO dep_devices(serial_number, dep_server_id, model, description, color, os, device_family, asset_tag, profile_status, profile_uuid, profile_assign_time,
		profile_push_time, device_assigned_date, device_assigned_by, op_type, op_date, assigned_profile_id, updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(serial_number) DO UPDATE SET dep_server_id=excluded.dep_server_id, model=excluded.model, description=excluded.description, color=excluded.color, os=excluded.os,
		device_family=excluded.device_family, asset_tag=excluded.asset_tag, profile_status=excluded.profile_status, profile_uuid=excluded.profile_uuid,
		profile_assign_time=excluded.profile_assign_time, profile_push_time=excluded.profile_push_time, device_assigned_date=excluded.device_assigned_date,
		device_assigned_by=excluded.device_assigned_by, op_type=excluded.op_type, op_date=excluded.op_date, assigned_profile_id=excluded.assigned_profile_id, updated_at=excluded.updated_at`,
		d.SerialNumber, d.DEPServerID, d.Model, d.Description, d.Color, d.OS, d.DeviceFamily, d.AssetTag, d.ProfileStatus, d.ProfileUUID, d.ProfileAssignTime,
		d.ProfilePushTime, d.DeviceAssignedDate, d.DeviceAssignedBy, d.OpType, d.OpDate, d.AssignedProfileID, d.UpdatedAt)
	return err
}

// GetDEPDevice loads an ADE device by serial.
func (s *Store) GetDEPDevice(serial string) (*DEPDevice, error) {
	ds, err := s.listDEPDevices(`WHERE dd.serial_number=?`, serial)
	if err != nil {
		return nil, err
	}
	if len(ds) == 0 {
		return nil, ErrNotFound
	}
	return ds[0], nil
}

// DeleteDEPDevice removes an ADE device record.
func (s *Store) DeleteDEPDevice(serial string) error {
	_, err := s.db.Exec(`DELETE FROM dep_devices WHERE serial_number=?`, serial)
	return err
}

// ListDEPDevices lists ADE devices, optionally for one server and/or search query.
func (s *Store) ListDEPDevices(serverID int64, q string) ([]*DEPDevice, error) {
	var where []string
	var args []any
	if serverID > 0 {
		where = append(where, `dd.dep_server_id=?`)
		args = append(args, serverID)
	}
	if q != "" {
		where = append(where, `(dd.serial_number LIKE ? OR dd.description LIKE ? OR dd.model LIKE ? OR dd.asset_tag LIKE ?)`)
		like := "%" + q + "%"
		args = append(args, like, like, like, like)
	}
	cond := ""
	if len(where) > 0 {
		cond = "WHERE " + strings.Join(where, " AND ")
	}
	return s.listDEPDevices(cond, args...)
}

func (s *Store) listDEPDevices(cond string, args ...any) ([]*DEPDevice, error) {
	rows, err := s.db.Query(`SELECT dd.serial_number, dd.dep_server_id, dd.model, dd.description, dd.color, dd.os, dd.device_family, dd.asset_tag, dd.profile_status, dd.profile_uuid,
		dd.profile_assign_time, dd.profile_push_time, dd.device_assigned_date, dd.device_assigned_by, dd.op_type, dd.op_date, dd.assigned_profile_id, dd.updated_at,
		COALESCE((SELECT udid FROM devices d WHERE d.serial_number=dd.serial_number AND d.enrollment_status='enrolled' LIMIT 1),''), COALESCE(s.name,'')
		FROM dep_devices dd LEFT JOIN dep_servers s ON s.id=dd.dep_server_id `+cond+` ORDER BY dd.serial_number`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*DEPDevice{}
	for rows.Next() {
		d := &DEPDevice{}
		if err := rows.Scan(&d.SerialNumber, &d.DEPServerID, &d.Model, &d.Description, &d.Color, &d.OS, &d.DeviceFamily, &d.AssetTag, &d.ProfileStatus, &d.ProfileUUID,
			&d.ProfileAssignTime, &d.ProfilePushTime, &d.DeviceAssignedDate, &d.DeviceAssignedBy, &d.OpType, &d.OpDate, &d.AssignedProfileID, &d.UpdatedAt, &d.EnrolledUDID, &d.ServerName); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ---- VPP (Apps and Books) ----

// VPPToken is an Apps and Books content token.
type VPPToken struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	Token        string `json:"-"`
	OrgName      string `json:"org_name"`
	LocationName string `json:"location_name"`
	ExpDate      int64  `json:"exp_date"`
	LastSync     int64  `json:"last_sync"`
	LastError    string `json:"last_error"`
	CreatedAt    int64  `json:"created_at"`
	AssetCount   int    `json:"asset_count"`
}

const vppCols = `t.id, t.name, t.token, t.org_name, t.location_name, t.exp_date, t.last_sync, t.last_error, t.created_at, (SELECT COUNT(*) FROM vpp_assets a WHERE a.vpp_token_id=t.id)`

func scanVPP(r scanner) (*VPPToken, error) {
	t := &VPPToken{}
	if err := r.Scan(&t.ID, &t.Name, &t.Token, &t.OrgName, &t.LocationName, &t.ExpDate, &t.LastSync, &t.LastError, &t.CreatedAt, &t.AssetCount); err != nil {
		return nil, notFound(err)
	}
	return t, nil
}

// ListVPPTokens returns VPP tokens.
func (s *Store) ListVPPTokens() ([]*VPPToken, error) {
	rows, err := s.db.Query(`SELECT ` + vppCols + ` FROM vpp_tokens t ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*VPPToken{}
	for rows.Next() {
		t, err := scanVPP(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetVPPToken loads a VPP token.
func (s *Store) GetVPPToken(id int64) (*VPPToken, error) {
	return scanVPP(s.db.QueryRow(`SELECT `+vppCols+` FROM vpp_tokens t WHERE t.id=?`, id))
}

// CreateVPPToken inserts a VPP token.
func (s *Store) CreateVPPToken(t *VPPToken) error {
	t.CreatedAt = Now()
	res, err := s.db.Exec(`INSERT INTO vpp_tokens(name, token, org_name, location_name, exp_date, created_at) VALUES(?,?,?,?,?,?)`, t.Name, t.Token, t.OrgName, t.LocationName, t.ExpDate, t.CreatedAt)
	if err != nil {
		return err
	}
	t.ID, _ = res.LastInsertId()
	return nil
}

// UpdateVPPToken saves a VPP token.
func (s *Store) UpdateVPPToken(t *VPPToken) error {
	_, err := s.db.Exec(`UPDATE vpp_tokens SET name=?, token=?, org_name=?, location_name=?, exp_date=?, last_sync=?, last_error=? WHERE id=?`,
		t.Name, t.Token, t.OrgName, t.LocationName, t.ExpDate, t.LastSync, t.LastError, t.ID)
	return err
}

// DeleteVPPToken removes a VPP token and its assets.
func (s *Store) DeleteVPPToken(id int64) error {
	_, err := s.db.Exec(`DELETE FROM vpp_tokens WHERE id=?`, id)
	return err
}

// VPPAsset is a licensed app/book.
type VPPAsset struct {
	VPPTokenID       int64  `json:"vpp_token_id"`
	AdamID           string `json:"adam_id"`
	PricingParam     string `json:"pricing_param"`
	ProductType      string `json:"product_type"`
	Name             string `json:"name"`
	IconURL          string `json:"icon_url"`
	AvailableCount   int    `json:"available_count"`
	AssignedCount    int    `json:"assigned_count"`
	TotalCount       int    `json:"total_count"`
	RetiredCount     int    `json:"retired_count"`
	DeviceAssignable bool   `json:"device_assignable"`
	Revocable        bool   `json:"revocable"`
	UpdatedAt        int64  `json:"updated_at"`
}

// ReplaceVPPAssets replaces the asset list of a token.
func (s *Store) ReplaceVPPAssets(tokenID int64, assets []*VPPAsset) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM vpp_assets WHERE vpp_token_id=?`, tokenID); err != nil {
		return err
	}
	for _, a := range assets {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO vpp_assets(vpp_token_id, adam_id, pricing_param, product_type, name, icon_url, available_count, assigned_count, total_count, retired_count, device_assignable, revocable, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, tokenID, a.AdamID, a.PricingParam, a.ProductType, a.Name, a.IconURL, a.AvailableCount, a.AssignedCount, a.TotalCount, a.RetiredCount,
			boolInt(a.DeviceAssignable), boolInt(a.Revocable), Now()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListVPPAssets returns assets (all tokens when tokenID is 0).
func (s *Store) ListVPPAssets(tokenID int64) ([]*VPPAsset, error) {
	q := `SELECT vpp_token_id, adam_id, pricing_param, product_type, name, icon_url, available_count, assigned_count, total_count, retired_count, device_assignable, revocable, updated_at FROM vpp_assets`
	var args []any
	if tokenID > 0 {
		q += ` WHERE vpp_token_id=?`
		args = append(args, tokenID)
	}
	rows, err := s.db.Query(q+` ORDER BY name COLLATE NOCASE`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*VPPAsset{}
	for rows.Next() {
		a := &VPPAsset{}
		var da, rv int
		if err := rows.Scan(&a.VPPTokenID, &a.AdamID, &a.PricingParam, &a.ProductType, &a.Name, &a.IconURL, &a.AvailableCount, &a.AssignedCount, &a.TotalCount, &a.RetiredCount, &da, &rv, &a.UpdatedAt); err != nil {
			return nil, err
		}
		a.DeviceAssignable, a.Revocable = da == 1, rv == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateVPPAssetName sets display metadata for an asset.
func (s *Store) UpdateVPPAssetName(adamID, name, icon string) {
	_, _ = s.db.Exec(`UPDATE vpp_assets SET name=?, icon_url=? WHERE adam_id=?`, name, icon, adamID)
}

// ---- webhooks ----

// Webhook delivers events to an external URL.
type Webhook struct {
	ID           int64    `json:"id"`
	Name         string   `json:"name"`
	URL          string   `json:"url"`
	Secret       string   `json:"secret,omitempty"`
	Events       []string `json:"events"`
	Enabled      bool     `json:"enabled"`
	LastStatus   int      `json:"last_status"`
	LastError    string   `json:"last_error"`
	LastDelivery int64    `json:"last_delivery"`
	CreatedAt    int64    `json:"created_at"`
}

func scanWebhook(r scanner) (*Webhook, error) {
	w := &Webhook{}
	var events string
	var en int
	if err := r.Scan(&w.ID, &w.Name, &w.URL, &w.Secret, &events, &en, &w.LastStatus, &w.LastError, &w.LastDelivery, &w.CreatedAt); err != nil {
		return nil, notFound(err)
	}
	w.Events = unmarshalJSON[[]string](events)
	if w.Events == nil {
		w.Events = []string{}
	}
	w.Enabled = en == 1
	return w, nil
}

const webhookCols = `id, name, url, secret, events, enabled, last_status, last_error, last_delivery, created_at`

// ListWebhooks returns all webhooks.
func (s *Store) ListWebhooks() ([]*Webhook, error) {
	rows, err := s.db.Query(`SELECT ` + webhookCols + ` FROM webhooks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Webhook{}
	for rows.Next() {
		w, err := scanWebhook(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// GetWebhook loads a webhook.
func (s *Store) GetWebhook(id int64) (*Webhook, error) {
	return scanWebhook(s.db.QueryRow(`SELECT `+webhookCols+` FROM webhooks WHERE id=?`, id))
}

// CreateWebhook inserts a webhook.
func (s *Store) CreateWebhook(w *Webhook) error {
	w.CreatedAt = Now()
	res, err := s.db.Exec(`INSERT INTO webhooks(name, url, secret, events, enabled, created_at) VALUES(?,?,?,?,?,?)`, w.Name, w.URL, w.Secret, mustJSON(w.Events), boolInt(w.Enabled), w.CreatedAt)
	if err != nil {
		return err
	}
	w.ID, _ = res.LastInsertId()
	return nil
}

// UpdateWebhook saves a webhook.
func (s *Store) UpdateWebhook(w *Webhook) error {
	_, err := s.db.Exec(`UPDATE webhooks SET name=?, url=?, secret=?, events=?, enabled=? WHERE id=?`, w.Name, w.URL, w.Secret, mustJSON(w.Events), boolInt(w.Enabled), w.ID)
	return err
}

// RecordWebhookDelivery stores the last delivery result.
func (s *Store) RecordWebhookDelivery(id int64, status int, errText string) {
	_, _ = s.db.Exec(`UPDATE webhooks SET last_status=?, last_error=?, last_delivery=? WHERE id=?`, status, errText, Now(), id)
}

// DeleteWebhook removes a webhook.
func (s *Store) DeleteWebhook(id int64) error {
	_, err := s.db.Exec(`DELETE FROM webhooks WHERE id=?`, id)
	return err
}
