package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"strconv"
)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GetSetting returns a setting value or def when unset.
func (s *Store) GetSetting(key, def string) string {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

// GetSettingInt returns an integer setting.
func (s *Store) GetSettingInt(key string, def int) int {
	v := s.GetSetting(key, "")
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// GetSettingBool returns a boolean setting.
func (s *Store) GetSettingBool(key string, def bool) bool {
	v := s.GetSetting(key, "")
	if v == "" {
		return def
	}
	return v == "1" || v == "true"
}

// SetSetting stores a setting.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

// DeleteSetting removes a setting.
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key=?`, key)
	return err
}

// AllSettings returns every stored setting.
func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// GetSettingJSON decodes a JSON setting into v. Returns false when unset.
func (s *Store) GetSettingJSON(key string, v any) bool {
	raw := s.GetSetting(key, "")
	if raw == "" {
		return false
	}
	return json.Unmarshal([]byte(raw), v) == nil
}

// SetSettingJSON stores v as JSON.
func (s *Store) SetSettingJSON(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.SetSetting(key, string(b))
}

// KeyPair is a stored certificate and/or private key.
type KeyPair struct {
	Name      string `json:"name"`
	CertPEM   string `json:"cert_pem"`
	KeyPEM    string `json:"-"`
	Meta      string `json:"meta"`
	CreatedAt int64  `json:"created_at"`
}

// GetKeyPair loads a named keypair.
func (s *Store) GetKeyPair(name string) (*KeyPair, error) {
	kp := &KeyPair{Name: name}
	err := s.db.QueryRow(`SELECT cert_pem, key_pem, meta, created_at FROM keypairs WHERE name=?`, name).
		Scan(&kp.CertPEM, &kp.KeyPEM, &kp.Meta, &kp.CreatedAt)
	if err != nil {
		return nil, notFound(err)
	}
	return kp, nil
}

// PutKeyPair upserts a named keypair.
func (s *Store) PutKeyPair(kp *KeyPair) error {
	if kp.CreatedAt == 0 {
		kp.CreatedAt = Now()
	}
	_, err := s.db.Exec(`INSERT INTO keypairs(name, cert_pem, key_pem, meta, created_at) VALUES(?,?,?,?,?)
		ON CONFLICT(name) DO UPDATE SET cert_pem=excluded.cert_pem, key_pem=excluded.key_pem, meta=excluded.meta, created_at=excluded.created_at`,
		kp.Name, kp.CertPEM, kp.KeyPEM, kp.Meta, kp.CreatedAt)
	return err
}

// DeleteKeyPair removes a named keypair.
func (s *Store) DeleteKeyPair(name string) error {
	_, err := s.db.Exec(`DELETE FROM keypairs WHERE name=?`, name)
	return err
}

// ---- users / sessions / api keys ----

// User is an administrator account.
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	DisplayName  string `json:"display_name"`
	Email        string `json:"email"`
	PasswordHash string `json:"-"`
	Role         string `json:"role"`
	Disabled     bool   `json:"disabled"`
	CreatedAt    int64  `json:"created_at"`
	LastLogin    int64  `json:"last_login"`
}

const userCols = `id, username, display_name, email, password_hash, role, disabled, created_at, last_login`

func scanUser(r scanner) (*User, error) {
	u := &User{}
	var disabled int
	if err := r.Scan(&u.ID, &u.Username, &u.DisplayName, &u.Email, &u.PasswordHash, &u.Role, &disabled, &u.CreatedAt, &u.LastLogin); err != nil {
		return nil, notFound(err)
	}
	u.Disabled = disabled == 1
	return u, nil
}

// CountUsers returns the number of admin users.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// CreateUser inserts a user.
func (s *Store) CreateUser(u *User) error {
	u.CreatedAt = Now()
	res, err := s.db.Exec(`INSERT INTO users(username, display_name, email, password_hash, role, disabled, created_at) VALUES(?,?,?,?,?,?,?)`,
		u.Username, u.DisplayName, u.Email, u.PasswordHash, u.Role, boolInt(u.Disabled), u.CreatedAt)
	if err != nil {
		return err
	}
	u.ID, _ = res.LastInsertId()
	return nil
}

// UpdateUser saves profile fields (not the password unless PasswordHash is set).
func (s *Store) UpdateUser(u *User) error {
	_, err := s.db.Exec(`UPDATE users SET username=?, display_name=?, email=?, role=?, disabled=?, password_hash=? WHERE id=?`,
		u.Username, u.DisplayName, u.Email, u.Role, boolInt(u.Disabled), u.PasswordHash, u.ID)
	return err
}

// TouchUserLogin records a login.
func (s *Store) TouchUserLogin(id int64) {
	_, _ = s.db.Exec(`UPDATE users SET last_login=? WHERE id=?`, Now(), id)
}

// GetUser fetches by id.
func (s *Store) GetUser(id int64) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, id))
}

// GetUserByName fetches by username.
func (s *Store) GetUserByName(name string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username=?`, name))
}

// ListUsers returns all users.
func (s *Store) ListUsers() ([]*User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteUser removes a user.
func (s *Store) DeleteUser(id int64) error {
	_, err := s.db.Exec(`DELETE FROM users WHERE id=?`, id)
	return err
}

// CountAdmins returns the number of enabled admin-role users.
func (s *Store) CountAdmins() int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='admin' AND disabled=0`).Scan(&n)
	return n
}

// Session is a logged-in browser session.
type Session struct {
	TokenHash string
	UserID    int64
	CSRF      string
	IP        string
	UserAgent string
	CreatedAt int64
	ExpiresAt int64
}

// CreateSession stores a session.
func (s *Store) CreateSession(ss *Session) error {
	_, err := s.db.Exec(`INSERT INTO sessions(token_hash, user_id, csrf, ip, user_agent, created_at, expires_at) VALUES(?,?,?,?,?,?,?)`,
		ss.TokenHash, ss.UserID, ss.CSRF, ss.IP, ss.UserAgent, ss.CreatedAt, ss.ExpiresAt)
	return err
}

// GetSession loads an unexpired session.
func (s *Store) GetSession(tokenHash string) (*Session, error) {
	ss := &Session{TokenHash: tokenHash}
	err := s.db.QueryRow(`SELECT user_id, csrf, ip, user_agent, created_at, expires_at FROM sessions WHERE token_hash=? AND expires_at>?`, tokenHash, Now()).
		Scan(&ss.UserID, &ss.CSRF, &ss.IP, &ss.UserAgent, &ss.CreatedAt, &ss.ExpiresAt)
	if err != nil {
		return nil, notFound(err)
	}
	return ss, nil
}

// ExtendSession pushes the expiry forward.
func (s *Store) ExtendSession(tokenHash string, expires int64) {
	_, _ = s.db.Exec(`UPDATE sessions SET expires_at=? WHERE token_hash=?`, expires, tokenHash)
}

// DeleteSession removes a session.
func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash=?`, tokenHash)
	return err
}

// DeleteUserSessions removes every session of a user.
func (s *Store) DeleteUserSessions(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id=?`, userID)
	return err
}

// APIKey is a token used for automation.
type APIKey struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Prefix    string `json:"prefix"`
	KeyHash   string `json:"-"`
	Role      string `json:"role"`
	CreatedBy string `json:"created_by"`
	CreatedAt int64  `json:"created_at"`
	LastUsed  int64  `json:"last_used"`
	ExpiresAt int64  `json:"expires_at"`
}

const apiKeyCols = `id, name, prefix, key_hash, role, created_by, created_at, last_used, expires_at`

func scanAPIKey(r scanner) (*APIKey, error) {
	k := &APIKey{}
	if err := r.Scan(&k.ID, &k.Name, &k.Prefix, &k.KeyHash, &k.Role, &k.CreatedBy, &k.CreatedAt, &k.LastUsed, &k.ExpiresAt); err != nil {
		return nil, notFound(err)
	}
	return k, nil
}

// CreateAPIKey stores a key.
func (s *Store) CreateAPIKey(k *APIKey) error {
	k.CreatedAt = Now()
	res, err := s.db.Exec(`INSERT INTO api_keys(name, prefix, key_hash, role, created_by, created_at, expires_at) VALUES(?,?,?,?,?,?,?)`,
		k.Name, k.Prefix, k.KeyHash, k.Role, k.CreatedBy, k.CreatedAt, k.ExpiresAt)
	if err != nil {
		return err
	}
	k.ID, _ = res.LastInsertId()
	return nil
}

// GetAPIKeyByHash looks up a key.
func (s *Store) GetAPIKeyByHash(hash string) (*APIKey, error) {
	return scanAPIKey(s.db.QueryRow(`SELECT `+apiKeyCols+` FROM api_keys WHERE key_hash=?`, hash))
}

// TouchAPIKey records usage.
func (s *Store) TouchAPIKey(id int64) {
	_, _ = s.db.Exec(`UPDATE api_keys SET last_used=? WHERE id=?`, Now(), id)
}

// ListAPIKeys returns all keys.
func (s *Store) ListAPIKeys() ([]*APIKey, error) {
	rows, err := s.db.Query(`SELECT ` + apiKeyCols + ` FROM api_keys ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// DeleteAPIKey removes a key.
func (s *Store) DeleteAPIKey(id int64) error {
	_, err := s.db.Exec(`DELETE FROM api_keys WHERE id=?`, id)
	return err
}

// CleanupExpired deletes expired sessions and stale SCEP challenges.
func (s *Store) CleanupExpired() {
	now := Now()
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE expires_at<?`, now)
	_, _ = s.db.Exec(`DELETE FROM scep_challenges WHERE expires_at<?`, now-86400)
}

var _ = sql.ErrNoRows
