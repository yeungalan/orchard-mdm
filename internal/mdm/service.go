// Package mdm implements the Apple MDM protocol: enrollment profiles, the
// check-in and command endpoints, the command queue and result processing.
package mdm

import (
	"context"
	"crypto"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/apns"
	"github.com/yeungalan/orchard-mdm/internal/ddm"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"howett.net/plist"
)

// CAKeyPairName is the keypairs row for the device identity CA.
const CAKeyPairName = "ca"

// Signer returns the identity used to sign configuration profiles.
type Signer func() (*x509.Certificate, crypto.Signer, []*x509.Certificate)

// Options configures the service.
type Options struct {
	Store            *store.Store
	Bus              *events.Bus
	Push             *apns.Manager
	DDM              *ddm.Service
	Log              *slog.Logger
	PublicURL        func() string
	TrustProxy       bool
	ClientCertHeader string
	PushDelay        time.Duration
}

// Service is the MDM protocol engine.
type Service struct {
	store      *store.Store
	bus        *events.Bus
	push       *apns.Manager
	ddm        *ddm.Service
	log        *slog.Logger
	publicURL  func() string
	trustProxy bool
	certHeader string

	caMu   sync.RWMutex
	caCert *x509.Certificate
	caKey  crypto.Signer

	signerMu sync.RWMutex
	signer   Signer

	pushMu      sync.Mutex
	pushPending map[string]bool
	pushTimer   *time.Timer
	pushDelay   time.Duration

	// OnPush is called after each push attempt (used for tests and metrics).
	OnPush func(apns.Result)
}

// New creates the service and ensures the identity CA exists.
func New(o Options) (*Service, error) {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.PushDelay == 0 {
		o.PushDelay = 750 * time.Millisecond
	}
	s := &Service{
		store: o.Store, bus: o.Bus, push: o.Push, ddm: o.DDM, log: o.Log, publicURL: o.PublicURL,
		trustProxy: o.TrustProxy, certHeader: o.ClientCertHeader, pushPending: map[string]bool{}, pushDelay: o.PushDelay,
	}
	if err := s.loadCA(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Service) loadCA() error {
	kp, err := s.store.GetKeyPair(CAKeyPairName)
	if err == nil {
		cert, err1 := pki.ParseCertPEM([]byte(kp.CertPEM))
		key, err2 := pki.ParseKeyPEM([]byte(kp.KeyPEM))
		if err1 != nil || err2 != nil {
			return fmt.Errorf("stored CA is unreadable: %v %v", err1, err2)
		}
		s.caCert, s.caKey = cert, key
		return nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	org := s.store.GetSetting(SettingOrgName, "Orchard MDM")
	cert, key, err := pki.NewCA(org+" Device CA", org, 10)
	if err != nil {
		return err
	}
	keyPEM, err := pki.KeyPEM(key)
	if err != nil {
		return err
	}
	if err := s.store.PutKeyPair(&store.KeyPair{Name: CAKeyPairName, CertPEM: pki.CertPEM(cert), KeyPEM: keyPEM}); err != nil {
		return err
	}
	s.log.Info("created device identity CA", "subject", cert.Subject.String(), "expires", cert.NotAfter.Format(time.DateOnly))
	s.caCert, s.caKey = cert, key
	return nil
}

// CA returns the device identity certificate authority.
func (s *Service) CA() (*x509.Certificate, crypto.Signer) {
	s.caMu.RLock()
	defer s.caMu.RUnlock()
	return s.caCert, s.caKey
}

// Store exposes the store.
func (s *Service) Store() *store.Store { return s.store }

// Bus exposes the event bus.
func (s *Service) Bus() *events.Bus { return s.bus }

// PushManager exposes the APNs manager.
func (s *Service) PushManager() *apns.Manager { return s.push }

// DDM exposes the declarative management service.
func (s *Service) DDM() *ddm.Service { return s.ddm }

// SetSigner sets the profile signing identity provider.
func (s *Service) SetSigner(sig Signer) {
	s.signerMu.Lock()
	s.signer = sig
	s.signerMu.Unlock()
}

// SigningIdentity returns the identity used to sign profiles (nil if none).
func (s *Service) SigningIdentity() (*x509.Certificate, crypto.Signer, []*x509.Certificate) {
	s.signerMu.RLock()
	sig := s.signer
	s.signerMu.RUnlock()
	if sig == nil {
		return nil, nil, nil
	}
	return sig()
}

// PublicURL returns the external base URL of the server.
func (s *Service) PublicURL() string {
	if s.publicURL != nil {
		if u := strings.TrimRight(s.publicURL(), "/"); u != "" {
			return u
		}
	}
	return strings.TrimRight(s.store.GetSetting(SettingPublicURL, ""), "/")
}

// ---- command queue ----

// Meta describes who/what queued a command.
type Meta struct {
	Source    string // manual|reconcile|system|compliance|portal|api
	Ref       string
	CreatedBy string
}

// ErrForbiddenCommand is returned for commands Orchard refuses to send.
var ErrForbiddenCommand = errors.New("Orchard MDM does not support this command: remote device wipe is intentionally not implemented")

// deniedRequestTypes are never sent to devices. EraseDevice wipes a device and
// DeleteUser removes a Shared iPad user's data; Orchard deliberately has no
// wipe capability (the enrollment profile also withholds the erase access right).
var deniedRequestTypes = map[string]bool{
	"EraseDevice": true,
	"DeleteUser":  true,
}

// IsDenied reports whether a request type is blocked.
func IsDenied(requestType string) bool {
	return deniedRequestTypes[strings.TrimSpace(requestType)]
}

// Enqueue adds a command for a device. cmd is the Command dictionary
// (including RequestType). It does not push; call Push afterwards.
func (s *Service) Enqueue(udid string, cmd map[string]any, meta Meta) (*store.Command, error) {
	rt, _ := cmd["RequestType"].(string)
	if rt == "" {
		return nil, errors.New("command has no RequestType")
	}
	if IsDenied(rt) {
		return nil, ErrForbiddenCommand
	}
	id := pki.NewUUID()
	body, err := plist.MarshalIndent(map[string]any{"CommandUUID": id, "Command": cmd}, plist.XMLFormat, "\t")
	if err != nil {
		return nil, fmt.Errorf("encode command: %w", err)
	}
	c := &store.Command{UUID: id, DeviceID: udid, RequestType: rt, Payload: body, Source: meta.Source, Ref: meta.Ref, CreatedBy: meta.CreatedBy}
	if err := s.store.InsertCommand(c); err != nil {
		return nil, err
	}
	if s.bus != nil {
		s.bus.Publish(events.CommandQueued, udid, map[string]any{"command_uuid": id, "request_type": rt, "source": meta.Source})
	}
	return c, nil
}

// EnqueueAndPush enqueues and schedules a push.
func (s *Service) EnqueueAndPush(udid string, cmd map[string]any, meta Meta) (*store.Command, error) {
	c, err := s.Enqueue(udid, cmd, meta)
	if err == nil {
		s.Push(udid)
	}
	return c, err
}

// ---- push ----

// Push schedules an APNs notification for the devices (debounced).
func (s *Service) Push(udids ...string) {
	if len(udids) == 0 {
		return
	}
	s.pushMu.Lock()
	for _, u := range udids {
		s.pushPending[u] = true
	}
	if s.pushTimer == nil {
		s.pushTimer = time.AfterFunc(s.pushDelay, s.flushPush)
	}
	s.pushMu.Unlock()
}

func (s *Service) flushPush() {
	s.pushMu.Lock()
	ids := make([]string, 0, len(s.pushPending))
	for u := range s.pushPending {
		ids = append(ids, u)
	}
	s.pushPending = map[string]bool{}
	s.pushTimer = nil
	s.pushMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s.PushNow(ctx, ids)
}

// FlushPushes sends any debounced pushes immediately (tests, shutdown).
func (s *Service) FlushPushes() {
	s.pushMu.Lock()
	if s.pushTimer != nil {
		s.pushTimer.Stop()
	}
	s.pushMu.Unlock()
	s.flushPush()
}

// PushNow sends notifications synchronously and returns the results.
func (s *Service) PushNow(ctx context.Context, udids []string) []apns.Result {
	if len(udids) == 0 || s.push == nil {
		return nil
	}
	client, err := s.push.Client()
	if err != nil {
		s.log.Debug("push skipped", "err", err)
		return nil
	}
	var targets []apns.Target
	for _, u := range udids {
		d, err := s.store.GetDevice(u)
		if err != nil || d.EnrollmentStatus != "enrolled" || !d.HasPushToken {
			continue
		}
		targets = append(targets, apns.Target{UDID: d.UDID, Token: d.PushToken, PushMagic: d.PushMagic})
	}
	results := client.PushMany(ctx, targets)
	for _, r := range results {
		if r.Err == nil {
			_ = s.store.UpdateDevice(r.UDID, map[string]any{"last_push": store.Now()})
		} else {
			s.log.Warn("push failed", "udid", r.UDID, "status", r.Status, "reason", r.Reason, "err", r.Err)
			if r.Status == http.StatusGone || r.Reason == "BadDeviceToken" || r.Reason == "Unregistered" {
				s.event(r.UDID, "push.failed", "warn", "APNs rejected the push token ("+r.Reason+"); the device may have been reset or unenrolled", nil)
			}
		}
		if s.OnPush != nil {
			s.OnPush(r)
		}
	}
	return results
}

// event records an activity entry.
func (s *Service) event(udid, typ, level, msg string, details map[string]any) {
	d := ""
	if details != nil {
		d = jsonString(details)
	}
	_ = s.store.InsertEvent(&store.Event{DeviceID: udid, Type: typ, Level: level, Message: msg, Details: d})
}

// ClientIP extracts the caller address.
func ClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			return strings.TrimSpace(strings.Split(xff, ",")[0])
		}
		if xr := r.Header.Get("X-Real-IP"); xr != "" {
			return strings.TrimSpace(xr)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
