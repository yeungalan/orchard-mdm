package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/yeungalan/orchard-mdm/internal/apns"
	"github.com/yeungalan/orchard-mdm/internal/config"
	"github.com/yeungalan/orchard-mdm/internal/devicesim"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
	csrf string
	key  string
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, rdr)
	req.Header.Set("Content-Type", "application/json")
	if c.csrf != "" {
		req.Header.Set("X-CSRF-Token", c.csrf)
	}
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func (c *client) must(method, path string, body any) map[string]any {
	c.t.Helper()
	status, out := c.do(method, path, body)
	if status != 200 {
		c.t.Fatalf("%s %s = %d %v", method, path, status, out)
	}
	return out
}

func TestFullStack(t *testing.T) {
	cfg := &config.Config{DataDir: t.TempDir(), APNsURL: "http://127.0.0.1:1", DisableSchedul: true, Listen: ":0"}
	app, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	app.Reconciler.SetDelay(0)
	srv := httptest.NewServer(app.Handler())
	defer srv.Close()
	cfg.PublicURL = srv.URL
	certPEM, keyPEM, _ := pki.NewTestPushCert("com.apple.mgmt.External.fullstack")
	app.Store.PutKeyPair(&store.KeyPair{Name: apns.KeyPairName, CertPEM: certPEM, KeyPEM: keyPEM})

	jar, _ := cookiejar.New(nil)
	c := &client{t: t, base: srv.URL, http: &http.Client{Jar: jar}}

	if st := c.must("GET", "/api/auth/status", nil); st["setup_required"] != true {
		t.Fatal("setup should be required")
	}
	if status, _ := c.do("GET", "/api/devices", nil); status != 401 {
		t.Fatalf("unauthenticated = %d", status)
	}
	if status, _ := c.do("POST", "/api/auth/setup", map[string]string{"setup_code": "WRONG", "username": "admin", "password": "correct horse battery"}); status != 403 {
		t.Fatalf("wrong setup code = %d", status)
	}
	out := c.must("POST", "/api/auth/setup", map[string]string{"setup_code": app.API.SetupToken(), "username": "admin", "password": "correct horse battery", "org_name": "Acme"})
	csrf := out["csrf"].(string)
	// mutations need the CSRF header
	if status, _ := c.do("POST", "/api/groups", map[string]any{"name": "x"}); status != 403 {
		t.Fatalf("missing CSRF = %d", status)
	}
	c.csrf = csrf

	g := c.must("POST", "/api/groups", map[string]any{"name": "Sales iPhones", "kind": "static"})
	gid := g["id"].(float64)
	tok := c.must("POST", "/api/enrollment/tokens", map[string]any{"name": "Sales", "ownership": "corporate", "group_ids": []float64{gid}, "assigned_user": "Alex"})
	token := tok["token"].(string)

	// device enrolls through the public enrollment link
	resp, err := http.Get(srv.URL + "/enroll/" + token)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("enroll page: %v %v", err, resp.Status)
	}
	resp.Body.Close()
	resp, _ = http.Get(srv.URL + "/enroll/" + token + "/profile")
	prof, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("Content-Type") != "application/x-apple-aspen-config" {
		t.Fatalf("profile content type %q: %s", resp.Header.Get("Content-Type"), prof)
	}
	dev := devicesim.New("Alex's iPhone", "iPhone17,1")
	if err := dev.Enroll(prof); err != nil {
		t.Fatal(err)
	}
	app.Bus.Wait()
	if _, err := dev.Poll(); err != nil {
		t.Fatal(err)
	}
	list := c.must("GET", "/api/devices", nil)
	if list["total"].(float64) != 1 {
		t.Fatalf("devices = %v", list)
	}
	d := c.must("GET", "/api/devices/"+dev.UDID, nil)
	device := d["device"].(map[string]any)
	if device["ownership"] != "corporate" || device["assigned_user"] != "Alex" || device["enrollment_type"] != "token" {
		t.Fatalf("token defaults not applied: %v", device)
	}
	if groups := d["groups"].([]any); len(groups) < 2 {
		t.Fatalf("token groups not applied: %v", groups)
	}
	if _, leaked := device["activation_lock_bypass"]; leaked {
		t.Fatal("bypass code exposed in device JSON")
	}

	// a manual command round trip
	res := c.must("POST", "/api/devices/"+dev.UDID+"/commands", map[string]any{"command": "DeviceLock", "params": map[string]any{"Message": "Locked by IT"}})
	uuid := res["command_uuid"].(string)
	handled, _ := dev.Poll()
	if !slices.Contains(handled, "DeviceLock") {
		t.Fatalf("DeviceLock not delivered: %v", handled)
	}
	cmd := c.must("GET", "/api/commands/"+uuid, nil)
	if cmd["command"].(map[string]any)["status"] != "Acknowledged" {
		t.Fatalf("command = %v", cmd)
	}
	for _, bad := range []string{"EraseDevice", "DeleteUser"} {
		if status, out := c.do("POST", "/api/devices/"+dev.UDID+"/commands", map[string]any{"command": bad}); status != 400 || !strings.Contains(out["error"].(string), "wipe") {
			t.Fatalf("%s = %d %v", bad, status, out)
		}
	}
	if status, _ := c.do("POST", "/api/devices/"+dev.UDID+"/actions/erase", nil); status != 400 {
		t.Fatalf("erase action = %d", status)
	}
	if status, _ := c.do("POST", "/api/devices/"+dev.UDID+"/commands", map[string]any{"command": "Custom", "params": map[string]any{"Command": `{"RequestType":"EraseDevice"}`}}); status != 400 {
		t.Fatal("custom erase accepted")
	}

	// configuration profile assigned to the token's group
	p := c.must("POST", "/api/profiles", map[string]any{"name": "Office Wi-Fi", "payloads": []map[string]any{{"type": "com.apple.wifi.managed",
		"values": map[string]any{"SSID_STR": "Acme", "EncryptionType": "WPA2", "Password": "secret123"}}}})
	pid := p["profile"].(map[string]any)["id"].(float64)
	c.must("POST", "/api/assignments", map[string]any{"item_type": "profile", "item_id": pid, "group_id": gid, "intent": "install"})
	app.Reconciler.ReconcileAll(context.Background())
	dev.Poll()
	if !dev.HasProfile(p["profile"].(map[string]any)["identifier"].(string)) {
		t.Fatal("assigned profile not installed")
	}
	if !dev.HasProfile("com.orchardmdm.portal") {
		t.Fatal("company portal web clip not installed")
	}

	// companion agent telemetry
	sec := c.must("GET", "/api/devices/"+dev.UDID+"/secrets", nil)
	body, _ := json.Marshal(map[string]any{"battery": 0.42, "battery_state": "unplugged", "ssid": "Acme-Office", "bssid": "aa:bb:cc:dd:ee:ff", "latitude": 35.68, "longitude": 139.76, "accuracy": 8})
	req, _ := http.NewRequest("POST", srv.URL+"/agent/v1/report", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+sec["agent_token"].(string))
	resp, err = http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("agent report: %v %v", err, resp.Status)
	}
	resp.Body.Close()
	tel := c.must("GET", "/api/devices/"+dev.UDID+"/telemetry", nil)
	foundSSID := false
	for _, n := range tel["networks"].([]any) {
		if n.(map[string]any)["value"] == "Acme-Office" {
			foundSSID = true
		}
	}
	if !foundSSID {
		t.Fatalf("agent SSID not recorded: %v", tel["networks"])
	}
	if locs := c.must("GET", "/api/devices/"+dev.UDID+"/locations", nil)["items"].([]any); len(locs) != 1 {
		t.Fatalf("locations = %v", locs)
	}

	// self-service portal
	portalURL := sec["portal_url"].(string)
	resp, _ = http.Get(portalURL)
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(page), "Alex&#39;s iPhone") {
		t.Fatalf("portal page %d: %.300s", resp.StatusCode, page)
	}

	// API keys and roles
	k := c.must("POST", "/api/apikeys", map[string]any{"name": "reporting", "role": "readonly"})
	ro := &client{t: t, base: srv.URL, http: http.DefaultClient, key: k["secret"].(string)}
	ro.must("GET", "/api/devices", nil)
	if status, _ := ro.do("POST", "/api/devices/"+dev.UDID+"/actions/sync", nil); status != 403 {
		t.Fatalf("readonly key could act: %d", status)
	}
	if status, _ := ro.do("GET", "/api/users", nil); status != 403 {
		t.Fatalf("readonly key could list users: %d", status)
	}
	audit := c.must("GET", "/api/audit", nil)
	if audit["total"].(float64) < 5 {
		t.Fatalf("audit log too short: %v", audit["total"])
	}
	dash := c.must("GET", "/api/dashboard", nil)
	if dash["devices"].(map[string]any)["enrolled"].(float64) != 1 {
		t.Fatalf("dashboard = %v", dash["devices"])
	}
	// console shell
	resp, _ = http.Get(srv.URL + "/")
	if resp.StatusCode != 200 {
		t.Fatalf("console = %d", resp.StatusCode)
	}
	resp.Body.Close()
}
