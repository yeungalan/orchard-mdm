package mdm

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"howett.net/plist"
)

// HandleConnect serves the ServerURL (PUT /mdm/connect): it records the
// result of the previous command and returns the next queued command.
func (s *Service) HandleConnect(w http.ResponseWriter, r *http.Request) {
	body, err := readBody(r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var msg map[string]any
	if _, err := plist.Unmarshal(body, &msg); err != nil {
		http.Error(w, "invalid plist", http.StatusBadRequest)
		return
	}
	udid := deviceID(msg)
	status := asString(msg["Status"])
	cert, err := s.requestCert(r, body)
	if err == nil {
		err = s.verifyIdentity(cert)
	}
	if err != nil {
		s.log.Warn("connect: identity rejected", "udid", udid, "err", err, "ip", ClientIP(r, s.trustProxy))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	d, err := s.store.GetDevice(udid)
	if err != nil || d.EnrollmentStatus == "unenrolled" {
		s.log.Warn("connect from unknown or unenrolled device", "udid", udid)
		http.Error(w, "unknown device", http.StatusUnauthorized)
		return
	}
	if err := s.authorizeDevice(d, cert); err != nil {
		s.log.Warn("connect: certificate mismatch", "udid", udid, "err", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.store.TouchDevice(udid)
	s.recordConnection(d, ClientIP(r, s.trustProxy))

	if asString(msg["UserID"]) != "" || asString(msg["UserShortName"]) != "" {
		// user channel: Orchard sends no user-scoped commands
		w.WriteHeader(http.StatusOK)
		return
	}
	if status != "" && status != "Idle" {
		s.processReport(d, asString(msg["CommandUUID"]), status, msg, body)
	}
	next, err := s.store.NextCommand(udid, status == store.StatusNotNow)
	if errors.Is(err, store.ErrNotFound) {
		w.WriteHeader(http.StatusOK)
		return
	}
	if err != nil {
		s.log.Error("next command", "udid", udid, "err", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	if err := s.store.MarkCommandSent(next.UUID); err != nil {
		s.log.Error("mark sent", "err", err)
	}
	s.log.Debug("sending command", "udid", udid, "type", next.RequestType, "uuid", next.UUID)
	w.Header().Set("Content-Type", "application/xml; charset=UTF-8")
	_, _ = w.Write(next.Payload)
}

// recordConnection keeps the public IP a device connects from (one sample per
// IP change, or hourly).
func (s *Service) recordConnection(d *store.Device, ip string) {
	if ip == "" || !s.SettingBool(SettingRecordConnectionIPs) {
		return
	}
	lastIP, ts := s.store.LastServerIPSample(d.UDID)
	if lastIP == ip && store.Now()-ts < 3600 {
		return
	}
	_ = s.store.InsertTelemetry(&store.TelemetrySample{DeviceID: d.UDID, Source: "server", Battery: -1, AvailableGB: -1, IP: ip})
	if d.LastIP != ip {
		_ = s.store.UpdateDevice(d.UDID, map[string]any{"last_ip": ip})
	}
}

// FormatErrorChain renders an MDM ErrorChain.
func FormatErrorChain(v any) string {
	list, _ := v.([]any)
	var parts []string
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		desc := asString(m["LocalizedDescription"])
		if desc == "" {
			desc = asString(m["USEnglishDescription"])
		}
		parts = append(parts, fmt.Sprintf("%s (%s %v)", desc, asString(m["ErrorDomain"]), m["ErrorCode"]))
	}
	return strings.Join(parts, "; ")
}

func (s *Service) processReport(d *store.Device, uuid, status string, msg map[string]any, body []byte) {
	errText := ""
	if status == store.StatusError || status == store.StatusCommandFormatError {
		errText = FormatErrorChain(msg["ErrorChain"])
		if errText == "" {
			errText = status
		}
	}
	cmd, err := s.store.CompleteCommand(d.UDID, uuid, status, body, errText)
	if err != nil {
		s.log.Warn("result for unknown command", "udid", d.UDID, "uuid", uuid, "status", status)
		return
	}
	if status == store.StatusNotNow {
		return
	}
	if status == store.StatusAcknowledged {
		s.handleResult(d, cmd, msg)
	}
	s.updateStates(d, cmd, status, errText, msg)
	typ := events.CommandCompleted
	level := "info"
	if status != store.StatusAcknowledged {
		typ = events.CommandFailed
		level = "warn"
	}
	if cmd.Source == "manual" || cmd.Source == "api" || status != store.StatusAcknowledged {
		msgText := cmd.RequestType + " " + strings.ToLower(status)
		if errText != "" {
			msgText += ": " + errText
		}
		s.event(d.UDID, "command."+strings.ToLower(status), level, msgText, map[string]any{"command_uuid": cmd.UUID})
	}
	s.bus.Publish(typ, d.UDID, map[string]any{"command_uuid": cmd.UUID, "request_type": cmd.RequestType, "status": status, "error": errText, "source": cmd.Source, "ref": cmd.Ref})
}

// updateStates tracks the managed profile/app state machine.
func (s *Service) updateStates(d *store.Device, cmd *store.Command, status, errText string, msg map[string]any) {
	ok := status == store.StatusAcknowledged
	if st, err := s.store.FindProfileStateByCommand(cmd.UUID); err == nil {
		switch cmd.RequestType {
		case "InstallProfile":
			if ok {
				st.Status, st.Error = "installed", ""
			} else {
				st.Status, st.Error = "failed", errText
			}
			_ = s.store.SetProfileState(st)
		case "RemoveProfile":
			if ok {
				_ = s.store.DeleteProfileState(st.DeviceID, st.ProfileID)
			} else {
				st.Status, st.Error = "remove_failed", errText
				_ = s.store.SetProfileState(st)
			}
		}
	}
	if st, err := s.store.FindAppStateByCommand(cmd.UUID); err == nil {
		switch cmd.RequestType {
		case "InstallApplication":
			if ok {
				st.Status, st.Error = "installing", ""
				if state := asString(msg["State"]); state == "Managed" {
					st.Status = "installed"
				}
			} else {
				st.Status, st.Error = "failed", errText
			}
			_ = s.store.SetAppState(st)
		case "RemoveApplication":
			if ok {
				st.Status, st.Error = "removed", ""
			} else {
				st.Status, st.Error = "failed", errText
			}
			_ = s.store.SetAppState(st)
		}
	}
}
