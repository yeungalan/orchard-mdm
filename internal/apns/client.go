// Package apns sends MDM push notifications through the Apple Push
// Notification service and manages the MDM push certificate lifecycle.
package apns

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"golang.org/x/net/http2"
)

// KeyPairName is the keypairs row holding the active push certificate.
const KeyPairName = "apns"

// Target is a device to notify.
type Target struct {
	UDID      string
	Token     string // hex device token
	PushMagic string
}

// Result is the outcome of one push.
type Result struct {
	UDID   string
	Status int
	Reason string
	ID     string
	Err    error
}

// Client sends pushes using one certificate.
type Client struct {
	http    *http.Client
	baseURL string
	topic   string
	cert    *x509.Certificate
}

// NewClient builds an HTTP/2 APNs client.
func NewClient(certPEM, keyPEM []byte, baseURL string) (*Client, error) {
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("load push certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	topic, err := pki.TopicFromCert(leaf)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12},
		ForceAttemptHTTP2:   true,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     5 * time.Minute,
	}
	if err := http2.ConfigureTransport(tr); err != nil {
		return nil, err
	}
	return &Client{
		http:    &http.Client{Transport: tr, Timeout: 20 * time.Second},
		baseURL: strings.TrimRight(baseURL, "/"),
		topic:   topic,
		cert:    leaf,
	}, nil
}

// Topic returns the push topic of the certificate.
func (c *Client) Topic() string { return c.topic }

// Push sends one MDM notification.
func (c *Client) Push(ctx context.Context, t Target) Result {
	res := Result{UDID: t.UDID}
	if t.Token == "" || t.PushMagic == "" {
		res.Err = errors.New("device has no push token")
		return res
	}
	body, _ := json.Marshal(map[string]string{"mdm": t.PushMagic})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/3/device/"+t.Token, bytes.NewReader(body))
	if err != nil {
		res.Err = err
		return res
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("apns-topic", c.topic)
	req.Header.Set("apns-push-type", "mdm")
	req.Header.Set("apns-priority", "10")
	req.Header.Set("apns-expiration", fmt.Sprint(time.Now().Add(72*time.Hour).Unix()))
	resp, err := c.http.Do(req)
	if err != nil {
		res.Err = err
		return res
	}
	defer resp.Body.Close()
	res.Status = resp.StatusCode
	res.ID = resp.Header.Get("apns-id")
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Reason string `json:"reason"`
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = json.Unmarshal(b, &e)
		res.Reason = e.Reason
		res.Err = fmt.Errorf("apns: %d %s", resp.StatusCode, e.Reason)
	}
	return res
}

// PushMany sends notifications concurrently.
func (c *Client) PushMany(ctx context.Context, targets []Target) []Result {
	out := make([]Result, len(targets))
	sem := make(chan struct{}, 16)
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int, t Target) {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = c.Push(ctx, t)
		}(i, t)
	}
	wg.Wait()
	return out
}

// Manager holds the current push client, rebuilding it when the stored
// certificate changes.
type Manager struct {
	store   *store.Store
	baseURL string

	mu     sync.Mutex
	client *Client
	loaded string // cert PEM the client was built from
}

// NewManager creates a manager.
func NewManager(s *store.Store, baseURL string) *Manager {
	return &Manager{store: s, baseURL: baseURL}
}

// ErrNoCertificate means no push certificate has been configured.
var ErrNoCertificate = errors.New("no APNs push certificate configured")

// Client returns a client for the stored certificate.
func (m *Manager) Client() (*Client, error) {
	kp, err := m.store.GetKeyPair(KeyPairName)
	if err != nil || kp.CertPEM == "" || kp.KeyPEM == "" {
		return nil, ErrNoCertificate
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil && m.loaded == kp.CertPEM {
		return m.client, nil
	}
	c, err := NewClient([]byte(kp.CertPEM), []byte(kp.KeyPEM), m.baseURL)
	if err != nil {
		return nil, err
	}
	m.client, m.loaded = c, kp.CertPEM
	return c, nil
}

// Topic returns the configured push topic or "" when none.
func (m *Manager) Topic() string {
	c, err := m.Client()
	if err != nil {
		return ""
	}
	return c.Topic()
}

// Info describes the active push certificate.
type Info struct {
	Configured bool      `json:"configured"`
	Topic      string    `json:"topic"`
	Subject    string    `json:"subject"`
	Issuer     string    `json:"issuer"`
	NotBefore  time.Time `json:"not_before"`
	NotAfter   time.Time `json:"not_after"`
	DaysLeft   int       `json:"days_left"`
	Serial     string    `json:"serial"`
	AppleID    string    `json:"apple_id"`
	Error      string    `json:"error,omitempty"`
}

// CertInfo returns information about the active certificate.
func (m *Manager) CertInfo() Info {
	kp, err := m.store.GetKeyPair(KeyPairName)
	if err != nil || kp.CertPEM == "" {
		return Info{}
	}
	c, err := pki.ParseCertPEM([]byte(kp.CertPEM))
	if err != nil {
		return Info{Configured: true, Error: err.Error()}
	}
	topic, _ := pki.TopicFromCert(c)
	var meta struct {
		AppleID string `json:"apple_id"`
	}
	_ = json.Unmarshal([]byte(kp.Meta), &meta)
	return Info{
		Configured: true,
		Topic:      topic,
		Subject:    c.Subject.CommonName,
		Issuer:     c.Issuer.CommonName,
		NotBefore:  c.NotBefore,
		NotAfter:   c.NotAfter,
		DaysLeft:   int(time.Until(c.NotAfter).Hours() / 24),
		Serial:     c.SerialNumber.Text(16),
		AppleID:    meta.AppleID,
	}
}
