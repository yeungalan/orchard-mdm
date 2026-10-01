// Package events is a tiny in-process publish/subscribe bus used to decouple
// the MDM protocol handlers from reconciliation, compliance and webhooks.
package events

import (
	"log/slog"
	"sync"
	"time"
)

// Event types.
const (
	DeviceEnrolled      = "device.enrolled"
	DeviceUnenrolled    = "device.unenrolled"
	DeviceTokenUpdate   = "device.token_update"
	DeviceInventory     = "device.inventory"
	DeviceUpdated       = "device.updated"
	DeviceLocation      = "device.location"
	DeviceTelemetry     = "device.telemetry"
	CommandQueued       = "command.queued"
	CommandCompleted    = "command.completed"
	CommandFailed       = "command.failed"
	ComplianceChanged   = "compliance.changed"
	AssignmentsChanged  = "assignments.changed"
	GroupsChanged       = "groups.changed"
	ADEDeviceAdded      = "ade.device_added"
	ADEDeviceRemoved    = "ade.device_removed"
	DDMStatus           = "ddm.status"
	PushCertExpiring    = "apns.cert_expiring"
	ProfileChanged      = "profile.changed"
	AppChanged          = "app.changed"
	DeclarationsChanged = "declarations.changed"
)

// AllTypes lists event types that can be subscribed to by webhooks.
var AllTypes = []string{
	DeviceEnrolled, DeviceUnenrolled, DeviceTokenUpdate, DeviceInventory, DeviceLocation, DeviceTelemetry,
	CommandCompleted, CommandFailed, ComplianceChanged, ADEDeviceAdded, ADEDeviceRemoved, DDMStatus, PushCertExpiring,
}

// Event is something that happened.
type Event struct {
	Type     string         `json:"event"`
	Time     time.Time      `json:"timestamp"`
	DeviceID string         `json:"device_id,omitempty"`
	Data     map[string]any `json:"data,omitempty"`
}

// Handler receives events.
type Handler func(Event)

// Bus dispatches events to subscribers asynchronously.
type Bus struct {
	mu   sync.RWMutex
	subs []sub
	wg   sync.WaitGroup
	sync bool
}

type sub struct {
	types map[string]bool
	h     Handler
}

// New creates a bus. When synchronous is true, handlers run inline (tests).
func New(synchronous bool) *Bus { return &Bus{sync: synchronous} }

// Subscribe registers a handler for the given types (all types when none given).
func (b *Bus) Subscribe(h Handler, types ...string) {
	s := sub{h: h}
	if len(types) > 0 {
		s.types = map[string]bool{}
		for _, t := range types {
			s.types[t] = true
		}
	}
	b.mu.Lock()
	b.subs = append(b.subs, s)
	b.mu.Unlock()
}

// Publish emits an event.
func (b *Bus) Publish(typ, deviceID string, data map[string]any) {
	e := Event{Type: typ, Time: time.Now().UTC(), DeviceID: deviceID, Data: data}
	b.mu.RLock()
	subs := append([]sub(nil), b.subs...)
	b.mu.RUnlock()
	for _, s := range subs {
		if s.types != nil && !s.types[typ] {
			continue
		}
		if b.sync {
			safeCall(s.h, e)
			continue
		}
		b.wg.Add(1)
		go func(h Handler) {
			defer b.wg.Done()
			safeCall(h, e)
		}(s.h)
	}
}

// Wait blocks until in-flight handlers finish.
func (b *Bus) Wait() { b.wg.Wait() }

func safeCall(h Handler, e Event) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("event handler panic", "event", e.Type, "panic", r)
		}
	}()
	h(e)
}
