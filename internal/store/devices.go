package store

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Device is an enrolled (or previously enrolled) Apple device.
type Device struct {
	UDID                  string         `json:"udid"`
	SerialNumber          string         `json:"serial_number"`
	IMEI                  string         `json:"imei"`
	MEID                  string         `json:"meid"`
	DeviceName            string         `json:"device_name"`
	Model                 string         `json:"model"`
	ModelName             string         `json:"model_name"`
	ProductName           string         `json:"product_name"`
	OSVersion             string         `json:"os_version"`
	BuildVersion          string         `json:"build_version"`
	EnrollmentStatus      string         `json:"enrollment_status"`
	EnrollmentType        string         `json:"enrollment_type"`
	Ownership             string         `json:"ownership"`
	EnrollmentTokenID     int64          `json:"enrollment_token_id"`
	EnrolledAt            int64          `json:"enrolled_at"`
	UnenrolledAt          int64          `json:"unenrolled_at"`
	LastSeen              int64          `json:"last_seen"`
	LastInventory         int64          `json:"last_inventory"`
	LastPush              int64          `json:"last_push"`
	Topic                 string         `json:"topic"`
	PushToken             string         `json:"-"`
	PushMagic             string         `json:"-"`
	UnlockToken           []byte         `json:"-"`
	BootstrapToken        []byte         `json:"-"`
	CertFingerprint       string         `json:"cert_fingerprint"`
	CertNotAfter          int64          `json:"cert_not_after"`
	Supervised            bool           `json:"supervised"`
	DEPEnrolled           bool           `json:"dep_enrolled"`
	AwaitingConfiguration bool           `json:"awaiting_configuration"`
	ActivationLock        bool           `json:"activation_lock"`
	FindMy                bool           `json:"find_my"`
	PasscodePresent       bool           `json:"passcode_present"`
	PasscodeCompliant     bool           `json:"passcode_compliant"`
	EncryptionCaps        int            `json:"encryption_caps"`
	LostMode              bool           `json:"lost_mode"`
	BatteryLevel          float64        `json:"battery_level"`
	CapacityGB            float64        `json:"capacity_gb"`
	AvailableGB           float64        `json:"available_gb"`
	WiFiMAC               string         `json:"wifi_mac"`
	BluetoothMAC          string         `json:"bluetooth_mac"`
	PhoneNumber           string         `json:"phone_number"`
	Carrier               string         `json:"carrier"`
	ICCID                 string         `json:"iccid"`
	Compliance            string         `json:"compliance"`
	ComplianceReasons     []string       `json:"compliance_reasons"`
	NoncompliantSince     int64          `json:"noncompliant_since"`
	PortalToken           string         `json:"-"`
	Tags                  []string       `json:"tags"`
	Notes                 string         `json:"notes"`
	AssignedUser          string         `json:"assigned_user"`
	AssignedEmail         string         `json:"assigned_email"`
	AssetTag              string         `json:"asset_tag"`
	ActivationLockBypass  string         `json:"-"`
	Location              map[string]any `json:"location,omitempty"`
	LocationAt            int64          `json:"location_at"`
	Info                  map[string]any `json:"info,omitempty"`
	Security              map[string]any `json:"security,omitempty"`
	Restrictions          map[string]any `json:"restrictions,omitempty"`
	OSUpdates             []any          `json:"os_updates,omitempty"`
	OSUpdateStatus        []any          `json:"os_update_status,omitempty"`
	ProvisioningProfiles  []any          `json:"provisioning_profiles,omitempty"`
	ManagedMedia          map[string]any `json:"managed_media,omitempty"`
	DDMStatus             map[string]any `json:"ddm_status,omitempty"`
	DDMToken              string         `json:"ddm_token"`
	DDMLastStatus         int64          `json:"ddm_last_status"`
	LastIP                string         `json:"last_ip"`
	SSID                  string         `json:"ssid"`
	BSSID                 string         `json:"bssid"`
	LocalIP               string         `json:"local_ip"`
	BatteryState          string         `json:"battery_state"`
	BatteryHealth         string         `json:"battery_health"`
	CellularTechnology    string         `json:"cellular_technology"`
	Roaming               bool           `json:"roaming"`
	Hotspot               bool           `json:"hotspot"`
	AgentToken            string         `json:"-"`
	AgentLastSeen         int64          `json:"agent_last_seen"`
	ManagedAppleID        string         `json:"managed_apple_id"`
	UserEnrollment        bool           `json:"user_enrollment"`
	AccountEnrollmentID   int64          `json:"account_enrollment_id"`
	TelemetryAt           int64          `json:"telemetry_at"`
	CreatedAt             int64          `json:"created_at"`
	UpdatedAt             int64          `json:"updated_at"`

	// HasPushToken is derived for API consumers.
	HasPushToken bool `json:"has_push_token"`
}

const deviceCols = `udid, serial_number, imei, meid, device_name, model, model_name, product_name, os_version, build_version,
	enrollment_status, enrollment_type, ownership, enrollment_token_id, enrolled_at, unenrolled_at, last_seen, last_inventory, last_push,
	topic, push_token, push_magic, unlock_token, bootstrap_token, cert_fingerprint, cert_not_after,
	supervised, dep_enrolled, awaiting_configuration, activation_lock, find_my, passcode_present, passcode_compliant, encryption_caps, lost_mode,
	battery_level, capacity_gb, available_gb, wifi_mac, bluetooth_mac, phone_number, carrier, iccid,
	compliance, compliance_reasons, noncompliant_since, portal_token, tags, notes, assigned_user, assigned_email, asset_tag, activation_lock_bypass,
	location_json, location_at, info_json, security_json, restrictions_json, os_updates_json, os_update_status_json, provisioning_json, media_json,
	ddm_status_json, ddm_token, ddm_last_status, last_ip, ssid, bssid, local_ip, battery_state, battery_health, cellular_technology, roaming, hotspot,
	agent_token, agent_last_seen, telemetry_at, managed_apple_id, user_enrollment, account_enrollment_id, created_at, updated_at`

func scanDevice(r scanner) (*Device, error) {
	d := &Device{}
	var supervised, dep, awaiting, alock, findmy, pp, pc, lost, roaming, hotspot, ue int
	var reasons, tags, loc, info, sec, restr, osu, osus, prov, media, ddm string
	err := r.Scan(&d.UDID, &d.SerialNumber, &d.IMEI, &d.MEID, &d.DeviceName, &d.Model, &d.ModelName, &d.ProductName, &d.OSVersion, &d.BuildVersion,
		&d.EnrollmentStatus, &d.EnrollmentType, &d.Ownership, &d.EnrollmentTokenID, &d.EnrolledAt, &d.UnenrolledAt, &d.LastSeen, &d.LastInventory, &d.LastPush,
		&d.Topic, &d.PushToken, &d.PushMagic, &d.UnlockToken, &d.BootstrapToken, &d.CertFingerprint, &d.CertNotAfter,
		&supervised, &dep, &awaiting, &alock, &findmy, &pp, &pc, &d.EncryptionCaps, &lost,
		&d.BatteryLevel, &d.CapacityGB, &d.AvailableGB, &d.WiFiMAC, &d.BluetoothMAC, &d.PhoneNumber, &d.Carrier, &d.ICCID,
		&d.Compliance, &reasons, &d.NoncompliantSince, &d.PortalToken, &tags, &d.Notes, &d.AssignedUser, &d.AssignedEmail, &d.AssetTag, &d.ActivationLockBypass,
		&loc, &d.LocationAt, &info, &sec, &restr, &osu, &osus, &prov, &media,
		&ddm, &d.DDMToken, &d.DDMLastStatus, &d.LastIP, &d.SSID, &d.BSSID, &d.LocalIP, &d.BatteryState, &d.BatteryHealth, &d.CellularTechnology, &roaming, &hotspot,
		&d.AgentToken, &d.AgentLastSeen, &d.TelemetryAt, &d.ManagedAppleID, &ue, &d.AccountEnrollmentID, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	d.Supervised, d.DEPEnrolled, d.AwaitingConfiguration = supervised == 1, dep == 1, awaiting == 1
	d.ActivationLock, d.FindMy, d.PasscodePresent, d.PasscodeCompliant, d.LostMode = alock == 1, findmy == 1, pp == 1, pc == 1, lost == 1
	d.Roaming, d.Hotspot, d.UserEnrollment = roaming == 1, hotspot == 1, ue == 1
	d.ComplianceReasons = unmarshalJSON[[]string](reasons)
	d.Tags = splitTags(tags)
	d.Location = unmarshalJSON[map[string]any](loc)
	d.Info = unmarshalJSON[map[string]any](info)
	d.Security = unmarshalJSON[map[string]any](sec)
	d.Restrictions = unmarshalJSON[map[string]any](restr)
	d.OSUpdates = unmarshalJSON[[]any](osu)
	d.OSUpdateStatus = unmarshalJSON[[]any](osus)
	d.ProvisioningProfiles = unmarshalJSON[[]any](prov)
	d.ManagedMedia = unmarshalJSON[map[string]any](media)
	d.DDMStatus = unmarshalJSON[map[string]any](ddm)
	d.HasPushToken = d.PushToken != "" && d.PushMagic != ""
	return d, nil
}

func splitTags(s string) []string {
	out := []string{}
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// JoinTags normalises a tag list for storage.
func JoinTags(tags []string) string {
	var clean []string
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.TrimSpace(strings.ReplaceAll(t, ",", " "))
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		clean = append(clean, t)
	}
	return strings.Join(clean, ",")
}

// GetDevice loads a device by UDID.
func (s *Store) GetDevice(udid string) (*Device, error) {
	return scanDevice(s.db.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE udid=?`, udid))
}

// GetDeviceByPortalToken loads a device by its self-service portal token.
func (s *Store) GetDeviceByPortalToken(tok string) (*Device, error) {
	if tok == "" {
		return nil, ErrNotFound
	}
	return scanDevice(s.db.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE portal_token=? AND enrollment_status='enrolled'`, tok))
}

// GetDeviceByAgentToken loads an enrolled device by its companion-agent token.
func (s *Store) GetDeviceByAgentToken(tok string) (*Device, error) {
	if len(tok) < 16 {
		return nil, ErrNotFound
	}
	return scanDevice(s.db.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE agent_token=? AND enrollment_status='enrolled'`, tok))
}

// GetDeviceBySerial loads the most recently seen device record with a serial number.
func (s *Store) GetDeviceBySerial(serial string) (*Device, error) {
	return scanDevice(s.db.QueryRow(`SELECT `+deviceCols+` FROM devices WHERE serial_number=? ORDER BY updated_at DESC LIMIT 1`, serial))
}

// EnsureDevice creates a device row if missing.
func (s *Store) EnsureDevice(udid string) error {
	now := Now()
	_, err := s.db.Exec(`INSERT INTO devices(udid, portal_token, agent_token, created_at, updated_at) VALUES(?,?,?,?,?) ON CONFLICT(udid) DO NOTHING`,
		udid, randomHex(24), randomHex(24), now, now)
	return err
}

// deviceWritable lists the columns that UpdateDevice may modify.
var deviceWritable = map[string]bool{}

func init() {
	for _, c := range strings.Split(deviceCols, ",") {
		c = strings.TrimSpace(c)
		if c != "udid" && c != "created_at" && c != "updated_at" {
			deviceWritable[c] = true
		}
	}
}

// UpdateDevice sets the given columns on a device. Values that are maps, slices
// (other than []byte) or structs are stored as JSON; bools become 0/1.
func (s *Store) UpdateDevice(udid string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var sets []string
	var args []any
	for k, v := range fields {
		if !deviceWritable[k] {
			return fmt.Errorf("store: column %q is not writable", k)
		}
		sets = append(sets, k+"=?")
		args = append(args, normaliseValue(v))
	}
	sets = append(sets, "updated_at=?")
	args = append(args, Now(), udid)
	res, err := s.db.Exec(`UPDATE devices SET `+strings.Join(sets, ", ")+` WHERE udid=?`, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func normaliseValue(v any) any {
	switch t := v.(type) {
	case nil:
		return ""
	case bool:
		return boolInt(t)
	case []byte, string, int, int64, int32, float64, float32, uint64, uint32:
		return t
	case []string:
		if t == nil {
			return ""
		}
		b, _ := json.Marshal(t)
		return string(b)
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// TouchDevice records the last time a device talked to us.
func (s *Store) TouchDevice(udid string) {
	_, _ = s.db.Exec(`UPDATE devices SET last_seen=? WHERE udid=?`, Now(), udid)
}

// DeleteDevice removes a device and everything attached to it.
func (s *Store) DeleteDevice(udid string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM commands WHERE device_id=?`,
		`DELETE FROM events WHERE device_id=?`,
		`DELETE FROM devices WHERE udid=?`,
	} {
		if _, err := tx.Exec(q, udid); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DeviceFilter narrows ListDevices.
type DeviceFilter struct {
	Query          string
	Status         string // enrolled|unenrolled|pending|"" (any)
	Compliance     string
	GroupID        int64
	Model          string
	OSVersion      string
	Ownership      string
	EnrollmentType string
	Supervised     string // "1" / "0" / ""
	UDIDs          []string
	Sort           string
	Desc           bool
	Limit          int
	Offset         int
}

var deviceSortable = map[string]string{
	"name": "device_name COLLATE NOCASE", "serial": "serial_number", "os": "os_version", "model": "product_name",
	"last_seen": "last_seen", "enrolled_at": "enrolled_at", "compliance": "compliance", "battery": "battery_level", "user": "assigned_user",
}

// ListDevices returns devices matching the filter and the total match count.
func (s *Store) ListDevices(f DeviceFilter) ([]*Device, int, error) {
	var where []string
	var args []any
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + q + "%"
		where = append(where, `(device_name LIKE ? OR serial_number LIKE ? OR udid LIKE ? OR product_name LIKE ? OR model_name LIKE ? OR assigned_user LIKE ? OR assigned_email LIKE ? OR imei LIKE ? OR phone_number LIKE ? OR tags LIKE ? OR asset_tag LIKE ? OR managed_apple_id LIKE ?)`)
		for i := 0; i < 12; i++ {
			args = append(args, like)
		}
	}
	if f.Status != "" {
		where = append(where, `enrollment_status=?`)
		args = append(args, f.Status)
	}
	if f.Compliance != "" {
		where = append(where, `compliance=?`)
		args = append(args, f.Compliance)
	}
	if f.GroupID > 0 {
		where = append(where, `udid IN (SELECT device_id FROM group_members WHERE group_id=?)`)
		args = append(args, f.GroupID)
	}
	if f.Model != "" {
		where = append(where, `(product_name=? OR model_name=?)`)
		args = append(args, f.Model, f.Model)
	}
	if f.OSVersion != "" {
		where = append(where, `os_version LIKE ?`)
		args = append(args, f.OSVersion+"%")
	}
	if f.EnrollmentType != "" {
		where = append(where, `enrollment_type=?`)
		args = append(args, f.EnrollmentType)
	}
	if f.Ownership != "" {
		where = append(where, `ownership=?`)
		args = append(args, f.Ownership)
	}
	if f.Supervised == "1" || f.Supervised == "0" {
		where = append(where, `supervised=?`)
		args = append(args, f.Supervised)
	}
	if len(f.UDIDs) > 0 {
		where = append(where, `udid IN (`+placeholders(len(f.UDIDs))+`)`)
		args = append(args, stringsToAny(f.UDIDs)...)
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM devices`+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "last_seen"
	if col, ok := deviceSortable[f.Sort]; ok {
		order = col
	}
	dir := " DESC"
	if f.Sort != "" && !f.Desc {
		dir = " ASC"
	}
	q := `SELECT ` + deviceCols + ` FROM devices` + cond + ` ORDER BY ` + order + dir + `, udid`
	if f.Limit > 0 {
		q += fmt.Sprintf(" LIMIT %d OFFSET %d", f.Limit, f.Offset)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*Device
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, d)
	}
	return out, total, rows.Err()
}

// EnrolledDevices returns all currently enrolled devices.
func (s *Store) EnrolledDevices() ([]*Device, error) {
	ds, _, err := s.ListDevices(DeviceFilter{Status: "enrolled"})
	return ds, err
}

// EnrolledUDIDs returns all enrolled device identifiers.
func (s *Store) EnrolledUDIDs() ([]string, error) {
	rows, err := s.db.Query(`SELECT udid FROM devices WHERE enrollment_status='enrolled'`)
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

// ---- inventory sub-tables ----

// DeviceApp is an installed application reported by a device.
type DeviceApp struct {
	BundleID      string `json:"bundle_id"`
	Name          string `json:"name"`
	Version       string `json:"version"`
	ShortVersion  string `json:"short_version"`
	BundleSize    int64  `json:"bundle_size"`
	DynamicSize   int64  `json:"dynamic_size"`
	IsManaged     bool   `json:"is_managed"`
	ManagedStatus string `json:"managed_status"`
	HasConfig     bool   `json:"has_config"`
}

// ReplaceDeviceApps replaces the installed-app inventory of a device, keeping
// managed-state information that came from ManagedApplicationList.
func (s *Store) ReplaceDeviceApps(udid string, apps []DeviceApp) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	managed := map[string][2]string{}
	rows, err := tx.Query(`SELECT bundle_id, is_managed, managed_status FROM device_apps WHERE device_id=?`, udid)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b, st string
		var m int
		if err := rows.Scan(&b, &m, &st); err == nil {
			managed[b] = [2]string{fmt.Sprint(m), st}
		}
	}
	rows.Close()
	if _, err := tx.Exec(`DELETE FROM device_apps WHERE device_id=?`, udid); err != nil {
		return err
	}
	for _, a := range apps {
		if a.BundleID == "" {
			continue
		}
		isManaged, status := boolInt(a.IsManaged), a.ManagedStatus
		if prev, ok := managed[a.BundleID]; ok && !a.IsManaged && status == "" {
			if prev[0] == "1" {
				isManaged = 1
			}
			status = prev[1]
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO device_apps(device_id, bundle_id, name, version, short_version, bundle_size, dynamic_size, is_managed, managed_status, has_config)
			VALUES(?,?,?,?,?,?,?,?,?,?)`, udid, a.BundleID, a.Name, a.Version, a.ShortVersion, a.BundleSize, a.DynamicSize, isManaged, status, boolInt(a.HasConfig)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetManagedApps updates the managed flag/status of apps on a device.
func (s *Store) SetManagedApps(udid string, statuses map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE device_apps SET is_managed=0, managed_status='' WHERE device_id=?`, udid); err != nil {
		return err
	}
	for bundle, status := range statuses {
		res, err := tx.Exec(`UPDATE device_apps SET is_managed=1, managed_status=? WHERE device_id=? AND bundle_id=?`, status, udid, bundle)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			if _, err := tx.Exec(`INSERT INTO device_apps(device_id, bundle_id, name, is_managed, managed_status) VALUES(?,?,?,?,?)`, udid, bundle, bundle, 1, status); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// ListDeviceApps returns the app inventory of a device.
func (s *Store) ListDeviceApps(udid string) ([]DeviceApp, error) {
	rows, err := s.db.Query(`SELECT bundle_id, name, version, short_version, bundle_size, dynamic_size, is_managed, managed_status, has_config FROM device_apps WHERE device_id=? ORDER BY name COLLATE NOCASE`, udid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceApp{}
	for rows.Next() {
		var a DeviceApp
		var m, c int
		if err := rows.Scan(&a.BundleID, &a.Name, &a.Version, &a.ShortVersion, &a.BundleSize, &a.DynamicSize, &m, &a.ManagedStatus, &c); err != nil {
			return nil, err
		}
		a.IsManaged, a.HasConfig = m == 1, c == 1
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeviceAppBundleIDs returns the set of installed bundle IDs on a device.
func (s *Store) DeviceAppBundleIDs(udid string) (map[string]DeviceApp, error) {
	apps, err := s.ListDeviceApps(udid)
	if err != nil {
		return nil, err
	}
	out := make(map[string]DeviceApp, len(apps))
	for _, a := range apps {
		out[a.BundleID] = a
	}
	return out, nil
}

// AppInstallCounts returns, for each bundle ID, how many enrolled devices report it installed.
func (s *Store) AppInstallCounts() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT a.bundle_id, COUNT(*) FROM device_apps a JOIN devices d ON d.udid=a.device_id WHERE d.enrollment_status='enrolled' GROUP BY a.bundle_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var b string
		var n int
		if err := rows.Scan(&b, &n); err != nil {
			return nil, err
		}
		out[b] = n
	}
	return out, rows.Err()
}

// DeviceProfile is a configuration profile reported by a device.
type DeviceProfile struct {
	Identifier        string   `json:"identifier"`
	DisplayName       string   `json:"display_name"`
	Organization      string   `json:"organization"`
	Description       string   `json:"description"`
	UUID              string   `json:"uuid"`
	Version           int64    `json:"version"`
	IsManaged         bool     `json:"is_managed"`
	IsEncrypted       bool     `json:"is_encrypted"`
	RemovalDisallowed bool     `json:"removal_disallowed"`
	PayloadTypes      []string `json:"payload_types"`
}

// ReplaceDeviceProfiles replaces the profile inventory of a device.
func (s *Store) ReplaceDeviceProfiles(udid string, profiles []DeviceProfile) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM device_profiles WHERE device_id=?`, udid); err != nil {
		return err
	}
	for _, p := range profiles {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO device_profiles(device_id, identifier, display_name, organization, description, uuid, version, is_managed, is_encrypted, removal_disallowed, payload_types)
			VALUES(?,?,?,?,?,?,?,?,?,?,?)`, udid, p.Identifier, p.DisplayName, p.Organization, p.Description, p.UUID, p.Version,
			boolInt(p.IsManaged), boolInt(p.IsEncrypted), boolInt(p.RemovalDisallowed), mustJSON(p.PayloadTypes)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListDeviceProfiles returns the profiles installed on a device.
func (s *Store) ListDeviceProfiles(udid string) ([]DeviceProfile, error) {
	rows, err := s.db.Query(`SELECT identifier, display_name, organization, description, uuid, version, is_managed, is_encrypted, removal_disallowed, payload_types FROM device_profiles WHERE device_id=? ORDER BY display_name COLLATE NOCASE`, udid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceProfile{}
	for rows.Next() {
		var p DeviceProfile
		var m, e, r int
		var pt string
		if err := rows.Scan(&p.Identifier, &p.DisplayName, &p.Organization, &p.Description, &p.UUID, &p.Version, &m, &e, &r, &pt); err != nil {
			return nil, err
		}
		p.IsManaged, p.IsEncrypted, p.RemovalDisallowed = m == 1, e == 1, r == 1
		p.PayloadTypes = unmarshalJSON[[]string](pt)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeviceCertificate is a certificate reported by a device.
type DeviceCertificate struct {
	SHA256     string `json:"sha256"`
	CommonName string `json:"common_name"`
	Subject    string `json:"subject"`
	Issuer     string `json:"issuer"`
	IsIdentity bool   `json:"is_identity"`
	NotBefore  int64  `json:"not_before"`
	NotAfter   int64  `json:"not_after"`
}

// ReplaceDeviceCertificates replaces the certificate inventory of a device.
func (s *Store) ReplaceDeviceCertificates(udid string, certs []DeviceCertificate) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM device_certificates WHERE device_id=?`, udid); err != nil {
		return err
	}
	for _, c := range certs {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO device_certificates(device_id, sha256, common_name, subject, issuer, is_identity, not_before, not_after) VALUES(?,?,?,?,?,?,?,?)`,
			udid, c.SHA256, c.CommonName, c.Subject, c.Issuer, boolInt(c.IsIdentity), c.NotBefore, c.NotAfter); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ListDeviceCertificates returns a device's certificates.
func (s *Store) ListDeviceCertificates(udid string) ([]DeviceCertificate, error) {
	rows, err := s.db.Query(`SELECT sha256, common_name, subject, issuer, is_identity, not_before, not_after FROM device_certificates WHERE device_id=? ORDER BY common_name COLLATE NOCASE`, udid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceCertificate{}
	for rows.Next() {
		var c DeviceCertificate
		var id int
		if err := rows.Scan(&c.SHA256, &c.CommonName, &c.Subject, &c.Issuer, &id, &c.NotBefore, &c.NotAfter); err != nil {
			return nil, err
		}
		c.IsIdentity = id == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountBy returns counts of enrolled devices grouped by a column.
func (s *Store) CountBy(column string) (map[string]int, error) {
	allowed := map[string]bool{"os_version": true, "product_name": true, "compliance": true, "ownership": true, "enrollment_type": true, "model_name": true, "supervised": true}
	if !allowed[column] {
		return nil, fmt.Errorf("store: cannot group by %s", column)
	}
	rows, err := s.db.Query(`SELECT ` + column + `, COUNT(*) FROM devices WHERE enrollment_status='enrolled' GROUP BY ` + column)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var k string
		var n int
		if err := rows.Scan(&k, &n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, rows.Err()
}

// CountDevices counts devices by enrollment status.
func (s *Store) CountDevices(status string) int {
	var n int
	if status == "" {
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM devices`).Scan(&n)
	} else {
		_ = s.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE enrollment_status=?`, status).Scan(&n)
	}
	return n
}

// DistinctModels lists product names of enrolled devices.
func (s *Store) DistinctValues(column string) ([]string, error) {
	allowed := map[string]bool{"product_name": true, "model_name": true, "os_version": true}
	if !allowed[column] {
		return nil, fmt.Errorf("store: invalid column")
	}
	rows, err := s.db.Query(`SELECT DISTINCT ` + column + ` FROM devices WHERE ` + column + `<>'' ORDER BY ` + column)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
