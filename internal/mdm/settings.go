package mdm

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"strings"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/store"
)

// Setting keys shared across packages.
const (
	SettingOrgName             = "org_name"
	SettingPublicURL           = "public_url"
	SettingSupportEmail        = "support_email"
	SettingSupportPhone        = "support_phone"
	SettingSupportURL          = "support_url"
	SettingEnrollRequireToken  = "enroll_require_token"
	SettingInventoryHours      = "inventory_interval_hours"
	SettingTelemetryMinutes    = "telemetry_interval_minutes"
	SettingLostModeLocMinutes  = "lost_mode_location_minutes"
	SettingSignProfiles        = "sign_profiles"
	SettingSCEPValidityDays    = "scep_validity_days"
	SettingCommandRetention    = "command_retention_days"
	SettingTelemetryRetention  = "telemetry_retention_days"
	SettingPortalEnabled       = "portal_enabled"
	SettingADESyncMinutes      = "ade_sync_minutes"
	SettingExtraTrustCerts     = "extra_trust_certs"
	SettingMDMIdentifier       = "mdm_identifier"
	SettingEnrollConsentText   = "enroll_consent_text"
	SettingPortalWebClipID     = "portal_webclip_identifier"
	SettingCommandExpiryDays   = "command_expiry_days"
	SettingRecordConnectionIPs = "record_connection_ips"
)

// SettingDefaults are applied when a key is unset.
var SettingDefaults = map[string]string{
	SettingOrgName:             "Orchard MDM",
	SettingEnrollRequireToken:  "1",
	SettingInventoryHours:      "4",
	SettingTelemetryMinutes:    "60",
	SettingLostModeLocMinutes:  "15",
	SettingSignProfiles:        "1",
	SettingSCEPValidityDays:    "730",
	SettingCommandRetention:    "90",
	SettingTelemetryRetention:  "90",
	SettingPortalEnabled:       "1",
	SettingADESyncMinutes:      "30",
	SettingCommandExpiryDays:   "30",
	SettingRecordConnectionIPs: "1",
}

// Setting returns a setting with its default applied.
func (s *Service) Setting(key string) string {
	return s.store.GetSetting(key, SettingDefaults[key])
}

// SettingInt returns an integer setting with its default applied.
func (s *Service) SettingInt(key string) int {
	var n int
	if _, err := fmt.Sscanf(s.Setting(key), "%d", &n); err != nil {
		return 0
	}
	return n
}

// SettingBool returns a boolean setting with its default applied.
func (s *Service) SettingBool(key string) bool {
	v := s.Setting(key)
	return v == "1" || v == "true"
}

// MDMIdentifier is the stable PayloadIdentifier prefix of the enrollment profile.
func (s *Service) MDMIdentifier() string {
	id := s.store.GetSetting(SettingMDMIdentifier, "")
	if id == "" {
		id = "com.orchardmdm.enrollment." + strings.ToLower(randomToken(4))
		_ = s.store.SetSetting(SettingMDMIdentifier, id)
	}
	return id
}

// ---- variables ----

// Vars returns the substitution variables for a device. They can be used as
// {{name}} in configuration profiles and managed app configuration.
func (s *Service) Vars(d *store.Device) map[string]string {
	last4 := d.UDID
	if len(last4) > 4 {
		last4 = last4[len(last4)-4:]
	}
	return map[string]string{
		"device.udid":          d.UDID,
		"device.udid_last4":    last4,
		"device.serial":        d.SerialNumber,
		"device.name":          d.DeviceName,
		"device.model":         d.ProductName,
		"device.model_name":    d.ModelName,
		"device.os_version":    d.OSVersion,
		"device.imei":          d.IMEI,
		"device.meid":          d.MEID,
		"device.wifi_mac":      d.WiFiMAC,
		"device.bluetooth_mac": d.BluetoothMAC,
		"device.phone":         d.PhoneNumber,
		"device.asset_tag":     d.AssetTag,
		"device.agent_token":   d.AgentToken,
		"device.ownership":     d.Ownership,
		"device.portal_url":    s.PublicURL() + "/portal/" + d.PortalToken,
		"user.name":            d.AssignedUser,
		"user.email":           d.AssignedEmail,
		"org.name":             s.Setting(SettingOrgName),
		"server.url":           s.PublicURL(),
		"server.agent_url":     s.PublicURL() + "/agent/v1/report",
	}
}

// VariableNames lists the supported variables (for the UI).
func VariableNames() []string {
	return []string{"device.udid", "device.udid_last4", "device.serial", "device.name", "device.model", "device.model_name", "device.os_version",
		"device.imei", "device.meid", "device.wifi_mac", "device.bluetooth_mac", "device.phone", "device.asset_tag", "device.agent_token", "device.ownership", "device.portal_url",
		"user.name", "user.email", "org.name", "server.url", "server.agent_url"}
}

// Expand replaces {{var}} placeholders. When xmlEscape is set values are
// escaped for inclusion in XML.
func Expand(text string, vars map[string]string, xmlEscape bool) string {
	if !strings.Contains(text, "{{") {
		return text
	}
	var b strings.Builder
	for {
		i := strings.Index(text, "{{")
		if i < 0 {
			b.WriteString(text)
			break
		}
		j := strings.Index(text[i:], "}}")
		if j < 0 {
			b.WriteString(text)
			break
		}
		name := strings.TrimSpace(text[i+2 : i+j])
		b.WriteString(text[:i])
		if v, ok := vars[name]; ok {
			if xmlEscape {
				v = html.EscapeString(v)
			}
			b.WriteString(v)
		} else {
			b.WriteString(text[i : i+j+2])
		}
		text = text[i+j+2:]
	}
	return b.String()
}

// ExpandValue walks a decoded JSON/plist value and expands strings.
func ExpandValue(v any, vars map[string]string) any {
	switch t := v.(type) {
	case string:
		return Expand(t, vars, false)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = ExpandValue(vv, vars)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = ExpandValue(vv, vars)
		}
		return out
	}
	return v
}

// ---- value helpers for decoded plists ----

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	case []byte:
		return string(t)
	}
	return fmt.Sprint(v)
}

func asBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case uint64:
		return t != 0
	case int64:
		return t != 0
	case string:
		return t == "true" || t == "1"
	}
	return false
}

func asFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case uint64:
		return float64(t), true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	}
	return 0, false
}

func asInt(v any) int64 {
	f, _ := asFloat(v)
	return int64(f)
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// jsonSafe converts plist-decoded values (with []byte and time.Time) to
// JSON-friendly values.
func jsonSafe(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			out[k] = jsonSafe(vv)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = jsonSafe(vv)
		}
		return out
	case []byte:
		return fmt.Sprintf("<data %d bytes>", len(t))
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	}
	return v
}

func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
