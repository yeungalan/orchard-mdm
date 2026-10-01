package server

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/yeungalan/orchard-mdm/internal/apns"
	"github.com/yeungalan/orchard-mdm/internal/config"
	"github.com/yeungalan/orchard-mdm/internal/devicesim"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

func newTestApp(t *testing.T) (*App, *httptest.Server, *client) {
	t.Helper()
	cfg := &config.Config{DataDir: t.TempDir(), APNsURL: "http://127.0.0.1:1", DisableSchedul: true, Listen: ":0"}
	app, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.Close() })
	app.Reconciler.SetDelay(0)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	cfg.PublicURL = srv.URL
	certPEM, keyPEM, _ := pki.NewTestPushCert("com.apple.mgmt.External.byod")
	app.Store.PutKeyPair(&store.KeyPair{Name: apns.KeyPairName, CertPEM: certPEM, KeyPEM: keyPEM})
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: srv.URL, http: &http.Client{Jar: jar}}
	out := c.must("POST", "/api/auth/setup", map[string]string{"setup_code": app.API.SetupToken(), "username": "admin", "password": "correct horse battery", "org_name": "Acme"})
	c.csrf = out["csrf"].(string)
	return app, srv, c
}

func TestBYODUserEnrollment(t *testing.T) {
	app, srv, c := newTestApp(t)
	g := c.must("POST", "/api/groups", map[string]any{"name": "Personal devices", "kind": "static"})
	gid := g["id"].(float64)
	tok := c.must("POST", "/api/enrollment/tokens", map[string]any{"name": "BYOD", "ownership": "personal"})
	code := tok["token"].(string)

	// account sign-in is off: discovery must not advertise the server
	resp, _ := http.Get(srv.URL + "/.well-known/com.apple.remotemanagement?user-identifier=alex@acme.example&model-family=iPhone")
	if resp.StatusCode != 404 {
		t.Fatalf("discovery while disabled = %d", resp.StatusCode)
	}
	c.must("PUT", "/api/enrollment/account", map[string]any{"enabled": true, "mode": "byod", "domains": []string{"acme.example"}, "auth": "code", "group_ids": []float64{gid}, "oidc_require_match": true})

	// discovery rejects other domains and non-iOS families
	for _, q := range []string{"user-identifier=bob@other.example&model-family=iPhone", "user-identifier=alex@acme.example&model-family=Mac"} {
		resp, _ = http.Get(srv.URL + "/.well-known/com.apple.remotemanagement?" + q)
		if resp.StatusCode != 404 {
			t.Fatalf("discovery %s = %d", q, resp.StatusCode)
		}
	}
	// unauthenticated enrollment request gets the web-auth challenge
	resp, _ = http.Post(srv.URL+"/enroll/account/byod", "application/pkcs7-signature", strings.NewReader("x"))
	if resp.StatusCode != 401 || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), `Bearer method="apple-as-web", url="`+srv.URL+`/enroll/account/byod/auth"`) {
		t.Fatalf("challenge = %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	// a wrong code doesn't produce a token
	bad := devicesim.New("Mallory", "iPhone17,1")
	if err := bad.AccountEnroll(srv.URL, "mallory@acme.example", devicesim.CodeAuth("not-a-code")); err == nil {
		t.Fatal("enrollment succeeded with a wrong code")
	}

	dev := devicesim.New("Alex's iPhone", "iPhone17,1")
	if err := dev.AccountEnroll(srv.URL, "alex@acme.example", devicesim.CodeAuth(code)); err != nil {
		t.Fatal(err)
	}
	if !dev.UserEnrollment || dev.EnrollmentMode != "BYOD" {
		t.Fatalf("device did not do a user enrollment: %+v", dev.EnrollmentMode)
	}
	app.Bus.Wait()
	handled, err := dev.Poll()
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Restrictions", "AvailableOSUpdates"} {
		if slices.Contains(handled, forbidden) {
			t.Fatalf("%s sent to a user enrollment", forbidden)
		}
	}
	d := c.must("GET", "/api/devices/"+dev.UDID, nil)
	device := d["device"].(map[string]any)
	if device["enrollment_type"] != "byod" || device["user_enrollment"] != true || device["managed_apple_id"] != "alex@acme.example" ||
		device["ownership"] != "personal" || device["serial_number"] != "" || device["os_version"] != "18.6" {
		t.Fatalf("device = %v", device)
	}
	inGroup := false
	for _, gr := range d["groups"].([]any) {
		if gr.(map[string]any)["name"] == "Personal devices" {
			inGroup = true
		}
	}
	if !inGroup {
		t.Fatal("account enrollment groups not applied")
	}
	if apps := c.must("GET", "/api/devices/"+dev.UDID+"/apps", nil)["items"].([]any); len(apps) != 0 {
		t.Fatalf("personal apps visible: %v", apps)
	}

	// commands iOS doesn't accept on user enrollments are refused up front
	for _, cmd := range []string{"EnableLostMode", "RestartDevice", "ClearPasscode", "Settings.DeviceName"} {
		if status, out := c.do("POST", "/api/devices/"+dev.UDID+"/commands", map[string]any{"command": cmd, "params": map[string]any{"Message": "x", "DeviceName": "x"}}); status != 400 {
			t.Fatalf("%s on BYOD = %d %v", cmd, status, out)
		}
	}
	if status, _ := c.do("POST", "/api/devices/"+dev.UDID+"/commands", map[string]any{"command": "Custom", "params": map[string]any{"Command": `{"RequestType":"ShutDownDevice"}`}}); status != 400 {
		t.Fatal("custom ShutDownDevice accepted for BYOD")
	}
	if status, _ := c.do("POST", "/api/devices/"+dev.UDID+"/commands", map[string]any{"command": "EraseDevice"}); status != 400 {
		t.Fatal("erase accepted")
	}
	c.must("POST", "/api/devices/"+dev.UDID+"/commands", map[string]any{"command": "DeviceLock", "params": map[string]any{}})

	// profiles: supported payloads install, unsupported ones are marked not applicable
	wifi := c.must("POST", "/api/profiles", map[string]any{"name": "Wi-Fi", "payloads": []map[string]any{{"type": "com.apple.wifi.managed", "values": map[string]any{"SSID_STR": "Acme", "EncryptionType": "WPA2", "Password": "secret123"}}}})
	home := c.must("POST", "/api/profiles", map[string]any{"name": "Home Screen", "payloads": []map[string]any{{"type": "com.apple.homescreenlayout", "values": map[string]any{"Dock": "com.apple.mobilesafari"}}}})
	for _, p := range []map[string]any{wifi, home} {
		c.must("POST", "/api/assignments", map[string]any{"item_type": "profile", "item_id": p["profile"].(map[string]any)["id"], "group_id": gid, "intent": "install"})
	}
	app.Reconciler.ReconcileDevice(dev.UDID)
	handled, err = dev.Poll()
	if err != nil {
		t.Fatal(err)
	}
	if !dev.HasProfile(wifi["profile"].(map[string]any)["identifier"].(string)) {
		t.Fatalf("Wi-Fi profile not installed (handled %v)", handled)
	}
	states, _ := app.Store.ListProfileStates(dev.UDID, int64(home["profile"].(map[string]any)["id"].(float64)))
	if len(states) != 1 || states[0].Status != "skipped" {
		t.Fatalf("home screen state = %+v", states)
	}
	// renew and locate are refused; removing management works
	if status, _ := c.do("POST", "/api/devices/"+dev.UDID+"/actions/renew-identity", nil); status != 400 {
		t.Fatal("renew identity accepted for BYOD")
	}
	c.must("POST", "/api/devices/"+dev.UDID+"/actions/unenroll", nil)
	dev.Poll()
	if dd, _ := app.Store.GetDevice(dev.UDID); dd.EnrollmentStatus != "unenrolled" {
		t.Fatalf("status after unenroll = %s", dd.EnrollmentStatus)
	}
	acct := c.must("GET", "/api/enrollment/account", nil)
	if n := len(acct["recent"].([]any)); n != 1 {
		t.Fatalf("recent sign-ins = %d", n)
	}

	// account-driven device enrollment (organization-owned)
	c.must("PUT", "/api/enrollment/account", map[string]any{"enabled": true, "mode": "adde", "domains": []string{"acme.example"}, "auth": "code", "oidc_require_match": true})
	corp := devicesim.New("Kim's iPad", "iPad16,3")
	if err := corp.AccountEnroll(srv.URL, "kim@acme.example", devicesim.CodeAuth(code)); err != nil {
		t.Fatal(err)
	}
	if corp.UserEnrollment || corp.EnrollmentMode != "ADDE" {
		t.Fatal("expected a device enrollment")
	}
	cd, _ := app.Store.GetDevice(corp.UDID)
	if cd.EnrollmentType != "adde" || cd.UserEnrollment || cd.ManagedAppleID != "kim@acme.example" || cd.SerialNumber == "" {
		t.Fatalf("ADDE device = %+v", cd)
	}
}

// fakeIdP is a minimal OpenID Connect provider that signs in whoever it's told to.
type fakeIdP struct {
	srv   *httptest.Server
	key   *rsa.PrivateKey
	email string
	mu    sync.Mutex
	codes map[string][2]string // code -> nonce, challenge
}

func newFakeIdP(t *testing.T, email string) *fakeIdP {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeIdP{key: key, email: email, codes: map[string][2]string{}}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	iss := f.srv.URL
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": iss, "authorization_endpoint": iss + "/authorize", "token_endpoint": iss + "/token",
			"jwks_uri": iss + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("code_challenge_method") != "S256" || q.Get("client_id") != "orchard" {
			http.Error(w, "bad request", 400)
			return
		}
		code := pki.RandomToken(8)
		f.mu.Lock()
		f.codes[code] = [2]string{q.Get("nonce"), q.Get("code_challenge")}
		f.mu.Unlock()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code="+code+"&state="+url.QueryEscape(q.Get("state")), http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		c, ok := f.codes[r.PostForm.Get("code")]
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != c[1] {
			http.Error(w, `{"error":"invalid_grant"}`, 400)
			return
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, (&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
		claims, _ := json.Marshal(map[string]any{"iss": iss, "aud": "orchard", "sub": "u1", "email": f.email, "name": "Alex Doe", "nonce": c[0],
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()})
		jws, _ := signer.Sign(claims)
		idt, _ := jws.CompactSerialize()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idt})
	})
	return f
}

func TestBYODSingleSignOn(t *testing.T) {
	app, srv, c := newTestApp(t)
	idp := newFakeIdP(t, "alex@acme.example")
	c.must("PUT", "/api/enrollment/account", map[string]any{"enabled": true, "mode": "byod", "domains": []string{"acme.example"}, "auth": "sso",
		"oidc_issuer": idp.srv.URL, "oidc_client_id": "orchard", "oidc_client_secret": "s3cret", "oidc_require_match": true})
	acct := c.must("GET", "/api/enrollment/account", nil)
	if cfg := acct["config"].(map[string]any); cfg["oidc_has_secret"] != true {
		t.Fatalf("secret not stored: %v", cfg)
	}
	if strings.Contains(mustJSON(acct), "s3cret") {
		t.Fatal("client secret exposed by the API")
	}
	dev := devicesim.New("Alex's iPhone", "iPhone17,1")
	if err := dev.AccountEnroll(srv.URL, "alex@acme.example", devicesim.LinkAuth("/sso")); err != nil {
		t.Fatal(err)
	}
	d, _ := app.Store.GetDevice(dev.UDID)
	if !d.UserEnrollment || d.AssignedUser != "Alex Doe" || d.ManagedAppleID != "alex@acme.example" {
		t.Fatalf("device = %+v", d)
	}
	// signing in at the identity provider as someone else is refused
	idp.email = "someone.else@acme.example"
	other := devicesim.New("Other", "iPhone17,1")
	if err := other.AccountEnroll(srv.URL, "alex@acme.example", devicesim.LinkAuth("/sso")); err == nil {
		t.Fatal("identity mismatch was accepted")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
