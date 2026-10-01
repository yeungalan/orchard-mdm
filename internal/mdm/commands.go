package mdm

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/yeungalan/orchard-mdm/internal/store"
	"howett.net/plist"
)

// Param describes one input of a catalog command.
type Param struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"` // string|text|int|bool|enum|stringlist|file|image|app|profile
	Required bool     `json:"required,omitempty"`
	Options  []string `json:"options,omitempty"`
	Default  any      `json:"default,omitempty"`
	Help     string   `json:"help,omitempty"`
}

// Params are user supplied values (decoded JSON).
type Params map[string]any

// String returns a trimmed string parameter.
func (p Params) String(k string) string {
	switch v := p[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case nil:
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(p[k]))
}

// Bool returns a boolean parameter.
func (p Params) Bool(k string) bool {
	switch v := p[k].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "on"
	case float64:
		return v != 0
	}
	return false
}

// Has reports whether a parameter was supplied.
func (p Params) Has(k string) bool {
	v, ok := p[k]
	if !ok || v == nil {
		return false
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) == "" {
		return false
	}
	return true
}

// Int returns an integer parameter.
func (p Params) Int(k string) (int64, bool) {
	switch v := p[k].(type) {
	case float64:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	case int:
		return int64(v), true
	case int64:
		return v, true
	}
	return 0, false
}

// Strings returns a list parameter (array or newline/comma separated text).
func (p Params) Strings(k string) []string {
	var out []string
	switch v := p[k].(type) {
	case []any:
		for _, x := range v {
			if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
				out = append(out, s)
			}
		}
	case []string:
		for _, s := range v {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, s := range strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == ',' }) {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// Data returns a base64 encoded binary parameter (data: URLs accepted).
func (p Params) Data(k string) ([]byte, error) {
	s := p.String(k)
	if s == "" {
		return nil, nil
	}
	if i := strings.Index(s, ";base64,"); strings.HasPrefix(s, "data:") && i > 0 {
		s = s[i+8:]
	}
	return base64.StdEncoding.DecodeString(s)
}

// BuildContext gives builders access to the device and server.
type BuildContext struct {
	Service *Service
	Device  *store.Device
}

// Spec is a command the console can send.
type Spec struct {
	ID          string  `json:"id"`
	RequestType string  `json:"request_type"`
	Label       string  `json:"label"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Params      []Param `json:"params"`
	Supervised  bool    `json:"supervised,omitempty"`
	Confirm     bool    `json:"confirm,omitempty"`
	Bulk        bool    `json:"bulk"`
	build       func(c *BuildContext, p Params) (map[string]any, error)
}

func req(rt string) map[string]any { return map[string]any{"RequestType": rt} }

func simple(rt string) func(*BuildContext, Params) (map[string]any, error) {
	return func(*BuildContext, Params) (map[string]any, error) { return req(rt), nil }
}

func settingsCmd(items ...map[string]any) map[string]any {
	list := make([]any, len(items))
	for i, it := range items {
		list[i] = it
	}
	return map[string]any{"RequestType": "Settings", "Settings": list}
}

// DeviceInformationQueries are requested during inventory.
var DeviceInformationQueries = []string{
	"UDID", "DeviceName", "OSVersion", "BuildVersion", "ModelName", "Model", "ModelNumber", "ProductName", "SerialNumber",
	"DeviceCapacity", "AvailableDeviceCapacity", "BatteryLevel", "CellularTechnology", "IMEI", "MEID", "ModemFirmwareVersion",
	"IsSupervised", "IsDeviceLocatorServiceEnabled", "IsActivationLockEnabled", "IsDoNotDisturbInEffect", "IsCloudBackupEnabled",
	"LastCloudBackupDate", "AwaitingConfiguration", "iTunesStoreAccountIsActive", "iTunesStoreAccountHash",
	"WiFiMAC", "BluetoothMAC", "EthernetMACs", "CurrentCarrierNetwork", "SubscriberCarrierNetwork", "CarrierSettingsVersion",
	"CurrentMCC", "CurrentMNC", "SubscriberMCC", "SubscriberMNC", "PhoneNumber", "ICCID", "DataRoamingEnabled", "VoiceRoamingEnabled",
	"IsRoaming", "PersonalHotspotEnabled", "IsNetworkTethered", "ServiceSubscriptions", "IsMDMLostModeEnabled", "MaximumResidentUsers",
	"IsMultiUser", "EstimatedResidentUsers", "QuotaSize", "ResidentUsers", "UserSessionTimeout", "TemporarySessionOnly",
	"TemporarySessionTimeout", "Languages", "Locales", "DeviceID", "EASDeviceIdentifier", "OrganizationInfo", "MDMOptions",
	"TimeZone", "SupplementalBuildVersion", "SupplementalOSVersionExtra", "SoftwareUpdateDeviceID", "DiagnosticSubmissionEnabled",
	"AppAnalyticsEnabled", "IsAppleSilicon", "HasBattery", "PINRequiredForEraseDevice", "PINRequiredForDeviceLock",
	"SystemIntegrityProtectionEnabled", "ProvisioningUDID", "SkipLanguageAndLocaleSetupForNewUsers", "EID",
}

// TelemetryQueries is the lightweight query set used for frequent battery /
// storage / network sampling.
var TelemetryQueries = []string{
	"BatteryLevel", "AvailableDeviceCapacity", "DeviceCapacity", "CurrentCarrierNetwork", "SubscriberCarrierNetwork", "CellularTechnology",
	"IsRoaming", "DataRoamingEnabled", "PersonalHotspotEnabled", "IsNetworkTethered", "OSVersion", "BuildVersion", "DeviceName",
	"IsMDMLostModeEnabled", "ServiceSubscriptions", "WiFiMAC", "IsDoNotDisturbInEffect",
}

var installActions = []string{"Default", "DownloadOnly", "InstallASAP", "NotifyOnly", "InstallLater"}

// Catalog lists every command available in the console.
var Catalog = []*Spec{
	// ---- inventory ----
	{ID: "DeviceInformation", RequestType: "DeviceInformation", Label: "Device information", Category: "Inventory", Bulk: true,
		Description: "Hardware, OS, battery, storage, network and carrier details.",
		build: func(*BuildContext, Params) (map[string]any, error) {
			return map[string]any{"RequestType": "DeviceInformation", "Queries": DeviceInformationQueries}, nil
		}},
	{ID: "SecurityInfo", RequestType: "SecurityInfo", Label: "Security information", Category: "Inventory", Bulk: true,
		Description: "Passcode presence/compliance, hardware encryption and management status.", build: simple("SecurityInfo")},
	{ID: "InstalledApplicationList", RequestType: "InstalledApplicationList", Label: "Installed apps", Category: "Inventory", Bulk: true,
		Description: "All apps installed on the device.",
		Params:      []Param{{Key: "ManagedAppsOnly", Label: "Managed apps only", Type: "bool"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			c := req("InstalledApplicationList")
			if p.Bool("ManagedAppsOnly") {
				c["ManagedAppsOnly"] = true
			}
			return c, nil
		}},
	{ID: "ManagedApplicationList", RequestType: "ManagedApplicationList", Label: "Managed apps", Category: "Inventory", Bulk: true,
		Description: "Status of apps installed by MDM.", build: simple("ManagedApplicationList")},
	{ID: "ProfileList", RequestType: "ProfileList", Label: "Installed profiles", Category: "Inventory", Bulk: true,
		Description: "Configuration profiles installed on the device.", build: simple("ProfileList")},
	{ID: "CertificateList", RequestType: "CertificateList", Label: "Certificates", Category: "Inventory", Bulk: true,
		Description: "Certificates installed on the device.", build: simple("CertificateList")},
	{ID: "ProvisioningProfileList", RequestType: "ProvisioningProfileList", Label: "Provisioning profiles", Category: "Inventory", Bulk: true,
		Description: "Installed app provisioning profiles.", build: simple("ProvisioningProfileList")},
	{ID: "Restrictions", RequestType: "Restrictions", Label: "Restrictions", Category: "Inventory", Bulk: true,
		Description: "Effective restrictions applied to the device.",
		build: func(*BuildContext, Params) (map[string]any, error) {
			return map[string]any{"RequestType": "Restrictions", "ProfileRestrictions": true}, nil
		}},
	{ID: "AvailableOSUpdates", RequestType: "AvailableOSUpdates", Label: "Available OS updates", Category: "Inventory", Bulk: true, Supervised: true,
		Description: "Software updates the device can install.", build: simple("AvailableOSUpdates")},
	{ID: "OSUpdateStatus", RequestType: "OSUpdateStatus", Label: "OS update status", Category: "Inventory", Bulk: true, Supervised: true,
		Description: "Progress of scheduled OS updates.", build: simple("OSUpdateStatus")},
	{ID: "ManagedMediaList", RequestType: "ManagedMediaList", Label: "Managed books", Category: "Inventory", Bulk: true,
		Description: "Books installed by MDM.", build: simple("ManagedMediaList")},
	{ID: "ActivationLockBypassCode", RequestType: "ActivationLockBypassCode", Label: "Activation Lock bypass code", Category: "Inventory", Supervised: true,
		Description: "Retrieve the code that clears Activation Lock (supervised devices).", build: simple("ActivationLockBypassCode")},
	{ID: "ManagedApplicationConfiguration", RequestType: "ManagedApplicationConfiguration", Label: "Managed app configuration", Category: "Inventory",
		Description: "Read the managed configuration of apps.",
		Params:      []Param{{Key: "Identifiers", Label: "Bundle identifiers", Type: "stringlist", Required: true}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			ids := p.Strings("Identifiers")
			if len(ids) == 0 {
				return nil, errors.New("at least one bundle identifier is required")
			}
			return map[string]any{"RequestType": "ManagedApplicationConfiguration", "Identifiers": ids}, nil
		}},
	{ID: "ManagedApplicationAttributes", RequestType: "ManagedApplicationAttributes", Label: "Managed app attributes", Category: "Inventory",
		Description: "Read attributes (VPN, associated domains…) of managed apps.",
		Params:      []Param{{Key: "Identifiers", Label: "Bundle identifiers", Type: "stringlist", Required: true}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			ids := p.Strings("Identifiers")
			if len(ids) == 0 {
				return nil, errors.New("at least one bundle identifier is required")
			}
			return map[string]any{"RequestType": "ManagedApplicationAttributes", "Identifiers": ids}, nil
		}},
	{ID: "ManagedApplicationFeedback", RequestType: "ManagedApplicationFeedback", Label: "Managed app feedback", Category: "Inventory",
		Description: "Collect feedback dictionaries written by managed apps.",
		Params: []Param{{Key: "Identifiers", Label: "Bundle identifiers", Type: "stringlist", Required: true},
			{Key: "DeleteFeedback", Label: "Delete feedback after reading", Type: "bool"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			ids := p.Strings("Identifiers")
			if len(ids) == 0 {
				return nil, errors.New("at least one bundle identifier is required")
			}
			return map[string]any{"RequestType": "ManagedApplicationFeedback", "Identifiers": ids, "DeleteFeedback": p.Bool("DeleteFeedback")}, nil
		}},
	{ID: "UserList", RequestType: "UserList", Label: "Shared iPad users", Category: "Inventory",
		Description: "Users on a Shared iPad.", build: simple("UserList")},

	// ---- security & device actions ----
	{ID: "DeviceLock", RequestType: "DeviceLock", Label: "Lock device", Category: "Security", Bulk: true, Confirm: true,
		Description: "Immediately lock the screen, optionally showing a message and phone number.",
		Params: []Param{{Key: "Message", Label: "Lock screen message", Type: "string"},
			{Key: "PhoneNumber", Label: "Phone number", Type: "string"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			c := req("DeviceLock")
			if v := p.String("Message"); v != "" {
				c["Message"] = v
			}
			if v := p.String("PhoneNumber"); v != "" {
				c["PhoneNumber"] = v
			}
			return c, nil
		}},
	{ID: "ClearPasscode", RequestType: "ClearPasscode", Label: "Clear passcode", Category: "Security", Confirm: true,
		Description: "Remove the device passcode (uses the unlock token escrowed at enrollment).",
		build: func(c *BuildContext, _ Params) (map[string]any, error) {
			if c.Device == nil || len(c.Device.UnlockToken) == 0 {
				return nil, errors.New("no unlock token was escrowed for this device; passcode cannot be cleared")
			}
			return map[string]any{"RequestType": "ClearPasscode", "UnlockToken": c.Device.UnlockToken}, nil
		}},
	{ID: "ClearRestrictionsPassword", RequestType: "ClearRestrictionsPassword", Label: "Clear Screen Time passcode", Category: "Security", Supervised: true, Confirm: true,
		Description: "Clear the restrictions (Screen Time) passcode.", build: simple("ClearRestrictionsPassword")},
	{ID: "EnableLostMode", RequestType: "EnableLostMode", Label: "Enable Lost Mode", Category: "Lost Mode", Supervised: true, Confirm: true,
		Description: "Lock the device in Lost Mode and allow it to be located.",
		Params: []Param{{Key: "Message", Label: "Message", Type: "string", Help: "Shown on the lock screen."},
			{Key: "PhoneNumber", Label: "Phone number", Type: "string"},
			{Key: "Footnote", Label: "Footnote", Type: "string"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			if p.String("Message") == "" && p.String("PhoneNumber") == "" {
				return nil, errors.New("Lost Mode needs a message or a phone number")
			}
			c := req("EnableLostMode")
			for _, k := range []string{"Message", "PhoneNumber", "Footnote"} {
				if v := p.String(k); v != "" {
					c[k] = v
				}
			}
			return c, nil
		}},
	{ID: "DisableLostMode", RequestType: "DisableLostMode", Label: "Disable Lost Mode", Category: "Lost Mode", Supervised: true,
		Description: "Leave Lost Mode.", build: simple("DisableLostMode")},
	{ID: "DeviceLocation", RequestType: "DeviceLocation", Label: "Locate device", Category: "Lost Mode", Supervised: true,
		Description: "Request the current location (device must be in Lost Mode).", build: simple("DeviceLocation")},
	{ID: "PlayLostModeSound", RequestType: "PlayLostModeSound", Label: "Play sound", Category: "Lost Mode", Supervised: true,
		Description: "Play a sound on a device in Lost Mode.", build: simple("PlayLostModeSound")},
	{ID: "RestartDevice", RequestType: "RestartDevice", Label: "Restart", Category: "Device", Supervised: true, Bulk: true, Confirm: true,
		Description: "Restart the device.", build: simple("RestartDevice")},
	{ID: "ShutDownDevice", RequestType: "ShutDownDevice", Label: "Shut down", Category: "Device", Supervised: true, Bulk: true, Confirm: true,
		Description: "Power off the device.", build: simple("ShutDownDevice")},
	{ID: "LogOutUser", RequestType: "LogOutUser", Label: "Log out user", Category: "Device", Confirm: true,
		Description: "Log out the current Shared iPad user.", build: simple("LogOutUser")},
	{ID: "RequestMirroring", RequestType: "RequestMirroring", Label: "Start AirPlay mirroring", Category: "Device", Supervised: true,
		Description: "Mirror the screen to an AirPlay destination.",
		Params: []Param{{Key: "DestinationName", Label: "Destination name", Type: "string"},
			{Key: "DestinationDeviceID", Label: "Destination device ID (MAC)", Type: "string"},
			{Key: "ScanTime", Label: "Scan time (seconds)", Type: "int"},
			{Key: "Password", Label: "AirPlay password", Type: "string"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			if p.String("DestinationName") == "" && p.String("DestinationDeviceID") == "" {
				return nil, errors.New("destination name or device ID is required")
			}
			c := req("RequestMirroring")
			for _, k := range []string{"DestinationName", "DestinationDeviceID", "Password"} {
				if v := p.String(k); v != "" {
					c[k] = v
				}
			}
			if n, ok := p.Int("ScanTime"); ok && n > 0 {
				c["ScanTime"] = n
			}
			return c, nil
		}},
	{ID: "StopMirroring", RequestType: "StopMirroring", Label: "Stop AirPlay mirroring", Category: "Device", Supervised: true,
		Description: "Stop screen mirroring.", build: simple("StopMirroring")},
	{ID: "RefreshCellularPlans", RequestType: "RefreshCellularPlans", Label: "Refresh cellular plans", Category: "Device",
		Description: "Ask the device to fetch eSIM plans from a carrier server.",
		Params:      []Param{{Key: "eSIMServerURL", Label: "eSIM server URL", Type: "string", Required: true}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			u := p.String("eSIMServerURL")
			if u == "" {
				return nil, errors.New("eSIM server URL is required")
			}
			return map[string]any{"RequestType": "RefreshCellularPlans", "eSIMServerURL": u}, nil
		}},
	{ID: "DeviceConfigured", RequestType: "DeviceConfigured", Label: "Release from Setup Assistant", Category: "Device",
		Description: "Tell a device waiting in Setup Assistant (ADE await configuration) to continue.", build: simple("DeviceConfigured")},
	{ID: "ScheduleOSUpdate", RequestType: "ScheduleOSUpdate", Label: "Update iOS", Category: "Software update", Supervised: true, Bulk: true, Confirm: true,
		Description: "Download and/or install a software update.",
		Params: []Param{{Key: "InstallAction", Label: "Action", Type: "enum", Options: installActions, Default: "Default"},
			{Key: "ProductVersion", Label: "Version (blank = latest)", Type: "string", Help: "e.g. 18.6"},
			{Key: "ProductKey", Label: "Product key", Type: "string", Help: "From Available OS updates; optional."},
			{Key: "MaxUserDeferrals", Label: "Max user deferrals", Type: "int"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			action := p.String("InstallAction")
			if action == "" {
				action = "Default"
			}
			upd := map[string]any{"InstallAction": action}
			if v := p.String("ProductVersion"); v != "" {
				upd["ProductVersion"] = v
			}
			if v := p.String("ProductKey"); v != "" {
				upd["ProductKey"] = v
			}
			if n, ok := p.Int("MaxUserDeferrals"); ok && n > 0 {
				upd["MaxUserDeferrals"] = n
			}
			return map[string]any{"RequestType": "ScheduleOSUpdate", "Updates": []any{upd}}, nil
		}},

	// ---- settings ----
	{ID: "Settings.DeviceName", RequestType: "Settings", Label: "Rename device", Category: "Settings", Supervised: true,
		Description: "Set the device name. Variables like {{device.serial}} are supported.",
		Params:      []Param{{Key: "DeviceName", Label: "Device name", Type: "string", Required: true}},
		build: func(c *BuildContext, p Params) (map[string]any, error) {
			name := p.String("DeviceName")
			if name == "" {
				return nil, errors.New("device name is required")
			}
			if c.Device != nil {
				name = Expand(name, c.Service.Vars(c.Device), false)
			}
			return settingsCmd(map[string]any{"Item": "DeviceName", "DeviceName": name}), nil
		}},
	{ID: "Settings.Wallpaper", RequestType: "Settings", Label: "Set wallpaper", Category: "Settings", Supervised: true, Bulk: true,
		Description: "Set the lock and/or home screen wallpaper (PNG or JPEG).",
		Params: []Param{{Key: "Image", Label: "Image", Type: "image", Required: true},
			{Key: "Where", Label: "Apply to", Type: "enum", Options: []string{"Lock screen", "Home screen", "Both"}, Default: "Both"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			img, err := p.Data("Image")
			if err != nil || len(img) == 0 {
				return nil, errors.New("a PNG or JPEG image is required")
			}
			where := map[string]int{"Lock screen": 1, "Home screen": 2, "Both": 3}[p.String("Where")]
			if where == 0 {
				where = 3
			}
			return settingsCmd(map[string]any{"Item": "Wallpaper", "Image": img, "Where": where}), nil
		}},
	{ID: "Settings.Bluetooth", RequestType: "Settings", Label: "Bluetooth on/off", Category: "Settings", Supervised: true, Bulk: true,
		Description: "Turn Bluetooth on or off.", Params: []Param{{Key: "Enabled", Label: "Enabled", Type: "bool", Default: true}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			return settingsCmd(map[string]any{"Item": "Bluetooth", "Enabled": p.Bool("Enabled")}), nil
		}},
	{ID: "Settings.PersonalHotspot", RequestType: "Settings", Label: "Personal Hotspot", Category: "Settings", Bulk: true,
		Description: "Turn Personal Hotspot on or off.", Params: []Param{{Key: "Enabled", Label: "Enabled", Type: "bool"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			return settingsCmd(map[string]any{"Item": "PersonalHotspot", "Enabled": p.Bool("Enabled")}), nil
		}},
	{ID: "Settings.DataRoaming", RequestType: "Settings", Label: "Data roaming", Category: "Settings", Bulk: true,
		Description: "Allow or prevent cellular data roaming.", Params: []Param{{Key: "Enabled", Label: "Enabled", Type: "bool"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			return settingsCmd(map[string]any{"Item": "DataRoaming", "Enabled": p.Bool("Enabled")}), nil
		}},
	{ID: "Settings.VoiceRoaming", RequestType: "Settings", Label: "Voice roaming", Category: "Settings", Bulk: true,
		Description: "Allow or prevent voice roaming.", Params: []Param{{Key: "Enabled", Label: "Enabled", Type: "bool"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			return settingsCmd(map[string]any{"Item": "VoiceRoaming", "Enabled": p.Bool("Enabled")}), nil
		}},
	{ID: "Settings.TimeZone", RequestType: "Settings", Label: "Time zone", Category: "Settings", Supervised: true, Bulk: true,
		Description: "Set the time zone (IANA name, e.g. Asia/Tokyo).", Params: []Param{{Key: "TimeZone", Label: "Time zone", Type: "string", Required: true}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			tz := p.String("TimeZone")
			if tz == "" {
				return nil, errors.New("time zone is required")
			}
			return settingsCmd(map[string]any{"Item": "TimeZone", "TimeZone": tz}), nil
		}},
	{ID: "Settings.Analytics", RequestType: "Settings", Label: "Diagnostics & analytics", Category: "Settings", Supervised: true, Bulk: true,
		Description: "Enable or disable diagnostic and app analytics submission.",
		Params: []Param{{Key: "DiagnosticSubmission", Label: "Diagnostic submission", Type: "bool"},
			{Key: "AppAnalytics", Label: "App analytics", Type: "bool"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			return settingsCmd(
				map[string]any{"Item": "DiagnosticSubmission", "Enabled": p.Bool("DiagnosticSubmission")},
				map[string]any{"Item": "AppAnalytics", "Enabled": p.Bool("AppAnalytics")},
			), nil
		}},
	{ID: "Settings.OrganizationInfo", RequestType: "Settings", Label: "Organization info", Category: "Settings", Bulk: true,
		Description: "Organization details shown on the device (Settings → General → VPN & Device Management).",
		Params: []Param{{Key: "OrganizationName", Label: "Name", Type: "string"}, {Key: "OrganizationAddress", Label: "Address", Type: "string"},
			{Key: "OrganizationPhone", Label: "Phone", Type: "string"}, {Key: "OrganizationEmail", Label: "Email", Type: "string"},
			{Key: "OrganizationMagic", Label: "Organization magic", Type: "string"}},
		build: func(c *BuildContext, p Params) (map[string]any, error) {
			info := map[string]any{}
			for _, k := range []string{"OrganizationName", "OrganizationAddress", "OrganizationPhone", "OrganizationEmail", "OrganizationMagic"} {
				if v := p.String(k); v != "" {
					info[k] = v
				}
			}
			if len(info) == 0 {
				info["OrganizationName"] = c.Service.Setting(SettingOrgName)
			}
			return settingsCmd(map[string]any{"Item": "OrganizationInfo", "OrganizationInfo": info}), nil
		}},
	{ID: "Settings.ActivationLock", RequestType: "Settings", Label: "Allow Activation Lock", Category: "Settings", Supervised: true,
		Description: "Allow the user to enable Activation Lock (Find My) on a supervised device.",
		Params:      []Param{{Key: "Allowed", Label: "Allow Activation Lock", Type: "bool"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			return settingsCmd(map[string]any{"Item": "MDMOptions", "MDMOptions": map[string]any{"ActivationLockAllowedWhileSupervised": p.Bool("Allowed")}}), nil
		}},
	{ID: "Settings.SoftwareUpdate", RequestType: "Settings", Label: "Software update cadence", Category: "Settings", Supervised: true, Bulk: true,
		Description: "Which updates are offered to the user (iOS 17+).",
		Params: []Param{{Key: "RecommendationCadence", Label: "Offer", Type: "enum",
			Options: []string{"Both minor updates and major upgrades", "Only the newest major version", "Only minor updates to the current version"}}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			n := map[string]int{"Both minor updates and major upgrades": 0, "Only the newest major version": 2, "Only minor updates to the current version": 1}[p.String("RecommendationCadence")]
			return settingsCmd(map[string]any{"Item": "SoftwareUpdateSettings", "RecommendationCadence": n}), nil
		}},
	{ID: "Settings.Accessibility", RequestType: "Settings", Label: "Accessibility", Category: "Settings", Bulk: true,
		Description: "Configure accessibility options (iOS 16+).",
		Params: []Param{{Key: "BoldTextEnabled", Label: "Bold text", Type: "bool"}, {Key: "IncreaseContrastEnabled", Label: "Increase contrast", Type: "bool"},
			{Key: "ReduceMotionEnabled", Label: "Reduce motion", Type: "bool"}, {Key: "ReduceTransparencyEnabled", Label: "Reduce transparency", Type: "bool"},
			{Key: "GrayscaleEnabled", Label: "Grayscale", Type: "bool"}, {Key: "ZoomEnabled", Label: "Zoom", Type: "bool"},
			{Key: "VoiceOverEnabled", Label: "VoiceOver", Type: "bool"}, {Key: "TouchAccommodationsEnabled", Label: "Touch accommodations", Type: "bool"},
			{Key: "TextSize", Label: "Text size (0-11)", Type: "int"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			item := map[string]any{"Item": "AccessibilitySettings"}
			for _, k := range []string{"BoldTextEnabled", "IncreaseContrastEnabled", "ReduceMotionEnabled", "ReduceTransparencyEnabled", "GrayscaleEnabled", "ZoomEnabled", "VoiceOverEnabled", "TouchAccommodationsEnabled"} {
				if p.Has(k) {
					item[k] = p.Bool(k)
				}
			}
			if n, ok := p.Int("TextSize"); ok && n >= 0 && n <= 11 {
				item["TextSize"] = n
			}
			return settingsCmd(item), nil
		}},

	// ---- profiles & apps ----
	{ID: "InstallProfile", RequestType: "InstallProfile", Label: "Install profile", Category: "Profiles", Bulk: true,
		Description: "Install a profile from the library or a .mobileconfig file.",
		Params: []Param{{Key: "profile_id", Label: "Library profile", Type: "profile"},
			{Key: "Payload", Label: "…or upload .mobileconfig", Type: "file"}},
		build: func(c *BuildContext, p Params) (map[string]any, error) {
			if id, ok := p.Int("profile_id"); ok && id > 0 {
				prof, err := c.Service.store.GetProfile(id)
				if err != nil {
					return nil, fmt.Errorf("profile %d not found", id)
				}
				b, err := c.Service.ProfileForDevice(prof, c.Device)
				if err != nil {
					return nil, err
				}
				return map[string]any{"RequestType": "InstallProfile", "Payload": b}, nil
			}
			b, err := p.Data("Payload")
			if err != nil || len(b) == 0 {
				return nil, errors.New("choose a library profile or upload a .mobileconfig file")
			}
			return map[string]any{"RequestType": "InstallProfile", "Payload": b}, nil
		}},
	{ID: "RemoveProfile", RequestType: "RemoveProfile", Label: "Remove profile", Category: "Profiles", Bulk: true, Confirm: true,
		Description: "Remove a profile by its identifier.",
		Params:      []Param{{Key: "Identifier", Label: "Profile identifier", Type: "string", Required: true}},
		build: func(c *BuildContext, p Params) (map[string]any, error) {
			id := p.String("Identifier")
			if id == "" {
				return nil, errors.New("identifier is required")
			}
			if id == c.Service.MDMIdentifier() {
				return nil, errors.New("use the Unenroll action to remove the management profile")
			}
			return map[string]any{"RequestType": "RemoveProfile", "Identifier": id}, nil
		}},
	{ID: "InstallProvisioningProfile", RequestType: "InstallProvisioningProfile", Label: "Install provisioning profile", Category: "Profiles",
		Description: "Install a .mobileprovision file.", Params: []Param{{Key: "ProvisioningProfile", Label: ".mobileprovision", Type: "file", Required: true}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			b, err := p.Data("ProvisioningProfile")
			if err != nil || len(b) == 0 {
				return nil, errors.New("a provisioning profile file is required")
			}
			return map[string]any{"RequestType": "InstallProvisioningProfile", "ProvisioningProfile": b}, nil
		}},
	{ID: "RemoveProvisioningProfile", RequestType: "RemoveProvisioningProfile", Label: "Remove provisioning profile", Category: "Profiles", Confirm: true,
		Description: "Remove a provisioning profile by UUID.", Params: []Param{{Key: "UUID", Label: "UUID", Type: "string", Required: true}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			u := p.String("UUID")
			if u == "" {
				return nil, errors.New("UUID is required")
			}
			return map[string]any{"RequestType": "RemoveProvisioningProfile", "UUID": u}, nil
		}},
	{ID: "InstallApplication", RequestType: "InstallApplication", Label: "Install app", Category: "Apps", Bulk: true,
		Description: "Install an app from the catalog.", Params: []Param{{Key: "app_id", Label: "App", Type: "app", Required: true}},
		build: func(c *BuildContext, p Params) (map[string]any, error) {
			id, ok := p.Int("app_id")
			if !ok || id <= 0 {
				return nil, errors.New("choose an app")
			}
			app, err := c.Service.store.GetApp(id)
			if err != nil {
				return nil, fmt.Errorf("app %d not found", id)
			}
			return c.Service.InstallApplicationCommand(app, c.Device)
		}},
	{ID: "RemoveApplication", RequestType: "RemoveApplication", Label: "Remove app", Category: "Apps", Bulk: true, Confirm: true,
		Description: "Remove a managed app (and its data) by bundle identifier.",
		Params:      []Param{{Key: "Identifier", Label: "Bundle identifier", Type: "string", Required: true}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			id := p.String("Identifier")
			if id == "" {
				return nil, errors.New("bundle identifier is required")
			}
			return map[string]any{"RequestType": "RemoveApplication", "Identifier": id}, nil
		}},
	{ID: "ValidateApplications", RequestType: "ValidateApplications", Label: "Validate enterprise apps", Category: "Apps",
		Description: "Re-validate enterprise app signatures.", Params: []Param{{Key: "Identifiers", Label: "Bundle identifiers (blank = all)", Type: "stringlist"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			c := req("ValidateApplications")
			if ids := p.Strings("Identifiers"); len(ids) > 0 {
				c["Identifiers"] = ids
			}
			return c, nil
		}},
	{ID: "InstallMedia", RequestType: "InstallMedia", Label: "Install book", Category: "Apps",
		Description: "Install a book from the Book Store or a URL (PDF/ePub).",
		Params: []Param{{Key: "iTunesStoreID", Label: "Book Store ID", Type: "int"}, {Key: "MediaURL", Label: "…or media URL", Type: "string"},
			{Key: "PersistentID", Label: "Persistent ID (for URL)", Type: "string"}, {Key: "Title", Label: "Title", Type: "string"}, {Key: "Kind", Label: "Kind", Type: "enum", Options: []string{"pdf", "epub", "ibooks"}}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			c := map[string]any{"RequestType": "InstallMedia", "MediaType": "Book"}
			if id, ok := p.Int("iTunesStoreID"); ok && id > 0 {
				c["iTunesStoreID"] = id
				return c, nil
			}
			u := p.String("MediaURL")
			if u == "" {
				return nil, errors.New("a store ID or media URL is required")
			}
			c["MediaURL"] = u
			pid := p.String("PersistentID")
			if pid == "" {
				pid = "com.orchardmdm.book." + randomToken(6)
			}
			c["PersistentID"] = pid
			if v := p.String("Title"); v != "" {
				c["Title"] = v
			}
			if v := p.String("Kind"); v != "" {
				c["Kind"] = v
			}
			return c, nil
		}},
	{ID: "RemoveMedia", RequestType: "RemoveMedia", Label: "Remove book", Category: "Apps", Confirm: true,
		Description: "Remove a managed book.",
		Params:      []Param{{Key: "iTunesStoreID", Label: "Book Store ID", Type: "int"}, {Key: "PersistentID", Label: "…or persistent ID", Type: "string"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			c := map[string]any{"RequestType": "RemoveMedia", "MediaType": "Book"}
			if id, ok := p.Int("iTunesStoreID"); ok && id > 0 {
				c["iTunesStoreID"] = id
			} else if v := p.String("PersistentID"); v != "" {
				c["PersistentID"] = v
			} else {
				return nil, errors.New("a store ID or persistent ID is required")
			}
			return c, nil
		}},
	{ID: "DeclarativeManagement", RequestType: "DeclarativeManagement", Label: "Sync declarations", Category: "Declarative",
		Description: "Ask the device to synchronise its Declarative Device Management state.",
		build: func(c *BuildContext, _ Params) (map[string]any, error) {
			tok, err := c.Service.ddm.Token(c.Device.UDID)
			if err != nil {
				return nil, err
			}
			return map[string]any{"RequestType": "DeclarativeManagement", "Data": SyncTokens(tok)}, nil
		}},

	// ---- advanced ----
	{ID: "Custom", RequestType: "", Label: "Custom command", Category: "Advanced", Confirm: true,
		Description: "Send any MDM command as a plist or JSON dictionary. Wipe commands are refused.",
		Params: []Param{{Key: "Command", Label: "Command dictionary (XML plist or JSON)", Type: "text", Required: true,
			Help: "Must include RequestType, e.g. {\"RequestType\":\"DeviceInformation\",\"Queries\":[\"BatteryLevel\"]}"}},
		build: func(_ *BuildContext, p Params) (map[string]any, error) {
			return ParseCustomCommand(p.String("Command"))
		}},
}

// SyncTokens returns the DeclarativeManagement Data blob.
func SyncTokens(token string) []byte {
	b, _ := json.Marshal(map[string]any{"SyncTokens": map[string]any{"DeclarationsToken": token}})
	return b
}

var catalogIndex = func() map[string]*Spec {
	m := map[string]*Spec{}
	for _, s := range Catalog {
		m[s.ID] = s
	}
	return m
}()

// LookupSpec finds a catalog entry.
func LookupSpec(id string) (*Spec, bool) {
	s, ok := catalogIndex[id]
	return s, ok
}

// CatalogByCategory returns category names in display order.
func CatalogCategories() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range Catalog {
		if !seen[s.Category] {
			seen[s.Category] = true
			out = append(out, s.Category)
		}
	}
	return out
}

// ParseCustomCommand decodes a raw command dictionary from XML plist or JSON.
func ParseCustomCommand(text string) (map[string]any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("command is empty")
	}
	var cmd map[string]any
	if strings.HasPrefix(text, "{") {
		if err := json.Unmarshal([]byte(text), &cmd); err != nil {
			return nil, fmt.Errorf("invalid JSON: %w", err)
		}
		cmd = normaliseJSONNumbers(cmd).(map[string]any)
	} else {
		if !strings.Contains(text, "<plist") {
			text = `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0">` + text + `</plist>`
		}
		if _, err := plist.Unmarshal([]byte(text), &cmd); err != nil {
			return nil, fmt.Errorf("invalid plist: %w", err)
		}
	}
	if inner, ok := cmd["Command"].(map[string]any); ok {
		cmd = inner
	}
	rt, _ := cmd["RequestType"].(string)
	if rt == "" {
		return nil, errors.New("RequestType is missing")
	}
	if IsDenied(rt) {
		return nil, ErrForbiddenCommand
	}
	return cmd, nil
}

// normaliseJSONNumbers converts whole float64 values to int64 so they encode
// as plist integers.
func normaliseJSONNumbers(v any) any {
	switch t := v.(type) {
	case float64:
		if t == float64(int64(t)) {
			return int64(t)
		}
		return t
	case map[string]any:
		for k, vv := range t {
			t[k] = normaliseJSONNumbers(vv)
		}
		return t
	case []any:
		for i, vv := range t {
			t[i] = normaliseJSONNumbers(vv)
		}
		return t
	}
	return v
}

// BuildCommand builds a catalog command for a device.
func (s *Service) BuildCommand(specID string, d *store.Device, p Params) (map[string]any, *Spec, error) {
	spec, ok := LookupSpec(specID)
	if !ok {
		if IsDenied(specID) {
			return nil, nil, ErrForbiddenCommand
		}
		return nil, nil, fmt.Errorf("unknown command %q", specID)
	}
	if p == nil {
		p = Params{}
	}
	cmd, err := spec.build(&BuildContext{Service: s, Device: d}, p)
	if err != nil {
		return nil, spec, err
	}
	if rt, _ := cmd["RequestType"].(string); IsDenied(rt) {
		return nil, spec, ErrForbiddenCommand
	}
	return cmd, spec, nil
}

// SendResult reports the outcome of queueing a command on one device.
type SendResult struct {
	UDID        string `json:"udid"`
	CommandUUID string `json:"command_uuid,omitempty"`
	Error       string `json:"error,omitempty"`
}

// SendCatalogCommand builds and queues a catalog command on several devices, then pushes.
func (s *Service) SendCatalogCommand(specID string, udids []string, p Params, meta Meta) []SendResult {
	var results []SendResult
	var pushed []string
	sort.Strings(udids)
	for _, u := range udids {
		res := SendResult{UDID: u}
		d, err := s.store.GetDevice(u)
		if err != nil {
			res.Error = "device not found"
			results = append(results, res)
			continue
		}
		if d.EnrollmentStatus != "enrolled" {
			res.Error = "device is not enrolled"
			results = append(results, res)
			continue
		}
		cmd, _, err := s.BuildCommand(specID, d, p)
		if err != nil {
			res.Error = err.Error()
			results = append(results, res)
			continue
		}
		if meta.Ref == "" {
			meta.Ref = specID
		}
		c, err := s.Enqueue(u, cmd, meta)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.CommandUUID = c.UUID
			pushed = append(pushed, u)
		}
		results = append(results, res)
	}
	s.Push(pushed...)
	return results
}

// InstallApplicationCommand builds InstallApplication for a catalog app.
func (s *Service) InstallApplicationCommand(app *store.App, d *store.Device) (map[string]any, error) {
	c := map[string]any{"RequestType": "InstallApplication"}
	flags := 0
	if app.RemoveOnUnenroll {
		flags |= 1
	}
	if app.PreventBackup {
		flags |= 4
	}
	c["ManagementFlags"] = flags
	switch app.Kind {
	case "appstore", "vpp":
		if app.ITunesID == 0 {
			return nil, errors.New("app has no App Store ID")
		}
		c["iTunesStoreID"] = app.ITunesID
		if app.UseVPP || app.Kind == "vpp" {
			c["Options"] = map[string]any{"PurchaseMethod": 1}
		}
	case "enterprise":
		if app.FileSecret == "" {
			return nil, errors.New("enterprise app has no uploaded package")
		}
		base := s.PublicURL()
		if base == "" {
			return nil, ErrNoPublicURL
		}
		c["ManifestURL"] = fmt.Sprintf("%s/files/apps/%d/%s/manifest.plist", base, app.ID, app.FileSecret)
	default:
		return nil, fmt.Errorf("unknown app kind %q", app.Kind)
	}
	if app.TakeManagement {
		c["ChangeManagementState"] = "Managed"
	}
	if len(app.Config) > 0 && string(app.Config) != "null" && string(app.Config) != "{}" {
		var cfg map[string]any
		if err := json.Unmarshal(app.Config, &cfg); err != nil {
			return nil, fmt.Errorf("managed app configuration is not a JSON object: %w", err)
		}
		cfg = normaliseJSONNumbers(cfg).(map[string]any)
		if d != nil {
			cfg = ExpandValue(cfg, s.Vars(d)).(map[string]any)
		}
		c["Configuration"] = cfg
	}
	if len(app.Attributes) > 0 && string(app.Attributes) != "null" && string(app.Attributes) != "{}" {
		var attrs map[string]any
		if err := json.Unmarshal(app.Attributes, &attrs); err == nil && len(attrs) > 0 {
			c["Attributes"] = normaliseJSONNumbers(attrs)
		}
	}
	return c, nil
}
