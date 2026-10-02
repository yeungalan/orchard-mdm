package mdm

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

// handleResult applies an acknowledged command response to the inventory.
func (s *Service) handleResult(d *store.Device, cmd *store.Command, msg map[string]any) {
	switch cmd.RequestType {
	case "DeviceInformation":
		q, _ := msg["QueryResponses"].(map[string]any)
		s.applyDeviceInformation(d, q, cmd.Ref == "telemetry")
	case "SecurityInfo":
		si, _ := msg["SecurityInfo"].(map[string]any)
		s.applySecurityInfo(d, si)
	case "InstalledApplicationList":
		list, _ := msg["InstalledApplicationList"].([]any)
		s.applyInstalledApps(d, list)
	case "ManagedApplicationList":
		m, _ := msg["ManagedApplicationList"].(map[string]any)
		s.applyManagedApps(d, m)
	case "ProfileList":
		list, _ := msg["ProfileList"].([]any)
		s.applyProfileList(d, list)
	case "CertificateList":
		list, _ := msg["CertificateList"].([]any)
		s.applyCertificateList(d, list)
	case "ProvisioningProfileList":
		_ = s.store.UpdateDevice(d.UDID, map[string]any{"provisioning_json": jsonSafe(orEmptyList(msg["ProvisioningProfileList"]))})
	case "Restrictions":
		r := map[string]any{}
		for _, k := range []string{"GlobalRestrictions", "ProfileRestrictions"} {
			if v, ok := msg[k]; ok {
				r[k] = jsonSafe(v)
			}
		}
		_ = s.store.UpdateDevice(d.UDID, map[string]any{"restrictions_json": r})
	case "AvailableOSUpdates":
		_ = s.store.UpdateDevice(d.UDID, map[string]any{"os_updates_json": jsonSafe(orEmptyList(msg["AvailableOSUpdates"]))})
	case "OSUpdateStatus":
		_ = s.store.UpdateDevice(d.UDID, map[string]any{"os_update_status_json": jsonSafe(orEmptyList(msg["OSUpdateStatus"]))})
	case "ManagedMediaList":
		_ = s.store.UpdateDevice(d.UDID, map[string]any{"media_json": map[string]any{"Books": jsonSafe(orEmptyList(msg["Books"]))}})
	case "ActivationLockBypassCode":
		if code := asString(msg["ActivationLockBypassCode"]); code != "" {
			_ = s.store.UpdateDevice(d.UDID, map[string]any{"activation_lock_bypass": code})
		}
	case "DeviceLocation":
		s.applyLocation(d, msg)
	case "EnableLostMode":
		_ = s.store.UpdateDevice(d.UDID, map[string]any{"lost_mode": true})
		s.event(d.UDID, "device.lost_mode", "warn", "Lost Mode enabled", nil)
		if !s.store.HasPendingCommand(d.UDID, "DeviceLocation", "") {
			_, _ = s.EnqueueAndPush(d.UDID, map[string]any{"RequestType": "DeviceLocation"}, Meta{Source: "system", Ref: "lostmode-location"})
		}
	case "DisableLostMode":
		_ = s.store.UpdateDevice(d.UDID, map[string]any{"lost_mode": false})
		s.event(d.UDID, "device.lost_mode", "info", "Lost Mode disabled", nil)
	case "DeviceConfigured":
		_ = s.store.UpdateDevice(d.UDID, map[string]any{"awaiting_configuration": false})
		s.event(d.UDID, "device.configured", "info", "Released from Setup Assistant", nil)
	case "Settings", "ClearPasscode", "InstallProfile", "RemoveProfile", "RemoveApplication", "InstallApplication":
		follow := map[string][]string{
			"InstallApplication": {"ManagedApplicationList", "InstalledApplicationList"},
			"Settings":           {"DeviceInformation"},
			"ClearPasscode":      {"SecurityInfo"},
			"InstallProfile":     {"ProfileList"},
			"RemoveProfile":      {"ProfileList"},
			"RemoveApplication":  {"InstalledApplicationList", "ManagedApplicationList"},
		}[cmd.RequestType]
		for _, t := range follow {
			if !s.store.HasPendingCommand(d.UDID, t, "") {
				if c, _, err := s.BuildCommand(t, d, nil); err == nil {
					_, _ = s.Enqueue(d.UDID, c, Meta{Source: "system", Ref: "refresh"})
				}
			}
		}
	}
}

func orEmptyList(v any) any {
	if v == nil {
		return []any{}
	}
	return v
}

var cellularTech = map[int64]string{0: "None", 1: "GSM", 2: "CDMA", 3: "GSM+CDMA"}

func (s *Service) applyDeviceInformation(d *store.Device, q map[string]any, telemetry bool) {
	if q == nil {
		return
	}
	fields := map[string]any{}
	for key, col := range map[string]string{
		"DeviceName": "device_name", "OSVersion": "os_version", "BuildVersion": "build_version", "ModelName": "model_name", "Model": "model",
		"ProductName": "product_name", "SerialNumber": "serial_number", "IMEI": "imei", "MEID": "meid", "WiFiMAC": "wifi_mac",
		"BluetoothMAC": "bluetooth_mac", "PhoneNumber": "phone_number", "ICCID": "iccid",
	} {
		if v, ok := q[key]; ok {
			if str := asString(v); str != "" {
				fields[col] = str
			}
		}
	}
	for key, col := range map[string]string{
		"IsSupervised": "supervised", "IsDeviceLocatorServiceEnabled": "find_my", "IsActivationLockEnabled": "activation_lock",
		"AwaitingConfiguration": "awaiting_configuration", "IsRoaming": "roaming", "PersonalHotspotEnabled": "hotspot", "IsMDMLostModeEnabled": "lost_mode",
	} {
		if v, ok := q[key]; ok {
			fields[col] = asBool(v)
		}
	}
	battery, available := -1.0, -1.0
	if v, ok := asFloat(q["BatteryLevel"]); ok {
		battery = round2(v)
		fields["battery_level"] = battery
	}
	if v, ok := asFloat(q["DeviceCapacity"]); ok {
		fields["capacity_gb"] = round2(v)
	}
	if v, ok := asFloat(q["AvailableDeviceCapacity"]); ok {
		available = round2(v)
		fields["available_gb"] = available
	}
	carrier := asString(q["CurrentCarrierNetwork"])
	if carrier == "" {
		carrier = asString(q["SubscriberCarrierNetwork"])
	}
	if subs, ok := q["ServiceSubscriptions"].([]any); ok {
		for _, raw := range subs {
			sub, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if carrier == "" {
				carrier = asString(sub["CurrentCarrierNetwork"])
			}
			if _, set := fields["phone_number"]; !set {
				if p := asString(sub["PhoneNumber"]); p != "" {
					fields["phone_number"] = p
				}
			}
			if _, set := fields["iccid"]; !set {
				if p := asString(sub["ICCID"]); p != "" {
					fields["iccid"] = p
				}
			}
			if asBool(sub["IsRoaming"]) {
				fields["roaming"] = true
			}
		}
	}
	if carrier != "" {
		fields["carrier"] = carrier
	}
	tech := ""
	if v, ok := q["CellularTechnology"]; ok {
		tech = cellularTech[asInt(v)]
		fields["cellular_technology"] = tech
	}
	info := d.Info
	if info == nil {
		info = map[string]any{}
	}
	for k, v := range q {
		info[k] = jsonSafe(v)
	}
	fields["info_json"] = info
	now := store.Now()
	fields["telemetry_at"] = now
	if !telemetry {
		fields["last_inventory"] = now
	}
	if err := s.store.UpdateDevice(d.UDID, fields); err != nil {
		s.log.Error("update device information", "udid", d.UDID, "err", err)
		return
	}
	roaming, _ := fields["roaming"].(bool)
	sample := &store.TelemetrySample{
		DeviceID: d.UDID, Source: "mdm", Battery: battery, AvailableGB: available, Carrier: carrier, CellularTechnology: tech,
		Roaming: roaming, IP: d.LastIP, Data: map[string]any{},
	}
	for _, k := range []string{"PersonalHotspotEnabled", "DataRoamingEnabled", "VoiceRoamingEnabled", "IsNetworkTethered", "IsDoNotDisturbInEffect", "IsMDMLostModeEnabled"} {
		if v, ok := q[k]; ok {
			sample.Data[k] = asBool(v)
		}
	}
	_ = s.store.InsertTelemetry(sample)
	s.bus.Publish(events.DeviceTelemetry, d.UDID, map[string]any{"battery": battery, "available_gb": available, "carrier": carrier})
	if !telemetry {
		s.bus.Publish(events.DeviceInventory, d.UDID, map[string]any{"kind": "DeviceInformation"})
	}
	if name, ok := fields["device_name"].(string); ok && d.DeviceName != "" && name != d.DeviceName {
		s.event(d.UDID, "device.renamed", "info", "Device name changed from \""+d.DeviceName+"\" to \""+name+"\"", nil)
	}
	if v, ok := fields["os_version"].(string); ok && d.OSVersion != "" && v != d.OSVersion {
		s.event(d.UDID, "device.os_updated", "info", "OS version changed from "+d.OSVersion+" to "+v, nil)
	}
}

func (s *Service) applySecurityInfo(d *store.Device, si map[string]any) {
	if si == nil {
		return
	}
	fields := map[string]any{"security_json": jsonSafe(si)}
	if v, ok := si["HardwareEncryptionCaps"]; ok {
		fields["encryption_caps"] = int(asInt(v))
	}
	if v, ok := si["PasscodePresent"]; ok {
		fields["passcode_present"] = asBool(v)
	}
	if v, ok := si["PasscodeCompliant"]; ok {
		fields["passcode_compliant"] = asBool(v)
	}
	if ms, ok := si["ManagementStatus"].(map[string]any); ok {
		if v, ok := ms["EnrolledViaDEP"]; ok {
			fields["dep_enrolled"] = asBool(v)
		}
	}
	_ = s.store.UpdateDevice(d.UDID, fields)
	s.bus.Publish(events.DeviceInventory, d.UDID, map[string]any{"kind": "SecurityInfo"})
}

func (s *Service) applyInstalledApps(d *store.Device, list []any) {
	apps := make([]store.DeviceApp, 0, len(list))
	present := map[string]bool{}
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		a := store.DeviceApp{
			BundleID: asString(m["Identifier"]), Name: asString(m["Name"]), Version: asString(m["Version"]), ShortVersion: asString(m["ShortVersion"]),
			BundleSize: asInt(m["BundleSize"]), DynamicSize: asInt(m["DynamicSize"]),
		}
		if a.Name == "" {
			a.Name = a.BundleID
		}
		present[a.BundleID] = true
		apps = append(apps, a)
	}
	if err := s.store.ReplaceDeviceApps(d.UDID, apps); err != nil {
		s.log.Error("store apps", "err", err)
		return
	}
	states, _ := s.store.ListAppStates(d.UDID, 0)
	for _, st := range states {
		switch {
		case (st.Status == "installing" || st.Status == "pending") && present[st.BundleID]:
			st.Status, st.Error = "installed", ""
			_ = s.store.SetAppState(st)
		case st.Status == "removed" && present[st.BundleID]:
			// still present; leave for the reconciler
		case st.Status == "installed" && !present[st.BundleID]:
			st.Status = "missing"
			_ = s.store.SetAppState(st)
		}
	}
	s.bus.Publish(events.DeviceInventory, d.UDID, map[string]any{"kind": "InstalledApplicationList"})
}

var managedStatusMap = map[string]string{
	"Managed": "installed", "Failed": "failed", "UserRejected": "failed", "UpdateFailed": "failed", "UserInstalledApp": "failed",
	"ManagedButUninstalled": "missing", "Installing": "installing", "Prompting": "installing", "PromptingForLogin": "installing",
	"Redeeming": "installing", "NeedsRedemption": "installing", "ValidatingPurchase": "installing", "PromptingForManagement": "installing",
	"Queued": "installing", "Updating": "installing", "PromptingForUpdate": "installing", "PromptingForUpdateLogin": "installing",
}

func (s *Service) applyManagedApps(d *store.Device, m map[string]any) {
	statuses := map[string]string{}
	for bundle, raw := range m {
		info, _ := raw.(map[string]any)
		statuses[bundle] = asString(info["Status"])
	}
	_ = s.store.SetManagedApps(d.UDID, statuses)
	states, _ := s.store.ListAppStates(d.UDID, 0)
	for _, st := range states {
		ms, ok := statuses[st.BundleID]
		if !ok || st.Intent == "uninstall" {
			continue
		}
		next := managedStatusMap[ms]
		if next == "" || next == st.Status {
			continue
		}
		st.Status = next
		if next == "failed" {
			st.Error = "Device reported status " + ms
		} else {
			st.Error = ""
		}
		_ = s.store.SetAppState(st)
	}
}

func (s *Service) applyProfileList(d *store.Device, list []any) {
	profiles := make([]store.DeviceProfile, 0, len(list))
	present := map[string]bool{}
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		p := store.DeviceProfile{
			Identifier: asString(m["PayloadIdentifier"]), DisplayName: asString(m["PayloadDisplayName"]), Organization: asString(m["PayloadOrganization"]),
			Description: asString(m["PayloadDescription"]), UUID: asString(m["PayloadUUID"]), Version: asInt(m["PayloadVersion"]),
			IsManaged: asBool(m["IsManaged"]), IsEncrypted: asBool(m["IsEncrypted"]), RemovalDisallowed: asBool(m["PayloadRemovalDisallowed"]),
		}
		if content, ok := m["PayloadContent"].([]any); ok {
			for _, c := range content {
				if cm, ok := c.(map[string]any); ok {
					p.PayloadTypes = append(p.PayloadTypes, asString(cm["PayloadType"]))
				}
			}
		}
		present[p.Identifier] = true
		profiles = append(profiles, p)
	}
	if err := s.store.ReplaceDeviceProfiles(d.UDID, profiles); err != nil {
		s.log.Error("store profiles", "err", err)
		return
	}
	states, _ := s.store.ListProfileStates(d.UDID, 0)
	for _, st := range states {
		if st.Status == "installed" && !present[st.Identifier] {
			st.Status, st.Error = "missing", "Profile not found on device"
			_ = s.store.SetProfileState(st)
		}
	}
	s.bus.Publish(events.DeviceInventory, d.UDID, map[string]any{"kind": "ProfileList"})
}

func (s *Service) applyCertificateList(d *store.Device, list []any) {
	var certs []store.DeviceCertificate
	for _, raw := range list {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		der, _ := m["Data"].([]byte)
		c := store.DeviceCertificate{CommonName: asString(m["CommonName"]), IsIdentity: asBool(m["IsIdentity"])}
		sum := sha256.Sum256(der)
		c.SHA256 = hex.EncodeToString(sum[:])
		if xc, err := x509.ParseCertificate(der); err == nil {
			c.Subject, c.Issuer = xc.Subject.String(), xc.Issuer.String()
			c.NotBefore, c.NotAfter = xc.NotBefore.Unix(), xc.NotAfter.Unix()
			if c.CommonName == "" {
				c.CommonName = xc.Subject.CommonName
			}
		}
		certs = append(certs, c)
	}
	_ = s.store.ReplaceDeviceCertificates(d.UDID, certs)
}

func (s *Service) applyLocation(d *store.Device, msg map[string]any) {
	lat, ok1 := asFloat(msg["Latitude"])
	lon, ok2 := asFloat(msg["Longitude"])
	if !ok1 || !ok2 {
		return
	}
	loc := &store.Location{DeviceID: d.UDID, Source: "mdm", Latitude: lat, Longitude: lon, Speed: -1, Course: -1}
	loc.Accuracy, _ = asFloat(msg["HorizontalAccuracy"])
	loc.Altitude, _ = asFloat(msg["Altitude"])
	if v, ok := asFloat(msg["Speed"]); ok {
		loc.Speed = v
	}
	if v, ok := asFloat(msg["Course"]); ok {
		loc.Course = v
	}
	if ts := asString(msg["Timestamp"]); ts != "" {
		if t, err := time.Parse(time.RFC3339, ts); err == nil {
			loc.TS = t.Unix()
		}
	}
	if loc.TS == 0 {
		loc.TS = store.Now()
	}
	s.SaveLocation(loc)
}

// SaveLocation stores a location fix and updates the device's last known position.
func (s *Service) SaveLocation(loc *store.Location) {
	if err := s.store.InsertLocation(loc); err != nil {
		s.log.Error("store location", "err", err)
		return
	}
	_ = s.store.UpdateDevice(loc.DeviceID, map[string]any{
		"location_json": map[string]any{"latitude": loc.Latitude, "longitude": loc.Longitude, "accuracy": loc.Accuracy, "altitude": loc.Altitude,
			"speed": loc.Speed, "course": loc.Course, "source": loc.Source, "ts": loc.TS},
		"location_at": loc.TS,
	})
	s.bus.Publish(events.DeviceLocation, loc.DeviceID, map[string]any{"latitude": loc.Latitude, "longitude": loc.Longitude, "accuracy": loc.Accuracy, "source": loc.Source})
}
