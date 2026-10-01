package dep_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smallstep/pkcs7"
	"github.com/yeungalan/orchard-mdm/internal/dep"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"github.com/yeungalan/orchard-mdm/internal/testenv"
	"howett.net/plist"
)

func fakeToken(t *testing.T, certPEM string) []byte {
	cert, err := pki.ParseCertPEM([]byte(certPEM))
	if err != nil {
		t.Fatal(err)
	}
	inner := "Content-Type: text/plain;charset=UTF-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\n-----BEGIN MESSAGE-----\n" +
		`{"consumer_key":"CK_1","consumer_secret":"CS_1","access_token":"AT_1","access_secret":"AS_1","access_token_expiry":"2027-01-01T00:00:00Z"}` +
		"\n-----END MESSAGE-----\n"
	enc, err := pkcs7.Encrypt([]byte(inner), []*x509.Certificate{cert})
	if err != nil {
		t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString(enc)
	var wrapped strings.Builder
	for i := 0; i < len(b64); i += 76 {
		end := i + 76
		if end > len(b64) {
			end = len(b64)
		}
		wrapped.WriteString(b64[i:end] + "\n")
	}
	return []byte("Content-Type: application/pkcs7-mime; name=\"smime.p7m\"; smime-type=enveloped-data\nContent-Transfer-Encoding: base64\nContent-Disposition: attachment; filename=\"smime.p7m\"\n\n" + wrapped.String())
}

func TestADEFlow(t *testing.T) {
	env := testenv.New(t)
	var syncCalls, assigned atomic.Int32
	apple := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/session" {
			if !strings.HasPrefix(r.Header.Get("Authorization"), `OAuth realm="ADM"`) || !strings.Contains(r.Header.Get("Authorization"), `oauth_consumer_key="CK_1"`) {
				w.WriteHeader(401)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{"auth_session_token": "sess1"})
			return
		}
		if r.Header.Get("X-ADM-Auth-Session") != "sess1" {
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/account":
			json.NewEncoder(w).Encode(map[string]string{"server_name": "Orchard Test", "server_uuid": "U1", "org_name": "Example Inc", "admin_id": "admin@example.com"})
		case "/server/devices":
			json.NewEncoder(w).Encode(map[string]any{"cursor": "c1", "more_to_follow": false, "devices": []map[string]string{
				{"serial_number": "SER001", "model": "iPhone 16", "description": "IPHONE 16 BLACK", "os": "iOS", "device_family": "iPhone", "profile_status": "empty"},
				{"serial_number": "SER002", "model": "iPad Air", "os": "iOS", "device_family": "iPad", "profile_status": "empty"},
			}})
		case "/devices/sync":
			syncCalls.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"cursor": "c2", "more_to_follow": false, "devices": []map[string]string{
				{"serial_number": "SER002", "op_type": "deleted"},
				{"serial_number": "SER003", "model": "iPhone 16 Pro", "op_type": "added", "profile_status": "empty"},
			}})
		case "/profile":
			var p map[string]any
			json.Unmarshal(body, &p)
			if !strings.HasSuffix(p["url"].(string), "/mdm/ade/enroll") || p["is_supervised"] != true {
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"profile_uuid": "PROF-1", "devices": map[string]string{}})
		case "/profile/devices":
			var req struct {
				Devices []string `json:"devices"`
			}
			json.Unmarshal(body, &req)
			res := map[string]string{}
			for _, d := range req.Devices {
				res[d] = "SUCCESS"
				assigned.Add(1)
			}
			json.NewEncoder(w).Encode(map[string]any{"profile_uuid": "PROF-1", "devices": res})
		default:
			w.WriteHeader(404)
		}
	}))
	defer apple.Close()

	svc := dep.New(env.Store, env.MDM, apple.URL, slog.Default())
	srv, err := svc.CreateServer("ABM")
	if err != nil {
		t.Fatal(err)
	}
	srv, err = svc.UploadToken(context.Background(), srv.ID, fakeToken(t, srv.CertPEM))
	if err != nil {
		t.Fatal(err)
	}
	if !srv.HasToken || srv.OrgName != "Example Inc" || srv.LastError != "" {
		t.Fatalf("server = %+v", srv)
	}
	prof := &store.DEPProfile{Name: "Corporate", Config: map[string]any{"await_device_configured": true, "skip_setup_items": []string{"Siri"}}}
	if err := env.Store.CreateDEPProfile(prof); err != nil {
		t.Fatal(err)
	}
	srv.DefaultProfileID = prof.ID
	env.Store.UpdateDEPServer(srv)

	res, err := svc.Sync(context.Background(), srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 2 || res.Assigned != 2 {
		t.Fatalf("first sync = %+v", res)
	}
	res, err = svc.Sync(context.Background(), srv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.Added != 1 || res.Removed != 1 || syncCalls.Load() != 1 {
		t.Fatalf("second sync = %+v", res)
	}
	devs, _ := env.Store.ListDEPDevices(0, "")
	if len(devs) != 2 {
		t.Fatalf("devices = %d", len(devs))
	}
	d1, _ := env.Store.GetDEPDevice("SER001")
	if d1.AssignedProfileID != prof.ID || d1.ProfileUUID != "PROF-1" {
		t.Fatalf("SER001 = %+v", d1)
	}

	// enrollment endpoint
	body, _ := plist.Marshal(map[string]any{"SERIAL": "SER001", "UDID": "UDID-1", "PRODUCT": "iPhone17,3", "VERSION": "22G86"}, plist.XMLFormat)
	rr := httptest.NewRecorder()
	svc.EnrollHandler(rr, httptest.NewRequest("POST", "/mdm/ade/enroll", bytes.NewReader(body)))
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "application/x-apple-aspen-config" {
		t.Fatalf("enroll = %d %s", rr.Code, rr.Body.String())
	}
	body, _ = plist.Marshal(map[string]any{"SERIAL": "UNKNOWN", "UDID": "UDID-2"}, plist.XMLFormat)
	rr = httptest.NewRecorder()
	svc.EnrollHandler(rr, httptest.NewRequest("POST", "/mdm/ade/enroll", bytes.NewReader(body)))
	if rr.Code != 403 {
		t.Fatalf("unknown serial allowed: %d", rr.Code)
	}
}
