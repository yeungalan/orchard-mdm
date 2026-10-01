// Package webhook delivers events to external HTTP endpoints.
package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

// Dispatcher posts events to configured webhooks.
type Dispatcher struct {
	store *store.Store
	log   *slog.Logger
	http  *http.Client
}

// New creates a dispatcher.
func New(s *store.Store, log *slog.Logger) *Dispatcher {
	return &Dispatcher{store: s, log: log, http: &http.Client{Timeout: 10 * time.Second}}
}

// Payload is the JSON body sent to webhooks.
type Payload struct {
	Event     string         `json:"event"`
	Timestamp time.Time      `json:"timestamp"`
	DeviceID  string         `json:"device_id,omitempty"`
	Device    map[string]any `json:"device,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// Subscribe registers the dispatcher on the bus.
func (d *Dispatcher) Subscribe(bus *events.Bus) {
	bus.Subscribe(func(e events.Event) { d.Dispatch(e) }, events.AllTypes...)
}

// Dispatch delivers an event to every matching webhook.
func (d *Dispatcher) Dispatch(e events.Event) {
	hooks, err := d.store.ListWebhooks()
	if err != nil {
		return
	}
	var targets []*store.Webhook
	for _, h := range hooks {
		if h.Enabled && (len(h.Events) == 0 || slices.Contains(h.Events, e.Type)) {
			targets = append(targets, h)
		}
	}
	if len(targets) == 0 {
		return
	}
	p := Payload{Event: e.Type, Timestamp: e.Time, DeviceID: e.DeviceID, Data: e.Data}
	if e.DeviceID != "" {
		if dev, err := d.store.GetDevice(e.DeviceID); err == nil {
			p.Device = map[string]any{
				"udid": dev.UDID, "serial_number": dev.SerialNumber, "device_name": dev.DeviceName, "product_name": dev.ProductName,
				"os_version": dev.OSVersion, "enrollment_status": dev.EnrollmentStatus, "compliance": dev.Compliance, "assigned_user": dev.AssignedUser,
			}
		}
	}
	body, _ := json.Marshal(p)
	for _, h := range targets {
		status, err := d.send(h, e.Type, body)
		errText := ""
		if err != nil {
			errText = err.Error()
			d.log.Warn("webhook delivery failed", "webhook", h.Name, "event", e.Type, "err", err)
		}
		d.store.RecordWebhookDelivery(h.ID, status, errText)
	}
}

// Sign computes the X-Orchard-Signature header value.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func (d *Dispatcher) send(h *store.Webhook, event string, body []byte) (int, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * 2 * time.Second)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.URL, bytes.NewReader(body))
		if err != nil {
			cancel()
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "orchard-mdm-webhook/1.0")
		req.Header.Set("X-Orchard-Event", event)
		if h.Secret != "" {
			req.Header.Set("X-Orchard-Signature", Sign(h.Secret, body))
		}
		resp, err := d.http.Do(req)
		if err != nil {
			cancel()
			lastErr = err
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		cancel()
		if resp.StatusCode < 300 {
			return resp.StatusCode, nil
		}
		lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
		if resp.StatusCode < 500 {
			return resp.StatusCode, lastErr
		}
	}
	return 0, lastErr
}

// Test sends a test event to one webhook.
func (d *Dispatcher) Test(h *store.Webhook) (int, error) {
	body, _ := json.Marshal(Payload{Event: "webhook.test", Timestamp: time.Now().UTC(), Data: map[string]any{"message": "Hello from Orchard MDM"}})
	status, err := d.send(h, "webhook.test", body)
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	d.store.RecordWebhookDelivery(h.ID, status, errText)
	return status, err
}
