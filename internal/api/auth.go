package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie = "orchard_session"
	sessionTTL    = 12 * time.Hour
	apiKeyPrefix  = "orch_"
)

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// SetupToken returns the one-time code required to create the first admin.
func (a *API) SetupToken() string {
	a.setupMu.Lock()
	defer a.setupMu.Unlock()
	if a.setupToken == "" {
		a.setupToken = strings.ToUpper(randomHex(4))
	}
	return a.setupToken
}

func validatePassword(pw string) error {
	if utf8.RuneCountInString(pw) < 10 {
		return badRequest("password must be at least 10 characters")
	}
	return nil
}

// CreateUser creates an account (used for seeding and the users API).
func (a *API) CreateUser(username, password, role, display string) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return badRequest("username is required")
	}
	if _, ok := Roles[role]; !ok {
		return badRequest("unknown role %q", role)
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return a.Store.CreateUser(&store.User{Username: username, DisplayName: display, PasswordHash: string(hash), Role: role})
}

func (a *API) authenticate(r *http.Request) (*Principal, error) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		key := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if !strings.HasPrefix(key, apiKeyPrefix) {
			return nil, errors.New("invalid api key")
		}
		k, err := a.Store.GetAPIKeyByHash(sha(key))
		if err != nil {
			return nil, err
		}
		if k.ExpiresAt > 0 && k.ExpiresAt < store.Now() {
			return nil, errors.New("api key expired")
		}
		if store.Now()-k.LastUsed > 60 {
			a.Store.TouchAPIKey(k.ID)
		}
		return &Principal{Kind: "apikey", ID: k.ID, Name: k.Name, Role: k.Role}, nil
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, errors.New("no session")
	}
	hash := sha(c.Value)
	sess, err := a.Store.GetSession(hash)
	if err != nil {
		return nil, err
	}
	u, err := a.Store.GetUser(sess.UserID)
	if err != nil || u.Disabled {
		return nil, errors.New("user disabled")
	}
	if time.Until(time.Unix(sess.ExpiresAt, 0)) < sessionTTL-15*time.Minute {
		a.Store.ExtendSession(hash, time.Now().Add(sessionTTL).Unix())
	}
	return &Principal{Kind: "session", ID: u.ID, Name: u.Username, Role: u.Role, CSRF: sess.CSRF, SessionHash: hash}, nil
}

func (a *API) secureCookies(r *http.Request) bool {
	return r.TLS != nil || strings.HasPrefix(a.MDM.PublicURL(), "https://") || (a.Cfg.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

func (a *API) authStatus(w http.ResponseWriter, r *http.Request) error {
	n, err := a.Store.CountUsers()
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"setup_required": n == 0, "org_name": a.MDM.Setting(mdm.SettingOrgName), "version": a.Version})
}

func (a *API) tooManyFailures(ip string) bool {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	var recent []time.Time
	for _, t := range a.loginFails[ip] {
		if t.After(cut) {
			recent = append(recent, t)
		}
	}
	a.loginFails[ip] = recent
	return len(recent) >= 8
}

func (a *API) recordFailure(ip string) {
	a.loginMu.Lock()
	a.loginFails[ip] = append(a.loginFails[ip], time.Now())
	a.loginMu.Unlock()
}

func (a *API) startSession(w http.ResponseWriter, r *http.Request, u *store.User) (map[string]any, error) {
	tok := randomHex(32)
	csrf := randomHex(16)
	ua := r.UserAgent()
	if len(ua) > 200 {
		ua = ua[:200]
	}
	if err := a.Store.CreateSession(&store.Session{TokenHash: sha(tok), UserID: u.ID, CSRF: csrf, IP: mdm.ClientIP(r, a.Cfg.TrustProxy), UserAgent: ua,
		CreatedAt: store.Now(), ExpiresAt: time.Now().Add(sessionTTL).Unix()}); err != nil {
		return nil, err
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, Secure: a.secureCookies(r), SameSite: http.SameSiteStrictMode})
	a.Store.TouchUserLogin(u.ID)
	return map[string]any{"user": u, "csrf": csrf}, nil
}

func (a *API) login(w http.ResponseWriter, r *http.Request) error {
	ip := mdm.ClientIP(r, a.Cfg.TrustProxy)
	if a.tooManyFailures(ip) {
		return errf(http.StatusTooManyRequests, "too many failed sign-in attempts; try again in a few minutes")
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	u, err := a.Store.GetUserByName(strings.TrimSpace(req.Username))
	if err != nil || u.Disabled || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Password)) != nil {
		a.recordFailure(ip)
		_ = a.Store.InsertAudit(&store.AuditEntry{Actor: req.Username, Action: "auth.login_failed", IP: ip})
		return errf(http.StatusUnauthorized, "incorrect username or password")
	}
	out, err := a.startSession(w, r, u)
	if err != nil {
		return err
	}
	_ = a.Store.InsertAudit(&store.AuditEntry{Actor: u.Username, Action: "auth.login", IP: ip})
	return ok(w, out)
}

func (a *API) setup(w http.ResponseWriter, r *http.Request) error {
	n, err := a.Store.CountUsers()
	if err != nil {
		return err
	}
	if n > 0 {
		return errf(http.StatusConflict, "setup has already been completed")
	}
	var req struct {
		SetupCode string `json:"setup_code"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		OrgName   string `json:"org_name"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	ip := mdm.ClientIP(r, a.Cfg.TrustProxy)
	if a.tooManyFailures(ip) {
		return errf(http.StatusTooManyRequests, "too many attempts")
	}
	if subtle.ConstantTimeCompare([]byte(strings.ToUpper(strings.TrimSpace(req.SetupCode))), []byte(a.SetupToken())) != 1 {
		a.recordFailure(ip)
		return errf(http.StatusForbidden, "the setup code is incorrect (it is printed in the server log)")
	}
	if err := a.CreateUser(req.Username, req.Password, "admin", "Administrator"); err != nil {
		return err
	}
	if strings.TrimSpace(req.OrgName) != "" {
		_ = a.Store.SetSetting(mdm.SettingOrgName, strings.TrimSpace(req.OrgName))
	}
	u, err := a.Store.GetUserByName(req.Username)
	if err != nil {
		return err
	}
	out, err := a.startSession(w, r, u)
	if err != nil {
		return err
	}
	_ = a.Store.InsertAudit(&store.AuditEntry{Actor: u.Username, Action: "auth.setup", IP: ip, Details: "created first administrator"})
	return ok(w, out)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if p.SessionHash != "" {
		_ = a.Store.DeleteSession(p.SessionHash)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) me(w http.ResponseWriter, r *http.Request, p *Principal) error {
	out := map[string]any{"principal": p, "csrf": p.CSRF, "permissions": map[string]bool{
		"read": true, "act": Roles[p.Role] >= PermAct, "manage": Roles[p.Role] >= PermManage, "admin": Roles[p.Role] >= PermAdmin,
	}}
	if p.Kind == "session" {
		if u, err := a.Store.GetUser(p.ID); err == nil {
			out["user"] = u
		}
	}
	return ok(w, out)
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request, p *Principal) error {
	if p.Kind != "session" {
		return badRequest("API keys have no password")
	}
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	u, err := a.Store.GetUser(p.ID)
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(req.Current)) != nil {
		return badRequest("current password is incorrect")
	}
	if err := validatePassword(req.New); err != nil {
		return err
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte(req.New), bcrypt.DefaultCost)
	u.PasswordHash = string(hash)
	if err := a.Store.UpdateUser(u); err != nil {
		return err
	}
	a.audit(r, p, "user.password_changed", u.Username, nil)
	return ok(w, map[string]bool{"ok": true})
}

// ---- users ----

func (a *API) listUsers(w http.ResponseWriter, r *http.Request, p *Principal) error {
	users, err := a.Store.ListUsers()
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": users, "roles": RoleNames})
}

func (a *API) createUserHandler(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Username    string `json:"username"`
		Password    string `json:"password"`
		Role        string `json:"role"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := a.CreateUser(req.Username, req.Password, req.Role, req.DisplayName); err != nil {
		return err
	}
	u, _ := a.Store.GetUserByName(req.Username)
	if u != nil && req.Email != "" {
		u.Email = req.Email
		_ = a.Store.UpdateUser(u)
	}
	a.audit(r, p, "user.created", req.Username, map[string]string{"role": req.Role})
	return ok(w, u)
}

func (a *API) updateUser(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	u, err := a.Store.GetUser(id)
	if err != nil {
		return err
	}
	var req struct {
		DisplayName *string `json:"display_name"`
		Email       *string `json:"email"`
		Role        *string `json:"role"`
		Disabled    *bool   `json:"disabled"`
		Password    *string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.DisplayName != nil {
		u.DisplayName = *req.DisplayName
	}
	if req.Email != nil {
		u.Email = *req.Email
	}
	demoting := false
	if req.Role != nil {
		if _, ok := Roles[*req.Role]; !ok {
			return badRequest("unknown role")
		}
		demoting = u.Role == "admin" && *req.Role != "admin"
		u.Role = *req.Role
	}
	if req.Disabled != nil {
		demoting = demoting || (*req.Disabled && u.Role == "admin" && !u.Disabled)
		u.Disabled = *req.Disabled
	}
	if demoting && a.Store.CountAdmins() <= 1 {
		return badRequest("at least one enabled administrator is required")
	}
	if req.Password != nil && *req.Password != "" {
		if err := validatePassword(*req.Password); err != nil {
			return err
		}
		hash, _ := bcrypt.GenerateFromPassword([]byte(*req.Password), bcrypt.DefaultCost)
		u.PasswordHash = string(hash)
		_ = a.Store.DeleteUserSessions(u.ID)
	}
	if err := a.Store.UpdateUser(u); err != nil {
		return err
	}
	if u.Disabled {
		_ = a.Store.DeleteUserSessions(u.ID)
	}
	a.audit(r, p, "user.updated", u.Username, map[string]any{"role": u.Role, "disabled": u.Disabled, "password_reset": req.Password != nil && *req.Password != ""})
	return ok(w, u)
}

func (a *API) deleteUser(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if p.Kind == "session" && id == p.ID {
		return badRequest("you cannot delete your own account")
	}
	u, err := a.Store.GetUser(id)
	if err != nil {
		return err
	}
	if u.Role == "admin" && !u.Disabled && a.Store.CountAdmins() <= 1 {
		return badRequest("at least one enabled administrator is required")
	}
	if err := a.Store.DeleteUser(id); err != nil {
		return err
	}
	a.audit(r, p, "user.deleted", u.Username, nil)
	return ok(w, map[string]bool{"ok": true})
}

// ---- API keys ----

func (a *API) listAPIKeys(w http.ResponseWriter, r *http.Request, p *Principal) error {
	keys, err := a.Store.ListAPIKeys()
	if err != nil {
		return err
	}
	if keys == nil {
		keys = []*store.APIKey{}
	}
	return ok(w, map[string]any{"items": keys})
}

func (a *API) createAPIKey(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		ExpiresIn int    `json:"expires_in_days"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Name) == "" {
		return badRequest("name is required")
	}
	if _, ok := Roles[req.Role]; !ok {
		return badRequest("unknown role")
	}
	prefix := randomHex(4)
	secret := apiKeyPrefix + prefix + "_" + randomHex(24)
	k := &store.APIKey{Name: strings.TrimSpace(req.Name), Prefix: apiKeyPrefix + prefix, KeyHash: sha(secret), Role: req.Role, CreatedBy: p.Actor()}
	if req.ExpiresIn > 0 {
		k.ExpiresAt = time.Now().AddDate(0, 0, req.ExpiresIn).Unix()
	}
	if err := a.Store.CreateAPIKey(k); err != nil {
		return err
	}
	a.audit(r, p, "apikey.created", k.Name, map[string]string{"role": k.Role})
	return ok(w, map[string]any{"key": k, "secret": secret})
}

func (a *API) deleteAPIKey(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := a.Store.DeleteAPIKey(id); err != nil {
		return err
	}
	a.audit(r, p, "apikey.deleted", r.PathValue("id"), nil)
	return ok(w, map[string]bool{"ok": true})
}
