package mdm

import (
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/yeungalan/orchard-mdm/internal/ddm"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"howett.net/plist"
)

const maxBody = 32 << 20

func readBody(r *http.Request) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r.Body, maxBody))
}

// deviceID returns the identifier of the enrollment (UDID for device
// enrollments, EnrollmentID for user enrollments).
func deviceID(m map[string]any) string {
	if u := asString(m["UDID"]); u != "" {
		return u
	}
	return asString(m["EnrollmentID"])
}

// HandleCheckin serves the CheckInURL (PUT /mdm/checkin).
func (s *Service) HandleCheckin(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var msg map[string]any
	if _, err := plist.Unmarshal(body, &msg); err != nil {
		s.log.Warn("check-in: invalid plist", "err", err, "ip", ClientIP(r, s.trustProxy))
		http.Error(w, "invalid plist", http.StatusBadRequest)
		return
	}
	mt := asString(msg["MessageType"])
	udid := deviceID(msg)
	if udid == "" && asString(msg["EnrollmentUserID"]) != "" {
		// user channel of a user enrollment: Orchard manages the device channel only
		w.WriteHeader(http.StatusOK)
		return
	}
	if udid == "" {
		http.Error(w, "missing UDID", http.StatusBadRequest)
		return
	}
	cert, err := s.requestCert(r, body)
	if err == nil {
		err = s.verifyIdentity(cert)
	}
	if err != nil {
		s.log.Warn("check-in: identity rejected", "type", mt, "udid", udid, "err", err, "ip", ClientIP(r, s.trustProxy))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.log.Debug("check-in", "type", mt, "udid", udid)

	if mt == "Authenticate" {
		if err := s.authenticate(udid, msg, cert, r); err != nil {
			s.log.Warn("authenticate failed", "udid", udid, "err", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	d, err := s.store.GetDevice(udid)
	if err != nil {
		s.log.Warn("check-in from unknown device", "type", mt, "udid", udid)
		http.Error(w, "unknown device", http.StatusUnauthorized)
		return
	}
	if err := s.authorizeDevice(d, cert); err != nil {
		s.log.Warn("check-in: certificate mismatch", "type", mt, "udid", udid, "err", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.store.TouchDevice(udid)

	switch mt {
	case "TokenUpdate":
		if err := s.tokenUpdate(d, msg); err != nil {
			s.log.Error("token update failed", "udid", udid, "err", err)
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	case "CheckOut":
		s.checkOut(d)
		w.WriteHeader(http.StatusOK)
	case "DeclarativeManagement":
		s.declarativeManagement(w, d, msg)
	case "SetBootstrapToken":
		tok, _ := msg["BootstrapToken"].([]byte)
		_ = s.store.UpdateDevice(udid, map[string]any{"bootstrap_token": tok})
		s.event(udid, "device.bootstrap_token", "info", "Bootstrap token escrowed", nil)
		w.WriteHeader(http.StatusOK)
	case "GetBootstrapToken":
		if len(d.BootstrapToken) == 0 {
			w.WriteHeader(http.StatusOK)
			return
		}
		out, _ := plist.Marshal(map[string]any{"BootstrapToken": d.BootstrapToken}, plist.XMLFormat)
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(out)
	case "UserAuthenticate", "GetToken":
		// user channels / token services are not offered
		http.Error(w, "not supported", http.StatusGone)
	default:
		s.log.Info("unhandled check-in message", "type", mt, "udid", udid)
		http.Error(w, "unsupported message", http.StatusBadRequest)
	}
}

func (s *Service) authenticate(udid string, msg map[string]any, cert *x509.Certificate, r *http.Request) error {
	fp := pki.Fingerprint(cert)
	issued, _ := s.store.GetIssuedCertBySHA(fp)
	if issued != nil && issued.DeviceID != "" && issued.DeviceID != udid {
		return fmt.Errorf("identity certificate is bound to device %s", issued.DeviceID)
	}
	ref := ""
	if issued != nil {
		ref = issued.Ref
	}
	if err := s.store.EnsureDevice(udid); err != nil {
		return err
	}
	prev, err := s.store.GetDevice(udid)
	if err != nil {
		return err
	}
	if strings.HasPrefix(ref, "renew:") && prev.EnrollmentStatus == "enrolled" {
		// identity renewal of an enrolled device: just rebind the certificate
		s.store.SetIssuedCertDevice(fp, udid)
		return s.store.UpdateDevice(udid, map[string]any{"cert_fingerprint": fp, "cert_not_after": cert.NotAfter.Unix(), "last_seen": store.Now()})
	}
	fields := map[string]any{
		"user_enrollment":       false,
		"managed_apple_id":      "",
		"account_enrollment_id": 0,
		"enrollment_status":     "pending",
		"push_token":            "",
		"push_magic":            "",
		"cert_fingerprint":      fp,
		"cert_not_after":        cert.NotAfter.Unix(),
		"ddm_token":             "",
		"last_seen":             store.Now(),
	}
	for key, col := range map[string]string{
		"SerialNumber": "serial_number", "IMEI": "imei", "MEID": "meid", "DeviceName": "device_name", "Model": "model",
		"ModelName": "model_name", "ProductName": "product_name", "OSVersion": "os_version", "BuildVersion": "build_version", "Topic": "topic",
	} {
		if v := asString(msg[key]); v != "" {
			fields[col] = v
		}
	}
	switch {
	case strings.HasPrefix(ref, "token:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(ref, "token:"), 10, 64)
		fields["enrollment_type"] = "token"
		fields["enrollment_token_id"] = id
		fields["dep_enrolled"] = false
		if t, err := s.store.GetEnrollmentToken(id); err == nil {
			if t.Ownership != "" {
				fields["ownership"] = t.Ownership
			}
			if t.AssignedUser != "" && prev.AssignedUser == "" {
				fields["assigned_user"] = t.AssignedUser
			}
		}
	case ref == "ade" || strings.HasPrefix(ref, "ade:"):
		fields["enrollment_type"] = "ade"
		fields["dep_enrolled"] = true
		fields["ownership"] = "corporate"
		fields["enrollment_token_id"] = 0
	case strings.HasPrefix(ref, "account:"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(ref, "account:"), 10, 64)
		a, err := s.store.GetAccountEnrollment(id)
		if err != nil {
			return fmt.Errorf("account enrollment %d not found", id)
		}
		byod := a.Mode == "byod"
		fields["enrollment_type"] = a.Mode
		fields["user_enrollment"] = byod
		fields["managed_apple_id"] = a.ManagedAppleID
		fields["account_enrollment_id"] = a.ID
		fields["enrollment_token_id"] = a.EnrollmentTokenID
		fields["dep_enrolled"] = false
		fields["assigned_email"] = a.ManagedAppleID
		if a.DisplayName != "" {
			fields["assigned_user"] = a.DisplayName
		}
		if byod {
			fields["ownership"] = "personal"
		} else {
			fields["ownership"] = "corporate"
		}
		if a.EnrollmentTokenID > 0 && !byod {
			if t, err := s.store.GetEnrollmentToken(a.EnrollmentTokenID); err == nil && t.Ownership != "" {
				fields["ownership"] = t.Ownership
			}
		}
		s.store.SetAccountEnrollmentDevice(a.ID, udid)
	case strings.HasPrefix(ref, "renew:"):
		// identity renewal profile re-installed; keep the original enrollment metadata
		for _, k := range []string{"user_enrollment", "managed_apple_id", "account_enrollment_id"} {
			delete(fields, k)
		}
	default:
		fields["enrollment_type"] = "manual"
		fields["enrollment_token_id"] = 0
	}
	if err := s.store.UpdateDevice(udid, fields); err != nil {
		return err
	}
	// a (re-)enrollment starts from a clean slate
	_, _ = s.store.CancelDeviceCommands(udid)
	_ = s.store.ClearDeviceState(udid)
	s.store.SetIssuedCertDevice(fp, udid)
	s.event(udid, "device.authenticate", "info", "Enrollment started", map[string]any{
		"serial": asString(msg["SerialNumber"]), "model": asString(msg["ProductName"]), "os": asString(msg["OSVersion"]), "ip": ClientIP(r, s.trustProxy),
	})
	return nil
}

func (s *Service) tokenUpdate(d *store.Device, msg map[string]any) error {
	if asString(msg["UserID"]) != "" || asString(msg["UserShortName"]) != "" || asString(msg["EnrollmentUserID"]) != "" {
		// user channel (Shared iPad / macOS / user enrollment); Orchard manages the device channel only
		s.log.Debug("ignoring user-channel TokenUpdate", "udid", d.UDID)
		return nil
	}
	tok, _ := msg["Token"].([]byte)
	magic := asString(msg["PushMagic"])
	if len(tok) == 0 || magic == "" {
		return errors.New("TokenUpdate without Token/PushMagic")
	}
	fields := map[string]any{
		"push_token":             hex.EncodeToString(tok),
		"push_magic":             magic,
		"awaiting_configuration": asBool(msg["AwaitingConfiguration"]),
	}
	if t := asString(msg["Topic"]); t != "" {
		fields["topic"] = t
	}
	if ut, ok := msg["UnlockToken"].([]byte); ok && len(ut) > 0 {
		fields["unlock_token"] = ut
	}
	first := d.EnrollmentStatus != "enrolled"
	if first {
		fields["enrollment_status"] = "enrolled"
		fields["enrolled_at"] = store.Now()
		fields["unenrolled_at"] = 0
	}
	if err := s.store.UpdateDevice(d.UDID, fields); err != nil {
		return err
	}
	if !first {
		s.bus.Publish(events.DeviceTokenUpdate, d.UDID, nil)
		return nil
	}
	// enrollment completed
	var groupIDs []int64
	if d.AccountEnrollmentID > 0 {
		if a, err := s.store.GetAccountEnrollment(d.AccountEnrollmentID); err == nil {
			groupIDs = append(groupIDs, a.GroupIDs...)
		}
	}
	if d.EnrollmentTokenID > 0 {
		if t, err := s.store.GetEnrollmentToken(d.EnrollmentTokenID); err == nil {
			groupIDs = append(groupIDs, t.GroupIDs...)
			s.store.IncrementEnrollmentTokenUse(t.ID)
		}
	}
	if d.EnrollmentType == "ade" && d.SerialNumber != "" {
		if dd, err := s.store.GetDEPDevice(d.SerialNumber); err == nil {
			pid := dd.AssignedProfileID
			if pid == 0 && dd.ProfileUUID != "" {
				pid = s.store.DEPProfileIDByUUID(dd.ProfileUUID)
			}
			if p, err := s.store.GetDEPProfile(pid); err == nil {
				groupIDs = append(groupIDs, p.GroupIDs...)
			}
			if dd.AssetTag != "" {
				_ = s.store.UpdateDevice(d.UDID, map[string]any{"asset_tag": dd.AssetTag})
			}
		}
	}
	for _, g := range groupIDs {
		_ = s.store.AddGroupMembers(g, []string{d.UDID})
	}
	s.QueueInventory(d.UDID, "enrollment")
	s.event(d.UDID, "device.enrolled", "info", "Device enrolled", map[string]any{"type": d.EnrollmentType, "awaiting_configuration": fields["awaiting_configuration"]})
	s.bus.Publish(events.DeviceEnrolled, d.UDID, map[string]any{
		"serial_number": d.SerialNumber, "device_name": d.DeviceName, "product_name": d.ProductName, "os_version": d.OSVersion,
		"enrollment_type": d.EnrollmentType, "awaiting_configuration": fields["awaiting_configuration"],
		"user_enrollment": d.UserEnrollment, "managed_apple_id": d.ManagedAppleID,
	})
	s.Push(d.UDID)
	return nil
}

func (s *Service) checkOut(d *store.Device) {
	_ = s.store.UpdateDevice(d.UDID, map[string]any{
		"enrollment_status": "unenrolled", "unenrolled_at": store.Now(), "push_token": "", "push_magic": "", "lost_mode": false,
	})
	_, _ = s.store.CancelDeviceCommands(d.UDID)
	_ = s.store.ClearDeviceState(d.UDID)
	s.event(d.UDID, "device.unenrolled", "warn", "Device removed the management profile (check-out)", nil)
	s.bus.Publish(events.DeviceUnenrolled, d.UDID, map[string]any{"serial_number": d.SerialNumber, "device_name": d.DeviceName})
}

func (s *Service) declarativeManagement(w http.ResponseWriter, d *store.Device, msg map[string]any) {
	endpoint := asString(msg["Endpoint"])
	data, _ := msg["Data"].([]byte)
	out, err := s.ddm.Handle(d.UDID, endpoint, data)
	switch {
	case errors.Is(err, ddm.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
		return
	case errors.Is(err, ddm.ErrUnknownEndpoint):
		http.Error(w, "unknown endpoint", http.StatusBadRequest)
		return
	case err != nil:
		s.log.Error("ddm request failed", "udid", d.UDID, "endpoint", endpoint, "err", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if out == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(out)
}

// QueueInventory queues the standard inventory commands (skipping any that are already pending).
func (s *Service) QueueInventory(udid, reason string) int {
	d, err := s.store.GetDevice(udid)
	if err != nil || (d.EnrollmentStatus != "enrolled" && d.EnrollmentStatus != "pending") {
		return 0
	}
	types := []string{"DeviceInformation", "SecurityInfo", "InstalledApplicationList", "ManagedApplicationList", "ProfileList", "CertificateList", "ProvisioningProfileList"}
	if !d.UserEnrollment {
		types = append(types, "Restrictions")
	}
	if d.Supervised && !d.UserEnrollment {
		types = append(types, "AvailableOSUpdates")
	}
	n := 0
	for _, t := range types {
		if s.store.HasPendingCommand(udid, t, "inventory") {
			continue
		}
		cmd, _, err := s.BuildCommand(t, d, nil)
		if err != nil {
			continue
		}
		if _, err := s.Enqueue(udid, cmd, Meta{Source: "system", Ref: "inventory", CreatedBy: reason}); err == nil {
			n++
		}
	}
	return n
}

// QueueTelemetry queues a lightweight DeviceInformation for battery/storage/network sampling.
func (s *Service) QueueTelemetry(udid string) bool {
	if s.store.HasPendingCommand(udid, "DeviceInformation", "") {
		return false
	}
	queries := TelemetryQueries
	if d, err := s.store.GetDevice(udid); err == nil && d.UserEnrollment {
		queries = UserEnrollmentTelemetryQueries
	}
	_, err := s.Enqueue(udid, map[string]any{"RequestType": "DeviceInformation", "Queries": queries}, Meta{Source: "system", Ref: "telemetry"})
	return err == nil
}
