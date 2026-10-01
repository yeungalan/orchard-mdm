// Package server wires Orchard MDM together and runs the HTTP servers and
// background jobs.
package server

import (
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/api"
	"github.com/yeungalan/orchard-mdm/internal/apns"
	"github.com/yeungalan/orchard-mdm/internal/apps"
	"github.com/yeungalan/orchard-mdm/internal/compliance"
	"github.com/yeungalan/orchard-mdm/internal/config"
	"github.com/yeungalan/orchard-mdm/internal/ddm"
	"github.com/yeungalan/orchard-mdm/internal/dep"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/portal"
	"github.com/yeungalan/orchard-mdm/internal/reconcile"
	"github.com/yeungalan/orchard-mdm/internal/scepsrv"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"github.com/yeungalan/orchard-mdm/internal/webhook"
	"github.com/yeungalan/orchard-mdm/web"
	"golang.org/x/crypto/acme/autocert"
)

// Version is set at build time.
var Version = "dev"

// App holds every component.
type App struct {
	Cfg        *config.Config
	Log        *slog.Logger
	Store      *store.Store
	Bus        *events.Bus
	APNs       *apns.Manager
	DDM        *ddm.Service
	MDM        *mdm.Service
	DEP        *dep.Service
	Reconciler *reconcile.Reconciler
	Compliance *compliance.Engine
	Webhooks   *webhook.Dispatcher
	ITunes     *apps.ITunes
	API        *api.API
	Started    time.Time

	acme *autocert.Manager

	tlsMu   sync.RWMutex
	tlsCert *tls.Certificate
	tlsMod  time.Time
}

// New builds the application.
func New(cfg *config.Config, log *slog.Logger) (*App, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "orchard.db"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	a := &App{Cfg: cfg, Log: log, Store: st, Bus: events.New(false), Started: time.Now()}
	a.APNs = apns.NewManager(st, cfg.APNsURL)
	a.DDM = ddm.New(st, a.Bus)
	a.MDM, err = mdm.New(mdm.Options{
		Store: st, Bus: a.Bus, Push: a.APNs, DDM: a.DDM, Log: log,
		PublicURL:  func() string { return cfg.PublicURL },
		TrustProxy: cfg.TrustProxy, ClientCertHeader: cfg.ClientCertHdr,
	})
	if err != nil {
		return nil, err
	}
	a.MDM.SetSigner(a.signingIdentity)
	a.DEP = dep.New(st, a.MDM, cfg.DEPURL, log)
	a.Reconciler = reconcile.New(st, a.MDM, log, cfg.VPPURL)
	a.Compliance = compliance.New(st, a.MDM, log)
	a.Webhooks = webhook.New(st, log)
	a.ITunes = apps.NewITunes(cfg.ITunesURL)
	a.Reconciler.Subscribe(a.Bus)
	a.Compliance.Subscribe(a.Bus)
	a.Webhooks.Subscribe(a.Bus)
	if len(cfg.ACMEDomains) > 0 {
		a.acme = &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(cfg.ACMEDomains...),
			Cache:      autocert.DirCache(filepath.Join(cfg.DataDir, "acme")),
			Email:      cfg.ACMEEmail,
		}
	}
	if cfg.TLSCert != "" {
		if err := a.loadTLS(); err != nil {
			return nil, err
		}
	}
	a.API = api.New(api.Deps{
		Cfg: cfg, Log: log, Store: st, Bus: a.Bus, APNs: a.APNs, MDM: a.MDM, DDM: a.DDM, DEP: a.DEP, Reconciler: a.Reconciler,
		Compliance: a.Compliance, Webhooks: a.Webhooks, ITunes: a.ITunes, Version: Version, Started: a.Started,
		TLSMode: a.tlsMode(),
	})
	if err := a.seed(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) tlsMode() string {
	switch {
	case a.acme != nil:
		return "acme"
	case a.Cfg.TLSCert != "":
		return "files"
	}
	return "proxy"
}

// Close releases resources.
func (a *App) Close() error {
	a.Bus.Wait()
	return a.Store.Close()
}

func (a *App) loadTLS() error {
	fi, err := os.Stat(a.Cfg.TLSCert)
	if err != nil {
		return fmt.Errorf("tls certificate: %w", err)
	}
	a.tlsMu.RLock()
	same := a.tlsCert != nil && fi.ModTime().Equal(a.tlsMod)
	a.tlsMu.RUnlock()
	if same {
		return nil
	}
	pair, err := tls.LoadX509KeyPair(a.Cfg.TLSCert, a.Cfg.TLSKey)
	if err != nil {
		return fmt.Errorf("load TLS key pair: %w", err)
	}
	pair.Leaf, _ = x509.ParseCertificate(pair.Certificate[0])
	a.tlsMu.Lock()
	a.tlsCert, a.tlsMod = &pair, fi.ModTime()
	a.tlsMu.Unlock()
	return nil
}

func (a *App) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	if err := a.loadTLS(); err != nil {
		a.Log.Warn("reloading TLS certificate failed; serving the previous one", "err", err)
	}
	a.tlsMu.RLock()
	defer a.tlsMu.RUnlock()
	if a.tlsCert == nil {
		return nil, errors.New("no TLS certificate")
	}
	return a.tlsCert, nil
}

// signingIdentity returns the TLS certificate used to sign profiles so iOS
// shows them as "Verified".
func (a *App) signingIdentity() (*x509.Certificate, crypto.Signer, []*x509.Certificate) {
	var c *tls.Certificate
	switch {
	case a.acme != nil:
		c, _ = a.acme.GetCertificate(&tls.ClientHelloInfo{ServerName: a.Cfg.ACMEDomains[0]})
	case a.Cfg.TLSCert != "":
		_ = a.loadTLS()
		a.tlsMu.RLock()
		c = a.tlsCert
		a.tlsMu.RUnlock()
	}
	if c == nil || len(c.Certificate) == 0 {
		return nil, nil, nil
	}
	leaf := c.Leaf
	if leaf == nil {
		leaf, _ = x509.ParseCertificate(c.Certificate[0])
	}
	signer, ok := c.PrivateKey.(crypto.Signer)
	if !ok || leaf == nil {
		return nil, nil, nil
	}
	var chain []*x509.Certificate
	for _, der := range c.Certificate[1:] {
		if ic, err := x509.ParseCertificate(der); err == nil {
			chain = append(chain, ic)
		}
	}
	return leaf, signer, chain
}

// Handler builds the HTTP routing tree.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	// device-facing MDM protocol
	mux.HandleFunc("PUT /mdm/checkin", a.MDM.HandleCheckin)
	mux.HandleFunc("PUT /mdm/connect", a.MDM.HandleConnect)
	mux.Handle("/scep", &scepsrv.Handler{Store: a.Store, CA: a.MDM.CA, Log: a.Log,
		Validity: func() time.Duration {
			return time.Duration(a.MDM.SettingInt(mdm.SettingSCEPValidityDays)) * 24 * time.Hour
		}})
	mux.HandleFunc("POST /mdm/ade/enroll", a.DEP.EnrollHandler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.DB().PingContext(r.Context()); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	})
	// end-user pages, companion agent API and app downloads
	portal.Register(mux, portal.Deps{Store: a.Store, MDM: a.MDM, Log: a.Log, TrustProxy: a.Cfg.TrustProxy, DataDir: a.Cfg.DataDir})
	// administration API and console
	a.API.Register(mux)
	mux.Handle("/", web.Handler())
	return a.middleware(mux)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (a *App) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: 200}
		defer func() {
			if rec := recover(); rec != nil {
				a.Log.Error("panic", "path", r.URL.Path, "panic", rec)
				http.Error(sw, "internal error", http.StatusInternalServerError)
			}
			lvl := slog.LevelDebug
			if sw.status >= 500 {
				lvl = slog.LevelWarn
			}
			a.Log.Log(r.Context(), lvl, "http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start).Round(time.Millisecond), "ip", mdm.ClientIP(r, a.Cfg.TrustProxy))
		}()
		h := sw.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		if !strings.HasPrefix(r.URL.Path, "/mdm/") && r.URL.Path != "/scep" {
			h.Set("X-Frame-Options", "DENY")
			h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data: https:; style-src 'self' 'unsafe-inline'; script-src 'self'; frame-src https://www.openstreetmap.org; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		}
		if a.Cfg.TLSEnabled() || strings.HasPrefix(a.MDM.PublicURL(), "https://") {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(sw, r)
	})
}

// Run serves HTTP(S) and runs background jobs until ctx is cancelled.
func (a *App) Run(ctx context.Context) error {
	handler := a.Handler()
	srv := &http.Server{
		Addr:              a.Cfg.Listen,
		Handler:           handler,
		ReadHeaderTimeout: 20 * time.Second,
		ReadTimeout:       10 * time.Minute,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(a.Log.Handler(), slog.LevelDebug),
	}
	var servers []*http.Server
	errc := make(chan error, 3)
	switch {
	case a.acme != nil:
		srv.TLSConfig = a.acme.TLSConfig()
		srv.TLSConfig.MinVersion = tls.VersionTLS12
	case a.Cfg.TLSCert != "":
		srv.TLSConfig = &tls.Config{GetCertificate: a.getCertificate, MinVersion: tls.VersionTLS12}
	}
	servers = append(servers, srv)
	go func() {
		a.Log.Info("Orchard MDM listening", "addr", a.Cfg.Listen, "tls", a.tlsMode(), "url", a.MDM.PublicURL(), "version", Version)
		var err error
		if srv.TLSConfig != nil {
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()
	if a.Cfg.HTTPRedirect != "" {
		var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := a.MDM.PublicURL()
			if target == "" {
				target = "https://" + strings.Split(r.Host, ":")[0]
			}
			http.Redirect(w, r, target+r.URL.RequestURI(), http.StatusMovedPermanently)
		})
		if a.acme != nil {
			h = a.acme.HTTPHandler(h)
		}
		rs := &http.Server{Addr: a.Cfg.HTTPRedirect, Handler: h, ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, rs)
		go func() {
			if err := rs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}
	if !a.Cfg.DisableSchedul {
		go a.runScheduler(ctx)
	}
	select {
	case <-ctx.Done():
	case err := <-errc:
		return err
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdown)
	}
	a.MDM.FlushPushes()
	return nil
}
