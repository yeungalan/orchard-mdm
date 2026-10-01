package store

// AccountEnrollment is an authorized account-driven enrollment: a person
// signed in through the web authentication flow and received an access token
// that the device presents to fetch its enrollment profile.
type AccountEnrollment struct {
	ID                int64   `json:"id"`
	TokenHash         string  `json:"-"`
	Mode              string  `json:"mode"` // byod (User Enrollment) | adde (account-driven device enrollment)
	UserIdentifier    string  `json:"user_identifier"`
	ManagedAppleID    string  `json:"managed_apple_id"`
	DisplayName       string  `json:"display_name"`
	AuthMethod        string  `json:"auth_method"` // code | sso
	EnrollmentTokenID int64   `json:"enrollment_token_id"`
	GroupIDs          []int64 `json:"group_ids"`
	Product           string  `json:"product"`
	OSBuild           string  `json:"os_build"`
	DeviceID          string  `json:"device_id"`
	IP                string  `json:"ip"`
	CreatedAt         int64   `json:"created_at"`
	ExpiresAt         int64   `json:"expires_at"`
	ProfileIssuedAt   int64   `json:"profile_issued_at"`
}

const accountCols = `id, token_hash, mode, user_identifier, managed_apple_id, display_name, auth_method, enrollment_token_id, group_ids, product, os_build, device_id, ip, created_at, expires_at, profile_issued_at`

func scanAccount(r scanner) (*AccountEnrollment, error) {
	a := &AccountEnrollment{}
	var groups string
	if err := r.Scan(&a.ID, &a.TokenHash, &a.Mode, &a.UserIdentifier, &a.ManagedAppleID, &a.DisplayName, &a.AuthMethod, &a.EnrollmentTokenID, &groups,
		&a.Product, &a.OSBuild, &a.DeviceID, &a.IP, &a.CreatedAt, &a.ExpiresAt, &a.ProfileIssuedAt); err != nil {
		return nil, notFound(err)
	}
	a.GroupIDs = unmarshalJSON[[]int64](groups)
	if a.GroupIDs == nil {
		a.GroupIDs = []int64{}
	}
	return a, nil
}

// CreateAccountEnrollment stores an authorized account enrollment.
func (s *Store) CreateAccountEnrollment(a *AccountEnrollment) error {
	if a.CreatedAt == 0 {
		a.CreatedAt = Now()
	}
	res, err := s.db.Exec(`INSERT INTO account_enrollments(token_hash, mode, user_identifier, managed_apple_id, display_name, auth_method, enrollment_token_id, group_ids, ip, created_at, expires_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?)`, a.TokenHash, a.Mode, a.UserIdentifier, a.ManagedAppleID, a.DisplayName, a.AuthMethod, a.EnrollmentTokenID, mustJSON(a.GroupIDs), a.IP, a.CreatedAt, a.ExpiresAt)
	if err != nil {
		return err
	}
	a.ID, _ = res.LastInsertId()
	return nil
}

// GetAccountEnrollmentByToken finds an unexpired enrollment by access-token hash.
func (s *Store) GetAccountEnrollmentByToken(hash string) (*AccountEnrollment, error) {
	return scanAccount(s.db.QueryRow(`SELECT `+accountCols+` FROM account_enrollments WHERE token_hash=? AND expires_at>?`, hash, Now()))
}

// GetAccountEnrollment loads an enrollment by id.
func (s *Store) GetAccountEnrollment(id int64) (*AccountEnrollment, error) {
	return scanAccount(s.db.QueryRow(`SELECT `+accountCols+` FROM account_enrollments WHERE id=?`, id))
}

// MarkAccountProfileIssued records that the enrollment profile was delivered.
func (s *Store) MarkAccountProfileIssued(id int64, product, build string) {
	_, _ = s.db.Exec(`UPDATE account_enrollments SET profile_issued_at=?, product=?, os_build=? WHERE id=?`, Now(), product, build, id)
}

// SetAccountEnrollmentDevice links an enrollment to the device that used it.
func (s *Store) SetAccountEnrollmentDevice(id int64, deviceID string) {
	_, _ = s.db.Exec(`UPDATE account_enrollments SET device_id=? WHERE id=?`, deviceID, id)
}

// ListAccountEnrollments returns recent account sign-ins, newest first.
func (s *Store) ListAccountEnrollments(limit int) ([]*AccountEnrollment, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT `+accountCols+` FROM account_enrollments ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AccountEnrollment{}
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// PruneAccountEnrollments removes stale sign-ins that never produced a device.
func (s *Store) PruneAccountEnrollments(maxAge int64) {
	_, _ = s.db.Exec(`DELETE FROM account_enrollments WHERE device_id='' AND created_at<?`, Now()-maxAge)
}
