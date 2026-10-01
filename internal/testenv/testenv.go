// Package testenv wires an in-memory Orchard MDM protocol stack for tests.
package testenv

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/apns"
	"github.com/yeungalan/orchard-mdm/internal/ddm"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/scepsrv"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

// Env is a running test stack.
type Env struct {
	Store  *store.Store
	Bus    *events.Bus
	MDM    *mdm.Service
	DDM    *ddm.Service
	Server *httptest.Server
	Mux    *http.ServeMux
	Log    *slog.Logger
}

// Topic is the fake push topic.
const Topic = "com.apple.mgmt.External.00000000-test"

// New builds the stack. Extra routes can be added to Mux before use.
func New(t testing.TB) *Env {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	certPEM, keyPEM, err := pki.NewTestPushCert(Topic)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.PutKeyPair(&store.KeyPair{Name: apns.KeyPairName, CertPEM: certPEM, KeyPEM: keyPEM}); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() {
		log = slog.Default()
	}
	bus := events.New(true)
	d := ddm.New(st, bus)
	env := &Env{Store: st, Bus: bus, DDM: d, Mux: http.NewServeMux(), Log: log}
	env.Server = httptest.NewServer(env.Mux)
	svc, err := mdm.New(mdm.Options{
		Store: st, Bus: bus, Push: apns.NewManager(st, "http://127.0.0.1:1"), DDM: d, Log: log,
		PublicURL: func() string { return env.Server.URL }, PushDelay: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	env.MDM = svc
	env.Mux.HandleFunc("PUT /mdm/checkin", svc.HandleCheckin)
	env.Mux.HandleFunc("PUT /mdm/connect", svc.HandleConnect)
	env.Mux.Handle("/scep", &scepsrv.Handler{Store: st, CA: svc.CA, Validity: func() time.Duration { return 365 * 24 * time.Hour }, Log: log})
	t.Cleanup(func() {
		env.Server.Close()
		st.Close()
	})
	return env
}
