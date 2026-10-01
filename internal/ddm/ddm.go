// Package ddm implements the server side of Apple's Declarative Device
// Management protocol (declarations, activations and status reports) that
// runs over the MDM check-in channel.
package ddm

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

// Kinds of declarations.
const (
	KindConfiguration = "configuration"
	KindAsset         = "asset"
	KindActivation    = "activation"
	KindManagement    = "management"
)

// Built-in identifiers.
const (
	StatusSubscriptionID = "orchard.status-subscriptions"
	DefaultActivationID  = "orchard.activation.default"
)

// DefaultStatusItems are the status items every device reports.
var DefaultStatusItems = []string{
	"device.identifier.serial-number",
	"device.identifier.udid",
	"device.model.family",
	"device.model.identifier",
	"device.model.marketing-name",
	"device.operating-system.build-version",
	"device.operating-system.family",
	"device.operating-system.marketing-name",
	"device.operating-system.version",
	"device.operating-system.supplemental.build-version",
	"device.operating-system.supplemental.extra-version",
	"device.power.battery-health",
	"management.client-capabilities",
	"management.declarations",
	"passcode.is-compliant",
	"passcode.is-present",
	"softwareupdate.failure-reason",
	"softwareupdate.install-reason",
	"softwareupdate.install-state",
	"softwareupdate.pending-version",
	"mdm.app",
}

// Item is one resolved declaration for a device.
type Item struct {
	Kind        string          `json:"kind"`
	Type        string          `json:"Type"`
	Identifier  string          `json:"Identifier"`
	ServerToken string          `json:"ServerToken"`
	Payload     json.RawMessage `json:"Payload"`
	Name        string          `json:"name,omitempty"`
}

// Service resolves declarations for devices.
type Service struct {
	store *store.Store
	bus   *events.Bus
}

// New creates a DDM service.
func New(s *store.Store, bus *events.Bus) *Service { return &Service{store: s, bus: bus} }

// KindOf maps a declaration type to its kind.
func KindOf(typ string) string {
	switch {
	case strings.HasPrefix(typ, "com.apple.configuration."):
		return KindConfiguration
	case strings.HasPrefix(typ, "com.apple.asset."):
		return KindAsset
	case strings.HasPrefix(typ, "com.apple.activation."):
		return KindActivation
	case strings.HasPrefix(typ, "com.apple.management."):
		return KindManagement
	}
	return ""
}

// TokenFor computes a server token for a payload.
func TokenFor(typ string, payload []byte) string {
	h := sha256.New()
	h.Write([]byte(typ))
	h.Write([]byte{0})
	h.Write(payload)
	return hex.EncodeToString(h.Sum(nil))[:32]
}

// Supported reports whether a device's OS version supports DDM over MDM
// (iOS/iPadOS 16 and later for device enrollments).
func Supported(d *store.Device) bool {
	v := d.OSVersion
	if v == "" {
		return false
	}
	major := 0
	fmt.Sscanf(v, "%d", &major)
	return major >= 16
}

// DeviceItems returns every declaration that applies to the device.
func (s *Service) DeviceItems(udid string) ([]Item, error) {
	assigns, err := s.store.DeviceAssignments(udid)
	if err != nil {
		return nil, err
	}
	include := map[int64]bool{}
	exclude := map[int64]bool{}
	for _, a := range assigns {
		if a.ItemType != "declaration" {
			continue
		}
		if a.Intent == "exclude" {
			exclude[a.ItemID] = true
		} else {
			include[a.ItemID] = true
		}
	}
	var items []Item
	var configIDs []string
	for id := range include {
		if exclude[id] {
			continue
		}
		d, err := s.store.GetDeclaration(id)
		if err != nil {
			continue
		}
		kind := KindOf(d.Type)
		if kind == "" {
			continue
		}
		items = append(items, Item{Kind: kind, Type: d.Type, Identifier: d.Identifier, ServerToken: d.ServerToken, Payload: d.Payload, Name: d.Name})
		if kind == KindConfiguration {
			configIDs = append(configIDs, d.Identifier)
		}
	}
	// built-in status subscriptions
	subs := make([]map[string]string, 0, len(DefaultStatusItems))
	for _, n := range DefaultStatusItems {
		subs = append(subs, map[string]string{"Name": n})
	}
	subPayload, _ := json.Marshal(map[string]any{"StatusItems": subs})
	subType := "com.apple.configuration.management.status-subscriptions"
	items = append(items, Item{Kind: KindConfiguration, Type: subType, Identifier: StatusSubscriptionID, ServerToken: TokenFor(subType, subPayload), Payload: subPayload, Name: "Status subscriptions"})
	configIDs = append(configIDs, StatusSubscriptionID)

	// one activation that activates every configuration not already
	// referenced by a user-defined activation
	activated := map[string]bool{}
	for _, it := range items {
		if it.Kind != KindActivation {
			continue
		}
		var p struct {
			StandardConfigurations []string `json:"StandardConfigurations"`
		}
		_ = json.Unmarshal(it.Payload, &p)
		for _, c := range p.StandardConfigurations {
			activated[c] = true
		}
	}
	var pending []string
	for _, c := range configIDs {
		if !activated[c] {
			pending = append(pending, c)
		}
	}
	sort.Strings(pending)
	if len(pending) > 0 {
		actType := "com.apple.activation.simple"
		actPayload, _ := json.Marshal(map[string]any{"StandardConfigurations": pending})
		items = append(items, Item{Kind: KindActivation, Type: actType, Identifier: DefaultActivationID, ServerToken: TokenFor(actType, actPayload), Payload: actPayload, Name: "Default activation"})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Identifier < items[j].Identifier })
	return items, nil
}

// DeclarationsToken summarises the device's declaration set.
func DeclarationsToken(items []Item) string {
	h := sha256.New()
	for _, it := range items {
		h.Write([]byte(it.Identifier))
		h.Write([]byte{0})
		h.Write([]byte(it.ServerToken))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:40]
}

// Token returns the current declarations token for a device.
func (s *Service) Token(udid string) (string, error) {
	items, err := s.DeviceItems(udid)
	if err != nil {
		return "", err
	}
	return DeclarationsToken(items), nil
}

// SyncTokensJSON builds the SyncTokens document.
func SyncTokensJSON(token string) []byte {
	b, _ := json.Marshal(map[string]any{
		"SyncTokens": map[string]any{
			"DeclarationsToken": token,
			"Timestamp":         time.Now().UTC().Format(time.RFC3339),
		},
	})
	return b
}

// ErrUnknownEndpoint is returned for unsupported DDM endpoints.
var ErrUnknownEndpoint = errors.New("ddm: unknown endpoint")

// ErrNotFound is returned when a declaration is not assigned to the device.
var ErrNotFound = errors.New("ddm: declaration not found")

// Handle serves a DeclarativeManagement check-in request and returns the JSON body.
func (s *Service) Handle(udid, endpoint string, data []byte) ([]byte, error) {
	switch {
	case endpoint == "tokens":
		tok, err := s.Token(udid)
		if err != nil {
			return nil, err
		}
		return SyncTokensJSON(tok), nil
	case endpoint == "declaration-items":
		items, err := s.DeviceItems(udid)
		if err != nil {
			return nil, err
		}
		groups := map[string][]map[string]string{
			"Activations": {}, "Assets": {}, "Configurations": {}, "Management": {},
		}
		key := map[string]string{KindActivation: "Activations", KindAsset: "Assets", KindConfiguration: "Configurations", KindManagement: "Management"}
		for _, it := range items {
			groups[key[it.Kind]] = append(groups[key[it.Kind]], map[string]string{"Identifier": it.Identifier, "ServerToken": it.ServerToken})
		}
		return json.Marshal(map[string]any{"Declarations": groups, "DeclarationsToken": DeclarationsToken(items)})
	case strings.HasPrefix(endpoint, "declaration/"):
		parts := strings.SplitN(strings.TrimPrefix(endpoint, "declaration/"), "/", 2)
		if len(parts) != 2 {
			return nil, ErrUnknownEndpoint
		}
		items, err := s.DeviceItems(udid)
		if err != nil {
			return nil, err
		}
		for _, it := range items {
			if it.Identifier == parts[1] && it.Kind == parts[0] {
				return json.Marshal(map[string]any{"Type": it.Type, "Identifier": it.Identifier, "ServerToken": it.ServerToken, "Payload": it.Payload})
			}
		}
		return nil, ErrNotFound
	case endpoint == "status":
		return nil, s.ProcessStatus(udid, data)
	}
	return nil, ErrUnknownEndpoint
}

// ProcessStatus stores a status report from the device.
func (s *Service) ProcessStatus(udid string, data []byte) error {
	var report struct {
		StatusItems map[string]any `json:"StatusItems"`
		Errors      []any          `json:"Errors"`
		FullReport  bool           `json:"FullReport"`
	}
	if err := json.Unmarshal(data, &report); err != nil {
		return fmt.Errorf("ddm status: %w", err)
	}
	d, err := s.store.GetDevice(udid)
	if err != nil {
		return err
	}
	merged := d.DDMStatus
	if merged == nil || report.FullReport {
		merged = map[string]any{}
	}
	deepMerge(merged, report.StatusItems)
	if len(report.Errors) > 0 {
		merged["_errors"] = report.Errors
	}
	fields := map[string]any{"ddm_status_json": merged, "ddm_last_status": store.Now()}

	if v, ok := lookup(report.StatusItems, "device", "power", "battery-health").(string); ok {
		fields["battery_health"] = v
	}
	if v, ok := lookup(report.StatusItems, "device", "operating-system", "version").(string); ok && v != "" {
		fields["os_version"] = v
	}
	if v, ok := lookup(report.StatusItems, "device", "operating-system", "build-version").(string); ok && v != "" {
		fields["build_version"] = v
	}
	if v, ok := lookup(report.StatusItems, "passcode", "is-present").(bool); ok {
		fields["passcode_present"] = v
	}
	if v, ok := lookup(report.StatusItems, "passcode", "is-compliant").(bool); ok {
		fields["passcode_compliant"] = v
	}
	if err := s.store.UpdateDevice(udid, fields); err != nil {
		return err
	}
	if decl, ok := lookup(report.StatusItems, "management", "declarations").(map[string]any); ok {
		for _, kind := range []string{"activations", "configurations", "assets", "management"} {
			list, _ := decl[kind].([]any)
			for _, raw := range list {
				m, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				st := store.DeclarationStatus{}
				st.Identifier, _ = m["identifier"].(string)
				st.Active, _ = m["active"].(bool)
				st.Valid, _ = m["valid"].(string)
				st.ServerToken, _ = m["server-token"].(string)
				st.Reasons = m["reasons"]
				if st.Identifier != "" {
					_ = s.store.UpsertDeclarationStatus(udid, st)
				}
			}
		}
	}
	if s.bus != nil {
		s.bus.Publish(events.DDMStatus, udid, map[string]any{"full_report": report.FullReport, "errors": len(report.Errors)})
	}
	return nil
}

func lookup(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = mm[p]
	}
	return cur
}

func deepMerge(dst, src map[string]any) {
	for k, v := range src {
		if sv, ok := v.(map[string]any); ok {
			if dv, ok := dst[k].(map[string]any); ok {
				deepMerge(dv, sv)
				continue
			}
		}
		dst[k] = v
	}
}
