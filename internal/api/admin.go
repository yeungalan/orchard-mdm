package api

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/apns"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"github.com/yeungalan/orchard-mdm/internal/vpp"
)

func decodeB64(s, what string) ([]byte, error) {
	if i := strings.Index(s, ";base64,"); strings.HasPrefix(s, "data:") && i > 0 {
		s = s[i+8:]
	}
	b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) == 0 {
		return nil, badRequest("upload the %s file", what)
	}
	return b, nil
}

func sendFile(w http.ResponseWriter, name, ctype string, data []byte) error {
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
	_, _ = w.Write(data)
	return nil
}

// ---- enrollment ----

func (a *API) readiness() []map[string]any {
	base := a.MDM.PublicURL()
	push := a.APNs.CertInfo()
	signer, _, _ := a.MDM.SigningIdentity()
	signDetail := ""
	if signer != nil {
		name := signer.Subject.CommonName
		if name == "" && len(signer.DNSNames) > 0 {
			name = signer.DNSNames[0]
		}
		signDetail = "Signed as " + name + ", valid until " + signer.NotAfter.Format("Jan 2, 2006")
	}
	checks := []map[string]any{
		{"id": "url", "label": "Public HTTPS URL configured", "ok": strings.HasPrefix(base, "https://"), "detail": base,
			"help": "Devices must reach the server over HTTPS with a trusted certificate. Set -url or Settings → General."},
		{"id": "apns", "label": "APNs push certificate installed", "ok": push.Configured && push.Error == "" && push.DaysLeft > 0,
			"detail": push.Topic, "help": "Settings → Apple Push: create a CSR, get it signed, and upload Apple's certificate."},
		{"id": "signing", "label": "Profiles signed with the TLS certificate", "ok": signer != nil, "detail": signDetail,
			"help": "Optional. When Orchard terminates TLS itself, profiles are signed and shown as Verified."},
	}
	if push.Configured && push.DaysLeft <= 30 {
		checks[1]["warning"] = fmt.Sprintf("Push certificate expires in %d days", push.DaysLeft)
	}
	return checks
}

func (a *API) enrollmentInfo(w http.ResponseWriter, r *http.Request, p *Principal) error {
	base := a.MDM.PublicURL()
	return ok(w, map[string]any{
		"enroll_url":     base + "/enroll",
		"ade_url":        base + "/mdm/ade/enroll",
		"checkin_url":    base + "/mdm/checkin",
		"server_url":     base + "/mdm/connect",
		"scep_url":       base + "/scep",
		"require_token":  a.MDM.SettingBool(mdm.SettingEnrollRequireToken),
		"readiness":      a.readiness(),
		"mdm_identifier": a.MDM.MDMIdentifier(),
		"access_rights":  mdm.AccessRights,
	})
}

func (a *API) listEnrollmentTokens(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ts, err := a.Store.ListEnrollmentTokens()
	if err != nil {
		return err
	}
	type view struct {
		*store.EnrollmentToken
		URL    string `json:"url"`
		Usable bool   `json:"usable"`
	}
	out := make([]view, 0, len(ts))
	for _, t := range ts {
		out = append(out, view{t, a.MDM.PublicURL() + "/enroll/" + t.Token, t.Usable()})
	}
	return ok(w, map[string]any{"items": out})
}

type tokenReq struct {
	Name         string  `json:"name"`
	Ownership    string  `json:"ownership"`
	GroupIDs     []int64 `json:"group_ids"`
	AssignedUser string  `json:"assigned_user"`
	MaxUses      int     `json:"max_uses"`
	ExpiresAt    int64   `json:"expires_at"`
	Enabled      *bool   `json:"enabled"`
}

func applyTokenReq(t *store.EnrollmentToken, req *tokenReq) error {
	if strings.TrimSpace(req.Name) == "" {
		return badRequest("name is required")
	}
	switch req.Ownership {
	case "", "corporate", "personal":
	default:
		return badRequest("ownership must be corporate or personal")
	}
	if req.Ownership == "" {
		req.Ownership = "corporate"
	}
	t.Name, t.Ownership, t.GroupIDs, t.AssignedUser, t.MaxUses, t.ExpiresAt = strings.TrimSpace(req.Name), req.Ownership, req.GroupIDs, req.AssignedUser, req.MaxUses, req.ExpiresAt
	if t.GroupIDs == nil {
		t.GroupIDs = []int64{}
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	return nil
}

func (a *API) createEnrollmentToken(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req tokenReq
	if err := decode(r, &req); err != nil {
		return err
	}
	t := &store.EnrollmentToken{Enabled: true}
	if err := applyTokenReq(t, &req); err != nil {
		return err
	}
	if err := a.Store.CreateEnrollmentToken(t); err != nil {
		return err
	}
	a.audit(r, p, "enrollment.token_created", t.Name, nil)
	return ok(w, t)
}

func (a *API) updateEnrollmentToken(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	t, err := a.Store.GetEnrollmentToken(id)
	if err != nil {
		return err
	}
	var req tokenReq
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := applyTokenReq(t, &req); err != nil {
		return err
	}
	if err := a.Store.UpdateEnrollmentToken(t); err != nil {
		return err
	}
	a.audit(r, p, "enrollment.token_updated", t.Name, nil)
	return ok(w, t)
}

func (a *API) deleteEnrollmentToken(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := a.Store.DeleteEnrollmentToken(id); err != nil {
		return err
	}
	a.audit(r, p, "enrollment.token_deleted", fmt.Sprint(id), nil)
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) downloadEnrollmentProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ref := "manual"
	if id := queryInt(r, "token_id", 0); id > 0 {
		t, err := a.Store.GetEnrollmentToken(int64(id))
		if err != nil {
			return err
		}
		ref = fmt.Sprintf("token:%d", t.ID)
	}
	prof, err := a.MDM.EnrollmentProfile(mdm.EnrollmentOptions{Ref: ref, ChallengeTTL: 7 * 24 * time.Hour})
	if err != nil {
		return badRequest("%v", err)
	}
	a.audit(r, p, "enrollment.profile_downloaded", ref, nil)
	return sendFile(w, "enroll.mobileconfig", "application/x-apple-aspen-config", prof)
}

// ---- ADE ----

func (a *API) listADEServers(w http.ResponseWriter, r *http.Request, p *Principal) error {
	servers, err := a.Store.ListDEPServers()
	if err != nil {
		return err
	}
	profiles, _ := a.Store.ListDEPProfiles()
	return ok(w, map[string]any{"items": servers, "profiles": profiles})
}

func (a *API) createADEServer(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	srv, err := a.DEP.CreateServer(req.Name)
	if err != nil {
		return err
	}
	a.audit(r, p, "ade.server_created", srv.Name, nil)
	return ok(w, srv)
}

func (a *API) updateADEServer(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	srv, err := a.Store.GetDEPServer(id)
	if err != nil {
		return err
	}
	var req struct {
		Name             *string `json:"name"`
		DefaultProfileID *int64  `json:"default_profile_id"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		srv.Name = strings.TrimSpace(*req.Name)
	}
	if req.DefaultProfileID != nil {
		srv.DefaultProfileID = *req.DefaultProfileID
	}
	if err := a.Store.UpdateDEPServer(srv); err != nil {
		return err
	}
	a.audit(r, p, "ade.server_updated", srv.Name, nil)
	return ok(w, srv)
}

func (a *API) deleteADEServer(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := a.Store.DeleteDEPServer(id); err != nil {
		return err
	}
	a.audit(r, p, "ade.server_deleted", fmt.Sprint(id), nil)
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) adePublicKey(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	srv, err := a.Store.GetDEPServer(id)
	if err != nil {
		return err
	}
	return sendFile(w, "orchard-ade-publickey.pem", "application/x-pem-file", []byte(srv.CertPEM))
}

func (a *API) adeUploadToken(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		Data string `json:"data"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	data, err := decodeB64(req.Data, "server token (.p7m)")
	if err != nil {
		return err
	}
	srv, err := a.DEP.UploadToken(r.Context(), id, data)
	if err != nil {
		return badRequest("%v", err)
	}
	a.audit(r, p, "ade.token_uploaded", srv.Name, map[string]string{"org": srv.OrgName})
	go func() {
		if _, err := a.DEP.Sync(r.Context(), id); err != nil {
			a.Log.Warn("initial ade sync", "err", err)
		}
	}()
	return ok(w, srv)
}

func (a *API) adeSync(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	res, err := a.DEP.Sync(r.Context(), id)
	if err != nil {
		return errf(http.StatusBadGateway, "sync failed: %v", err)
	}
	a.audit(r, p, "ade.sync", fmt.Sprint(id), res)
	return ok(w, res)
}

func (a *API) listADEDevices(w http.ResponseWriter, r *http.Request, p *Principal) error {
	sid, _ := strconv.ParseInt(r.URL.Query().Get("server_id"), 10, 64)
	devs, err := a.Store.ListDEPDevices(sid, r.URL.Query().Get("q"))
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": devs})
}

func (a *API) listADEProfiles(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ps, err := a.Store.ListDEPProfiles()
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": ps, "skip_items": adeSkipItems})
}

var adeSkipItems = []string{"Accessibility", "Appearance", "AppleID", "AppStore", "Biometric", "CameraButton", "DeviceToDeviceMigration", "Diagnostics",
	"EnableLockdownMode", "FileVault", "iCloudDiagnostics", "iCloudStorage", "iMessageAndFaceTime", "Intelligence", "Keyboard", "Location", "MessagingActivationUsingPhoneNumber",
	"Passcode", "Payment", "Privacy", "Restore", "RestoreCompleted", "Safety", "ScreenSaver", "ScreenTime", "SIMSetup", "Siri", "SoftwareUpdate",
	"TapToSetup", "TermsOfAddress", "TOS", "UpdateCompleted", "WatchMigration", "Welcome", "Wallpaper"}

func validateADEProfile(p *store.DEPProfile) error {
	if strings.TrimSpace(p.Name) == "" {
		return badRequest("name is required")
	}
	if p.Config == nil {
		p.Config = map[string]any{}
	}
	if v, ok := p.Config["is_mdm_removable"].(bool); ok && v {
		if s, _ := p.Config["is_supervised"].(bool); !s {
			// allowed but unusual
		}
	}
	return nil
}

func (a *API) createADEProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req store.DEPProfile
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := validateADEProfile(&req); err != nil {
		return err
	}
	if err := a.Store.CreateDEPProfile(&req); err != nil {
		return err
	}
	a.audit(r, p, "ade.profile_created", req.Name, nil)
	return ok(w, req)
}

func (a *API) updateADEProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	prof, err := a.Store.GetDEPProfile(id)
	if err != nil {
		return err
	}
	var req store.DEPProfile
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := validateADEProfile(&req); err != nil {
		return err
	}
	prof.Name, prof.Config, prof.GroupIDs = req.Name, req.Config, req.GroupIDs
	if prof.GroupIDs == nil {
		prof.GroupIDs = []int64{}
	}
	if err := a.Store.UpdateDEPProfile(prof); err != nil {
		return err
	}
	a.audit(r, p, "ade.profile_updated", prof.Name, nil)
	return ok(w, prof)
}

func (a *API) deleteADEProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := a.Store.DeleteDEPProfile(id); err != nil {
		return err
	}
	a.audit(r, p, "ade.profile_deleted", fmt.Sprint(id), nil)
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) adeAssign(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		ProfileID int64    `json:"profile_id"`
		Serials   []string `json:"serials"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.ProfileID == 0 || len(req.Serials) == 0 {
		return badRequest("choose a profile and at least one device")
	}
	n, err := a.DEP.AssignProfile(r.Context(), req.ProfileID, req.Serials)
	a.audit(r, p, "ade.assign", fmt.Sprint(req.ProfileID), map[string]any{"serials": len(req.Serials), "assigned": n})
	if err != nil {
		return ok(w, map[string]any{"assigned": n, "error": err.Error()})
	}
	return ok(w, map[string]any{"assigned": n})
}

func (a *API) adeUnassign(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Serials []string `json:"serials"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := a.DEP.UnassignProfile(r.Context(), req.Serials); err != nil {
		return errf(http.StatusBadGateway, "%v", err)
	}
	a.audit(r, p, "ade.unassign", "", map[string]int{"serials": len(req.Serials)})
	return ok(w, map[string]bool{"ok": true})
}

// ---- VPP ----

func (a *API) listVPPTokens(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ts, err := a.Store.ListVPPTokens()
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": ts})
}

func (a *API) createVPPToken(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Name string `json:"name"`
		Data string `json:"data"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	data, err := decodeB64(req.Data, "content token (.vpptoken)")
	if err != nil {
		return err
	}
	raw, info, err := vpp.ParseToken(data)
	if err != nil {
		return badRequest("%v", err)
	}
	t := &store.VPPToken{Name: strings.TrimSpace(req.Name), Token: raw, OrgName: info.OrgName, ExpDate: info.Expiry().Unix()}
	if t.Name == "" {
		t.Name = info.OrgName
	}
	if err := a.Store.CreateVPPToken(t); err != nil {
		return err
	}
	c := vpp.New(a.Cfg.VPPURL, raw)
	if _, err := c.ClaimToken(r.Context(), a.MDM.MDMIdentifier(), "Orchard MDM"); err != nil {
		t.LastError = "claim: " + err.Error()
		_ = a.Store.UpdateVPPToken(t)
	}
	go func() {
		_, _ = vpp.SyncToken(r.Context(), a.Store, a.Cfg.VPPURL, a.ITunes, t)
	}()
	a.audit(r, p, "vpp.token_added", t.Name, nil)
	return ok(w, t)
}

func (a *API) deleteVPPToken(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := a.Store.DeleteVPPToken(id); err != nil {
		return err
	}
	a.audit(r, p, "vpp.token_deleted", fmt.Sprint(id), nil)
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) syncVPPToken(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	t, err := a.Store.GetVPPToken(id)
	if err != nil {
		return err
	}
	n, err := vpp.SyncToken(r.Context(), a.Store, a.Cfg.VPPURL, a.ITunes, t)
	if err != nil {
		return errf(http.StatusBadGateway, "sync failed: %v", err)
	}
	return ok(w, map[string]int{"assets": n})
}

func (a *API) listVPPAssets(w http.ResponseWriter, r *http.Request, p *Principal) error {
	assets, err := a.Store.ListVPPAssets(0)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": assets})
}

// ---- APNs ----

func (a *API) apnsStatus(w http.ResponseWriter, r *http.Request, p *Principal) error {
	csr, _ := a.APNs.PendingCSR()
	return ok(w, map[string]any{"certificate": a.APNs.CertInfo(), "pending_csr": len(csr) > 0})
}

func (a *API) apnsCSR(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req apns.CSRRequest
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Org == "" {
		req.Org = a.MDM.Setting(mdm.SettingOrgName)
	}
	csr, err := a.APNs.GenerateCSR(req)
	if err != nil {
		return err
	}
	a.audit(r, p, "apns.csr_generated", req.Email, nil)
	return ok(w, map[string]string{"csr": string(csr)})
}

func (a *API) apnsDownloadCSR(w http.ResponseWriter, r *http.Request, p *Principal) error {
	csr, err := a.APNs.PendingCSR()
	if err != nil {
		return badRequest("%v", err)
	}
	return sendFile(w, "orchard-push.csr", "application/pkcs10", csr)
}

func (a *API) apnsVendorSign(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		P12      string `json:"p12"`
		Password string `json:"password"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	data, err := decodeB64(req.P12, "MDM vendor certificate (.p12)")
	if err != nil {
		return err
	}
	cert, key, chain, err := apns.ParseVendorP12(data, req.Password)
	if err != nil {
		return badRequest("%v", err)
	}
	out, err := a.APNs.VendorSign(r.Context(), cert, key, chain)
	if err != nil {
		return badRequest("%v", err)
	}
	a.audit(r, p, "apns.vendor_signed", cert.Subject.CommonName, nil)
	return sendFile(w, "PushCertificateRequest.plist.b64", "application/octet-stream", out)
}

func (a *API) apnsMDMCertRequest(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Email string `json:"email"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if !strings.Contains(req.Email, "@") {
		return badRequest("enter the email address registered at mdmcert.download")
	}
	if err := a.APNs.RequestMDMCertDownload(r.Context(), req.Email, ""); err != nil {
		return errf(http.StatusBadGateway, "%v", err)
	}
	a.audit(r, p, "apns.mdmcert_requested", req.Email, nil)
	return ok(w, map[string]string{"message": "Request submitted. mdmcert.download will email you an encrypted file; upload it here to decrypt it."})
}

func (a *API) apnsMDMCertDecrypt(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Data string `json:"data"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	data, err := decodeB64(req.Data, "encrypted mdmcert.download")
	if err != nil {
		return err
	}
	out, err := a.APNs.DecryptMDMCertResponse(data)
	if err != nil {
		return badRequest("%v", err)
	}
	return sendFile(w, "PushCertificateRequest.plist.b64", "application/octet-stream", out)
}

func (a *API) apnsUploadCert(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Data             string `json:"data"`
		Password         string `json:"password"`
		AppleID          string `json:"apple_id"`
		AllowTopicChange bool   `json:"allow_topic_change"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	data, err := decodeB64(req.Data, "push certificate")
	if err != nil {
		return err
	}
	res, err := a.APNs.InstallCertificate(data, req.Password, req.AppleID, req.AllowTopicChange)
	if err != nil {
		if res != nil && res.TopicChanged {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "topic_changed": true})
			return nil
		}
		return badRequest("%v", err)
	}
	a.audit(r, p, "apns.certificate_installed", res.Topic, map[string]any{"expires": res.NotAfter, "apple_id": req.AppleID})
	return ok(w, res)
}

func (a *API) apnsTest(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		UDID string `json:"udid"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	out, err := a.runAction(r.Context(), req.UDID, "push", p)
	if err != nil {
		return err
	}
	return ok(w, out)
}

// ---- settings ----

var publicSettings = []string{
	mdm.SettingOrgName, mdm.SettingPublicURL, mdm.SettingSupportEmail, mdm.SettingSupportPhone, mdm.SettingSupportURL, mdm.SettingEnrollRequireToken,
	mdm.SettingInventoryHours, mdm.SettingTelemetryMinutes, mdm.SettingLostModeLocMinutes, mdm.SettingSignProfiles, mdm.SettingSCEPValidityDays,
	mdm.SettingCommandRetention, mdm.SettingTelemetryRetention, mdm.SettingPortalEnabled, mdm.SettingADESyncMinutes, mdm.SettingExtraTrustCerts,
	mdm.SettingEnrollConsentText, mdm.SettingCommandExpiryDays, mdm.SettingRecordConnectionIPs, "compliance_default", "ade_allow_unknown_serials",
	mdm.SettingAgentReportSeconds,
}

func (a *API) getSettings(w http.ResponseWriter, r *http.Request, p *Principal) error {
	out := map[string]string{}
	for _, k := range publicSettings {
		out[k] = a.MDM.Setting(k)
	}
	return ok(w, map[string]any{"settings": out, "public_url_locked": a.Cfg.PublicURL != "", "effective_public_url": a.MDM.PublicURL(), "tls_mode": a.TLSMode})
}

func (a *API) putSettings(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req map[string]string
	if err := decode(r, &req); err != nil {
		return err
	}
	allowed := map[string]bool{}
	for _, k := range publicSettings {
		allowed[k] = true
	}
	changed := map[string]string{}
	for k, v := range req {
		if !allowed[k] {
			return badRequest("unknown setting %q", k)
		}
		v = strings.TrimSpace(v)
		switch k {
		case mdm.SettingPublicURL:
			v = strings.TrimRight(v, "/")
			if v != "" && !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "http://") {
				return badRequest("the public URL must start with https://")
			}
		case mdm.SettingInventoryHours, mdm.SettingTelemetryMinutes, mdm.SettingLostModeLocMinutes, mdm.SettingSCEPValidityDays,
			mdm.SettingCommandRetention, mdm.SettingTelemetryRetention, mdm.SettingADESyncMinutes, mdm.SettingCommandExpiryDays, mdm.SettingAgentReportSeconds:
			if n, err := strconv.Atoi(v); err != nil || n < 0 {
				return badRequest("%s must be a non-negative number", k)
			}
		case mdm.SettingExtraTrustCerts:
			if v != "" {
				if _, err := pki.ParseCertsPEM([]byte(v)); err != nil {
					return badRequest("extra trusted certificates must be PEM")
				}
			}
		}
		if err := a.Store.SetSetting(k, v); err != nil {
			return err
		}
		changed[k] = v
	}
	if _, ok := changed[mdm.SettingExtraTrustCerts]; ok {
		changed[mdm.SettingExtraTrustCerts] = "(updated)"
	}
	a.audit(r, p, "settings.updated", "", changed)
	return a.getSettings(w, r, p)
}

// ---- webhooks ----

func (a *API) listWebhooks(w http.ResponseWriter, r *http.Request, p *Principal) error {
	hooks, err := a.Store.ListWebhooks()
	if err != nil {
		return err
	}
	if Roles[p.Role] < PermAdmin {
		for _, h := range hooks {
			h.Secret = ""
		}
	}
	return ok(w, map[string]any{"items": hooks, "events": events.AllTypes})
}

func (a *API) saveWebhook(h *store.Webhook, r *http.Request) error {
	var req store.Webhook
	if err := decode(r, &req); err != nil {
		return err
	}
	if !strings.HasPrefix(req.URL, "https://") && !strings.HasPrefix(req.URL, "http://") {
		return badRequest("URL must start with https:// or http://")
	}
	h.Name, h.URL, h.Events, h.Enabled = strings.TrimSpace(req.Name), req.URL, req.Events, req.Enabled
	if h.Name == "" {
		h.Name = req.URL
	}
	if req.Secret != "" {
		h.Secret = req.Secret
	}
	if h.Events == nil {
		h.Events = []string{}
	}
	return nil
}

func (a *API) createWebhook(w http.ResponseWriter, r *http.Request, p *Principal) error {
	h := &store.Webhook{}
	if err := a.saveWebhook(h, r); err != nil {
		return err
	}
	if h.Secret == "" {
		h.Secret = randomHex(20)
	}
	if err := a.Store.CreateWebhook(h); err != nil {
		return err
	}
	a.audit(r, p, "webhook.created", h.Name, nil)
	return ok(w, h)
}

func (a *API) updateWebhook(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	h, err := a.Store.GetWebhook(id)
	if err != nil {
		return err
	}
	if err := a.saveWebhook(h, r); err != nil {
		return err
	}
	if err := a.Store.UpdateWebhook(h); err != nil {
		return err
	}
	a.audit(r, p, "webhook.updated", h.Name, nil)
	return ok(w, h)
}

func (a *API) deleteWebhook(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	if err := a.Store.DeleteWebhook(id); err != nil {
		return err
	}
	a.audit(r, p, "webhook.deleted", fmt.Sprint(id), nil)
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) testWebhook(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	h, err := a.Store.GetWebhook(id)
	if err != nil {
		return err
	}
	status, err := a.Webhooks.Test(h)
	if err != nil {
		return ok(w, map[string]any{"ok": false, "status": status, "error": err.Error()})
	}
	return ok(w, map[string]any{"ok": true, "status": status})
}

// ---- PKI ----

func (a *API) downloadCA(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ca, _ := a.MDM.CA()
	return sendFile(w, "orchard-device-ca.pem", "application/x-pem-file", []byte(pki.CertPEM(ca)))
}

func (a *API) listIssuedCerts(w http.ResponseWriter, r *http.Request, p *Principal) error {
	certs, err := a.Store.ListIssuedCerts(queryInt(r, "limit", 200))
	if err != nil {
		return err
	}
	type certView struct {
		*store.IssuedCert
		DeviceName string `json:"device_name,omitempty"`
	}
	names := map[string]string{}
	out := make([]certView, len(certs))
	for i, c := range certs {
		if _, seen := names[c.DeviceID]; !seen && c.DeviceID != "" {
			if d, err := a.Store.GetDevice(c.DeviceID); err == nil {
				names[c.DeviceID] = d.DeviceName
			} else {
				names[c.DeviceID] = ""
			}
		}
		out[i] = certView{IssuedCert: c, DeviceName: names[c.DeviceID]}
	}
	ca, _ := a.MDM.CA()
	return ok(w, map[string]any{"items": out, "ca": map[string]any{"subject": ca.Subject.String(), "not_after": ca.NotAfter, "fingerprint": pki.Fingerprint(ca)}})
}

// ---- events / audit / system ----

func (a *API) listEvents(w http.ResponseWriter, r *http.Request, p *Principal) error {
	evs, err := a.Store.ListEvents("", r.URL.Query().Get("type"), queryInt(r, "limit", 100), queryInt(r, "offset", 0))
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": evs})
}

// auditView adds a readable name and console link for the entry's target.
type auditView struct {
	*store.AuditEntry
	TargetLabel string `json:"target_label,omitempty"`
	TargetHref  string `json:"target_href,omitempty"`
}

func (a *API) listAudit(w http.ResponseWriter, r *http.Request, p *Principal) error {
	items, total, err := a.Store.ListAudit(r.URL.Query().Get("q"), queryInt(r, "limit", 100), queryInt(r, "offset", 0))
	if err != nil {
		return err
	}
	type ref struct{ label, href string }
	cache := map[string]ref{}
	resolve := func(e *store.AuditEntry) ref {
		key := e.Target
		if strings.HasPrefix(e.Action, "device.") {
			key = "device:" + e.Target
		}
		if v, hit := cache[key]; hit {
			return v
		}
		var v ref
		kind, idStr, _ := strings.Cut(key, ":")
		id, _ := strconv.ParseInt(idStr, 10, 64)
		switch kind {
		case "device":
			if d, err := a.Store.GetDevice(idStr); err == nil {
				v = ref{d.DeviceName, "#/devices/" + idStr}
				if v.label == "" {
					v.label = d.SerialNumber
				}
			}
		case "profile":
			if x, err := a.Store.GetProfile(id); err == nil {
				v = ref{x.Name, "#/profiles/" + idStr}
			}
		case "app":
			if x, err := a.Store.GetApp(id); err == nil {
				v = ref{x.Name, "#/apps/" + idStr}
			}
		case "declaration":
			if x, err := a.Store.GetDeclaration(id); err == nil {
				v = ref{x.Name, "#/declarations/" + idStr}
			}
		case "compliance":
			if x, err := a.Store.GetCompliancePolicy(id); err == nil {
				v = ref{x.Name, "#/compliance/" + idStr}
			}
		}
		cache[key] = v
		return v
	}
	out := make([]auditView, len(items))
	for i, e := range items {
		v := resolve(e)
		out[i] = auditView{AuditEntry: e, TargetLabel: v.label, TargetHref: v.href}
	}
	return ok(w, map[string]any{"items": out, "total": total})
}

func (a *API) variables(w http.ResponseWriter, r *http.Request, p *Principal) error {
	return ok(w, map[string]any{"items": mdm.VariableNames()})
}

func (a *API) system(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var dbSize int64
	if fi, err := os.Stat(filepath.Join(a.Cfg.DataDir, "orchard.db")); err == nil {
		dbSize = fi.Size()
	}
	ca, _ := a.MDM.CA()
	return ok(w, map[string]any{
		"version": a.Version, "go_version": runtime.Version(), "started": a.Started, "uptime_seconds": int64(time.Since(a.Started).Seconds()),
		"db_size": dbSize, "data_dir": a.Cfg.DataDir, "public_url": a.MDM.PublicURL(), "tls_mode": a.TLSMode, "listen": a.Cfg.Listen,
		"ca": map[string]any{"subject": ca.Subject.String(), "not_after": ca.NotAfter}, "readiness": a.readiness(),
		"wipe_supported": false,
	})
}

func (a *API) dashboard(w http.ResponseWriter, r *http.Request, p *Principal) error {
	osCounts, _ := a.Store.CountBy("os_version")
	modelCounts, _ := a.Store.CountBy("product_name")
	compliance, _ := a.Store.CountBy("compliance")
	ownership, _ := a.Store.CountBy("ownership")
	enrollTypes, _ := a.Store.CountBy("enrollment_type")
	cmdStats, _ := a.Store.CommandStats(24 * 3600)
	recent, _ := a.Store.ListEvents("", "", 15, 0)
	devs, _ := a.Store.EnrolledDevices()
	var lowBattery, lowStorage, stale, lost, noPasscode []DeviceSummary
	now := store.Now()
	for _, d := range devs {
		s := summarize(d)
		if d.BatteryLevel >= 0 && d.BatteryLevel < 0.2 {
			lowBattery = append(lowBattery, s)
		}
		if d.CapacityGB > 0 && d.AvailableGB < 2 {
			lowStorage = append(lowStorage, s)
		}
		if d.LastSeen > 0 && now-d.LastSeen > 7*86400 {
			stale = append(stale, s)
		}
		if d.LostMode {
			lost = append(lost, s)
		}
		if d.LastInventory > 0 && !d.PasscodePresent {
			noPasscode = append(noPasscode, s)
		}
	}
	type kv struct {
		Key   string `json:"key"`
		Count int    `json:"count"`
	}
	sorted := func(m map[string]int, limit int) []kv {
		out := make([]kv, 0, len(m))
		for k, v := range m {
			if k == "" {
				k = "Unknown"
			}
			out = append(out, kv{k, v})
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Count != out[j].Count {
				return out[i].Count > out[j].Count
			}
			return out[i].Key < out[j].Key
		})
		if limit > 0 && len(out) > limit {
			out = out[:limit]
		}
		return out
	}
	adeDevices, _ := a.Store.ListDEPDevices(0, "")
	unassigned := 0
	for _, d := range adeDevices {
		if d.ProfileUUID == "" && d.AssignedProfileID == 0 {
			unassigned++
		}
	}
	nonEmpty := func(s []DeviceSummary) []DeviceSummary {
		if s == nil {
			return []DeviceSummary{}
		}
		if len(s) > 10 {
			return s[:10]
		}
		return s
	}
	return ok(w, map[string]any{
		"devices": map[string]int{
			"enrolled": a.Store.CountDevices("enrolled"), "pending": a.Store.CountDevices("pending"), "unenrolled": a.Store.CountDevices("unenrolled"),
			"total": a.Store.CountDevices(""), "low_battery": len(lowBattery), "low_storage": len(lowStorage), "stale": len(stale), "lost_mode": len(lost),
			"no_passcode": len(noPasscode),
		},
		"compliance": compliance, "os_versions": sorted(osCounts, 12), "models": sorted(modelCounts, 10), "ownership": ownership,
		"enrollment_types": enrollTypes, "commands_24h": cmdStats, "pending_commands": a.Store.PendingCommandCount(""), "recent_events": recent,
		"apns": a.APNs.CertInfo(), "readiness": a.readiness(), "ade": map[string]int{"devices": len(adeDevices), "unassigned": unassigned},
		"attention": map[string]any{"low_battery": nonEmpty(lowBattery), "low_storage": nonEmpty(lowStorage), "stale": nonEmpty(stale), "lost_mode": nonEmpty(lost)},
	})
}
