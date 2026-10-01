package portal

// Account-driven enrollment: a person signs in to their work account in
// Settings ("Sign in to Work or School Account"), the device discovers this
// server through the well-known endpoint, the person authenticates in a web
// view (enrollment code or single sign-on), and the device receives a User
// Enrollment (BYOD) or device enrollment profile bound to the Managed Apple
// Account.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/smallstep/pkcs7"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"golang.org/x/oauth2"
	"howett.net/plist"
)

// Account enrollment settings.
const (
	SettingAccountEnabled   = "account_enroll_enabled"
	SettingAccountMode      = "account_enroll_mode" // byod | adde
	SettingAccountDomains   = "account_enroll_domains"
	SettingAccountAuth      = "account_enroll_auth" // code | sso | both
	SettingAccountGroupIDs  = "account_enroll_group_ids"
	SettingOIDCIssuer       = "oidc_issuer"
	SettingOIDCClientID     = "oidc_client_id"
	SettingOIDCClientSecret = "oidc_client_secret"
	SettingOIDCClaim        = "oidc_identity_claim"
	SettingOIDCMatch        = "oidc_require_match"
)

const (
	callbackScheme = "apple-remotemanagement-user-login"
	tokenTTL       = time.Hour
	stateTTL       = 15 * time.Minute
)

// AccountConfig is the effective account-driven enrollment configuration.
type AccountConfig struct {
	Enabled      bool
	Mode         string // byod | adde
	Domains      []string
	AllowCode    bool
	AllowSSO     bool
	GroupIDs     []int64
	OIDCIssuer   string
	OIDCClientID string
	OIDCSecret   string
	OIDCClaim    string
	OIDCMatch    bool
}

// LoadAccountConfig reads the configuration from settings.
func LoadAccountConfig(s *store.Store) AccountConfig {
	c := AccountConfig{
		Enabled:      s.GetSettingBool(SettingAccountEnabled, false),
		Mode:         s.GetSetting(SettingAccountMode, "byod"),
		OIDCIssuer:   strings.TrimSpace(s.GetSetting(SettingOIDCIssuer, "")),
		OIDCClientID: strings.TrimSpace(s.GetSetting(SettingOIDCClientID, "")),
		OIDCSecret:   s.GetSetting(SettingOIDCClientSecret, ""),
		OIDCClaim:    s.GetSetting(SettingOIDCClaim, "email"),
		OIDCMatch:    s.GetSettingBool(SettingOIDCMatch, true),
	}
	if c.Mode != "adde" {
		c.Mode = "byod"
	}
	for _, d := range strings.Split(s.GetSetting(SettingAccountDomains, ""), ",") {
		if d = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(d), "@"))); d != "" {
			c.Domains = append(c.Domains, d)
		}
	}
	auth := s.GetSetting(SettingAccountAuth, "code")
	c.AllowCode = auth == "code" || auth == "both"
	c.AllowSSO = (auth == "sso" || auth == "both") && c.OIDCIssuer != "" && c.OIDCClientID != ""
	_ = s.GetSettingJSON(SettingAccountGroupIDs, &c.GroupIDs)
	if c.OIDCClaim == "" {
		c.OIDCClaim = "email"
	}
	return c
}

// DomainAllowed reports whether an account identifier's domain may enroll.
func (c AccountConfig) DomainAllowed(identifier string) bool {
	_, domain, ok := splitIdentifier(identifier)
	if !ok {
		return false
	}
	if len(c.Domains) == 0 {
		return true
	}
	for _, d := range c.Domains {
		if domain == d || strings.HasSuffix(domain, "."+d) {
			return true
		}
	}
	return false
}

func splitIdentifier(id string) (user, domain string, ok bool) {
	id = strings.TrimSpace(id)
	i := strings.LastIndex(id, "@")
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	user, domain = id[:i], strings.ToLower(id[i+1:])
	if !strings.Contains(domain, ".") || strings.ContainsAny(domain, " /?#") {
		return "", "", false
	}
	return user, domain, true
}

func modeToProfile(mode string) string {
	if mode == "adde" {
		return mdm.ModeADDE
	}
	return mdm.ModeBYOD
}

type oidcState struct {
	mode, userID, verifier, nonce string
	expires                       time.Time
}

type accountFlow struct {
	p *portal

	mu     sync.Mutex
	states map[string]*oidcState

	provMu   sync.Mutex
	provider *oidc.Provider
	provFor  string
}

func (p *portal) registerAccount(mux *http.ServeMux) {
	f := &accountFlow{p: p, states: map[string]*oidcState{}}
	mux.HandleFunc("GET /.well-known/com.apple.remotemanagement", f.wellKnown)
	mux.HandleFunc("POST /enroll/account/{mode}", f.enroll)
	mux.HandleFunc("GET /enroll/account/{mode}/auth", f.authPage)
	mux.HandleFunc("POST /enroll/account/{mode}/auth", f.authCode)
	mux.HandleFunc("GET /enroll/account/{mode}/sso", f.ssoStart)
	mux.HandleFunc("GET /enroll/account/callback", f.ssoCallback)
}

func validMode(m string) bool { return m == "byod" || m == "adde" }

// wellKnown answers service discovery.
func (f *accountFlow) wellKnown(w http.ResponseWriter, r *http.Request) {
	cfg := LoadAccountConfig(f.p.Store)
	base := f.p.MDM.PublicURL()
	if !cfg.Enabled || base == "" {
		http.NotFound(w, r)
		return
	}
	userID := r.URL.Query().Get("user-identifier")
	if userID != "" && !cfg.DomainAllowed(userID) {
		http.NotFound(w, r)
		return
	}
	switch r.URL.Query().Get("model-family") {
	case "", "iPhone", "iPad":
	default:
		// Orchard manages iPhone and iPad only
		http.NotFound(w, r)
		return
	}
	version := "mdm-byod"
	if cfg.Mode == "adde" {
		version = "mdm-adde"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{"Servers": []map[string]string{{"Version": version, "BaseURL": base + "/enroll/account/" + cfg.Mode}}})
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

// enroll is the BaseURL: without a valid access token it starts web
// authentication; with one it returns the enrollment profile.
func (f *accountFlow) enroll(w http.ResponseWriter, r *http.Request) {
	mode := r.PathValue("mode")
	cfg := LoadAccountConfig(f.p.Store)
	if !validMode(mode) || !cfg.Enabled {
		http.Error(w, "account sign-in enrollment is not enabled", http.StatusForbidden)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	content := body
	if p7, err := pkcs7.Parse(body); err == nil && len(p7.Content) > 0 {
		content = p7.Content
	}
	var info struct {
		Language string `plist:"LANGUAGE"`
		Product  string `plist:"PRODUCT"`
		Version  string `plist:"VERSION"`
	}
	_, _ = plist.Unmarshal(content, &info)
	tok := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	if tok == "" {
		f.challenge(w, mode)
		return
	}
	a, err := f.p.Store.GetAccountEnrollmentByToken(hashToken(tok))
	if err != nil || a.Mode != mode {
		f.challenge(w, mode)
		return
	}
	prof, err := f.p.MDM.EnrollmentProfile(mdm.EnrollmentOptions{
		Ref: fmt.Sprintf("account:%d", a.ID), Mode: modeToProfile(a.Mode), ManagedAppleID: a.ManagedAppleID, ChallengeTTL: tokenTTL,
	})
	if err != nil {
		f.p.Log.Error("account enrollment profile", "err", err)
		http.Error(w, "enrollment is not available", http.StatusServiceUnavailable)
		return
	}
	f.p.Store.MarkAccountProfileIssued(a.ID, info.Product, info.Version)
	_ = f.p.Store.InsertEvent(&store.Event{Type: "account.profile", Message: fmt.Sprintf("Enrollment profile issued to %s (%s, %s)", a.ManagedAppleID, info.Product, modeLabel(a.Mode))})
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(prof)
}

func modeLabel(m string) string {
	if m == "adde" {
		return "device enrollment"
	}
	return "User Enrollment"
}

func (f *accountFlow) challenge(w http.ResponseWriter, mode string) {
	authURL := f.p.MDM.PublicURL() + "/enroll/account/" + mode + "/auth"
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer method="apple-as-web", url="%s"`, authURL))
	w.WriteHeader(http.StatusUnauthorized)
}

type accountPage struct {
	*page
	Mode           string
	BYOD           bool
	UserIdentifier string
	AllowCode      bool
	AllowSSO       bool
	SSOURL         string
	Code           string
}

func (f *accountFlow) renderAuth(w http.ResponseWriter, r *http.Request, mode, userID, errMsg string, status int) {
	cfg := LoadAccountConfig(f.p.Store)
	pg := &accountPage{page: f.p.base(r, "Sign in"), Mode: mode, BYOD: mode == "byod", UserIdentifier: userID, AllowCode: cfg.AllowCode, AllowSSO: cfg.AllowSSO}
	pg.Error = errMsg
	if cfg.AllowSSO {
		pg.SSOURL = "/enroll/account/" + mode + "/sso?user-identifier=" + url.QueryEscape(userID)
	}
	// the form submits to this server and is answered with a redirect to the
	// authentication-session callback scheme
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; form-action 'self' "+callbackScheme+":; frame-ancestors 'none'; base-uri 'self'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := f.p.tmpl.ExecuteTemplate(w, "account.html", pg); err != nil {
		f.p.Log.Error("render account", "err", err)
	}
}

func (f *accountFlow) precheck(w http.ResponseWriter, r *http.Request, mode, userID string) (AccountConfig, bool) {
	cfg := LoadAccountConfig(f.p.Store)
	switch {
	case !validMode(mode) || !cfg.Enabled:
		f.renderAuth(w, r, mode, userID, "Signing in with a work account isn't enabled for this organization.", http.StatusForbidden)
		return cfg, false
	case cfg.Mode != mode:
		f.renderAuth(w, r, mode, userID, "This enrollment option isn't offered anymore. Start again from Settings.", http.StatusForbidden)
		return cfg, false
	case !cfg.DomainAllowed(userID):
		f.renderAuth(w, r, mode, userID, "“"+userID+"” isn't a work account this organization manages.", http.StatusForbidden)
		return cfg, false
	}
	return cfg, true
}

func (f *accountFlow) authPage(w http.ResponseWriter, r *http.Request) {
	mode, userID := r.PathValue("mode"), strings.TrimSpace(r.URL.Query().Get("user-identifier"))
	if _, ok := f.precheck(w, r, mode, userID); !ok {
		return
	}
	f.renderAuth(w, r, mode, userID, "", http.StatusOK)
}

// authCode completes sign-in with an enrollment code.
func (f *accountFlow) authCode(w http.ResponseWriter, r *http.Request) {
	mode := r.PathValue("mode")
	_ = r.ParseForm()
	userID := strings.TrimSpace(r.PostForm.Get("user-identifier"))
	cfg, ok := f.precheck(w, r, mode, userID)
	if !ok {
		return
	}
	if !cfg.AllowCode {
		f.renderAuth(w, r, mode, userID, "Sign in with your organization account instead.", http.StatusForbidden)
		return
	}
	code := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(r.PostForm.Get("code")), " ", ""))
	t, err := f.p.Store.GetEnrollmentTokenByValue(code)
	if err != nil || !t.Usable() {
		f.renderAuth(w, r, mode, userID, "That enrollment code isn't valid. Check it with your IT team.", http.StatusOK)
		return
	}
	user, _, _ := splitIdentifier(userID)
	f.complete(w, r, &store.AccountEnrollment{Mode: mode, UserIdentifier: userID, ManagedAppleID: userID, DisplayName: user, AuthMethod: "code",
		EnrollmentTokenID: t.ID, GroupIDs: cfg.GroupIDs})
}

// complete records the sign-in and hands the access token to the device.
func (f *accountFlow) complete(w http.ResponseWriter, r *http.Request, a *store.AccountEnrollment) {
	tok := pki.RandomToken(32)
	a.TokenHash = hashToken(tok)
	a.ExpiresAt = store.Now() + int64(tokenTTL.Seconds())
	a.IP = mdm.ClientIP(r, f.p.TrustProxy)
	if a.GroupIDs == nil {
		a.GroupIDs = []int64{}
	}
	if err := f.p.Store.CreateAccountEnrollment(a); err != nil {
		f.p.Log.Error("store account enrollment", "err", err)
		f.renderAuth(w, r, a.Mode, a.UserIdentifier, "Something went wrong. Try again.", http.StatusInternalServerError)
		return
	}
	_ = f.p.Store.InsertEvent(&store.Event{Type: "account.signin", Message: fmt.Sprintf("%s signed in for %s (%s)", a.UserIdentifier, modeLabel(a.Mode), a.AuthMethod)})
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Location", callbackScheme+"://authentication-results?access-token="+url.QueryEscape(tok))
	w.WriteHeader(http.StatusPermanentRedirect)
}

// ---- single sign-on (OpenID Connect) ----

func (f *accountFlow) oidc(ctx context.Context, cfg AccountConfig) (*oidc.Provider, *oauth2.Config, error) {
	f.provMu.Lock()
	defer f.provMu.Unlock()
	if f.provider == nil || f.provFor != cfg.OIDCIssuer {
		pctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		prov, err := oidc.NewProvider(pctx, cfg.OIDCIssuer)
		if err != nil {
			return nil, nil, err
		}
		f.provider, f.provFor = prov, cfg.OIDCIssuer
	}
	oc := &oauth2.Config{
		ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCSecret, Endpoint: f.provider.Endpoint(),
		RedirectURL: f.p.MDM.PublicURL() + "/enroll/account/callback", Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
	}
	return f.provider, oc, nil
}

func (f *accountFlow) ssoStart(w http.ResponseWriter, r *http.Request) {
	mode, userID := r.PathValue("mode"), strings.TrimSpace(r.URL.Query().Get("user-identifier"))
	cfg, ok := f.precheck(w, r, mode, userID)
	if !ok {
		return
	}
	if !cfg.AllowSSO {
		f.renderAuth(w, r, mode, userID, "Single sign-on isn't set up. Use an enrollment code.", http.StatusForbidden)
		return
	}
	_, oc, err := f.oidc(r.Context(), cfg)
	if err != nil {
		f.p.Log.Error("oidc discovery", "issuer", cfg.OIDCIssuer, "err", err)
		f.renderAuth(w, r, mode, userID, "The sign-in service can't be reached right now. Try again later.", http.StatusBadGateway)
		return
	}
	st := &oidcState{mode: mode, userID: userID, verifier: oauth2.GenerateVerifier(), nonce: pki.RandomToken(16), expires: time.Now().Add(stateTTL)}
	key := pki.RandomToken(24)
	f.mu.Lock()
	now := time.Now()
	for k, v := range f.states {
		if now.After(v.expires) {
			delete(f.states, k)
		}
	}
	f.states[key] = st
	f.mu.Unlock()
	opts := []oauth2.AuthCodeOption{oidc.Nonce(st.nonce), oauth2.S256ChallengeOption(st.verifier)}
	if userID != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", userID))
	}
	http.Redirect(w, r, oc.AuthCodeURL(key, opts...), http.StatusFound)
}

func (f *accountFlow) ssoCallback(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("state")
	f.mu.Lock()
	st := f.states[key]
	delete(f.states, key)
	f.mu.Unlock()
	if st == nil || time.Now().After(st.expires) {
		f.renderAuth(w, r, "byod", "", "This sign-in has expired. Start again from Settings on your device.", http.StatusBadRequest)
		return
	}
	cfg, ok := f.precheck(w, r, st.mode, st.userID)
	if !ok {
		return
	}
	if e := r.URL.Query().Get("error"); e != "" {
		f.renderAuth(w, r, st.mode, st.userID, "Sign-in was canceled or failed ("+e+").", http.StatusUnauthorized)
		return
	}
	prov, oc, err := f.oidc(r.Context(), cfg)
	if err != nil {
		f.renderAuth(w, r, st.mode, st.userID, "The sign-in service can't be reached right now.", http.StatusBadGateway)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	tok, err := oc.Exchange(ctx, r.URL.Query().Get("code"), oauth2.VerifierOption(st.verifier))
	if err != nil {
		f.p.Log.Warn("oidc code exchange", "err", err)
		f.renderAuth(w, r, st.mode, st.userID, "Sign-in couldn't be completed. Try again.", http.StatusUnauthorized)
		return
	}
	raw, _ := tok.Extra("id_token").(string)
	idt, err := prov.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID}).Verify(ctx, raw)
	if err != nil || idt.Nonce != st.nonce {
		f.p.Log.Warn("oidc id token rejected", "err", err)
		f.renderAuth(w, r, st.mode, st.userID, "Sign-in couldn't be verified. Try again.", http.StatusUnauthorized)
		return
	}
	var claims map[string]any
	_ = idt.Claims(&claims)
	identity, _ := claims[cfg.OIDCClaim].(string)
	if identity == "" {
		identity, _ = claims["email"].(string)
	}
	userID := st.userID
	if cfg.OIDCMatch && !strings.EqualFold(strings.TrimSpace(identity), userID) {
		f.p.Log.Warn("account enrollment identity mismatch", "entered", userID, "idp", identity)
		f.renderAuth(w, r, st.mode, userID, fmt.Sprintf("You signed in as %s, but the device is enrolling %s. Sign in with the same work account.", identity, userID), http.StatusForbidden)
		return
	}
	if userID == "" {
		userID = identity
	}
	name, _ := claims["name"].(string)
	if name == "" {
		name, _, _ = splitIdentifier(userID)
	}
	f.complete(w, r, &store.AccountEnrollment{Mode: st.mode, UserIdentifier: userID, ManagedAppleID: userID, DisplayName: name, AuthMethod: "sso", GroupIDs: cfg.GroupIDs})
}
