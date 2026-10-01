// Package portal serves the end-user pages (enrollment and the self-service
// Company Portal), the companion agent API and enterprise app downloads.
package portal

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/apps"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"rsc.io/qr"
)

//go:embed templates/*.html
var templateFS embed.FS

// Deps are the services the portal uses.
type Deps struct {
	Store      *store.Store
	MDM        *mdm.Service
	Log        *slog.Logger
	TrustProxy bool
	DataDir    string
}

type portal struct {
	Deps
	tmpl *template.Template
}

// Register adds the public routes.
func Register(mux *http.ServeMux, d Deps) {
	p := &portal{Deps: d}
	funcs := template.FuncMap{
		"pct": func(f float64) string {
			if f < 0 {
				return "—"
			}
			return fmt.Sprintf("%.0f%%", f*100)
		},
		"ago":   ago,
		"gb":    func(f float64) string { return fmt.Sprintf("%.1f GB", f) },
		"model": ModelName,
	}
	p.tmpl = template.Must(template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html"))
	mux.HandleFunc("GET /enroll", p.enrollLanding)
	mux.HandleFunc("POST /enroll", p.enrollCode)
	mux.HandleFunc("GET /enroll/profile", p.openProfile)
	mux.HandleFunc("GET /enroll/{token}", p.enrollToken)
	mux.HandleFunc("GET /enroll/{token}/profile", p.tokenProfile)
	mux.HandleFunc("GET /enroll/{token}/qr.png", p.enrollQR)
	mux.HandleFunc("GET /portal/{token}", p.portalPage)
	mux.HandleFunc("POST /portal/{token}/apps/{id}", p.portalInstall)
	mux.HandleFunc("POST /portal/{token}/sync", p.portalSync)
	mux.HandleFunc("POST /agent/v1/report", p.agentReport)
	mux.HandleFunc("GET /agent/v1/config", p.agentConfig)
	mux.HandleFunc("GET /files/apps/{id}/{secret}/manifest.plist", p.appManifest)
	mux.HandleFunc("GET /files/apps/{id}/{secret}/app.ipa", p.appIPA)
	mux.HandleFunc("GET /ca.pem", p.caPEM)
}

func ago(ts int64) string {
	if ts == 0 {
		return "never"
	}
	d := time.Since(time.Unix(ts, 0))
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

type page struct {
	Org          string
	Title        string
	Support      map[string]string
	Error        string
	Notice       string
	Token        string
	TokenName    string
	ProfileURL   string
	QRURL        string
	EnrollURL    string
	IsAppleMob   bool
	RequireToken bool
	Ready        bool
	Device       *store.Device
	Apps         []portalApp
	Reasons      []string
}

type portalApp struct {
	ID       int64
	Name     string
	Seller   string
	Icon     string
	Status   string
	Required bool
}

func (p *portal) base(r *http.Request, title string) *page {
	ua := r.UserAgent()
	return &page{
		Org:   p.MDM.Setting(mdm.SettingOrgName),
		Title: title,
		Support: map[string]string{
			"email": p.MDM.Setting(mdm.SettingSupportEmail), "phone": p.MDM.Setting(mdm.SettingSupportPhone), "url": p.MDM.Setting(mdm.SettingSupportURL),
		},
		IsAppleMob:   strings.Contains(ua, "iPhone") || strings.Contains(ua, "iPad") || (strings.Contains(ua, "Macintosh") && strings.Contains(ua, "Mobile")),
		RequireToken: p.MDM.SettingBool(mdm.SettingEnrollRequireToken),
	}
}

func (p *portal) render(w http.ResponseWriter, name string, data *page, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := p.tmpl.ExecuteTemplate(w, name, data); err != nil {
		p.Log.Error("render", "template", name, "err", err)
	}
}

// ---- enrollment ----

func (p *portal) enrollReady() error {
	if p.MDM.PublicURL() == "" {
		return mdm.ErrNoPublicURL
	}
	if p.MDM.PushManager().Topic() == "" {
		return mdm.ErrNoPushCert
	}
	return nil
}

func (p *portal) enrollLanding(w http.ResponseWriter, r *http.Request) {
	pg := p.base(r, "Enroll your device")
	if err := p.enrollReady(); err != nil {
		pg.Error = "Enrollment isn't available yet. Your IT team still needs to finish setting up the server."
		p.render(w, "enroll.html", pg, http.StatusServiceUnavailable)
		return
	}
	pg.Ready = true
	if !pg.RequireToken {
		pg.ProfileURL = "/enroll/profile"
	}
	p.render(w, "enroll.html", pg, http.StatusOK)
}

func (p *portal) enrollCode(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	code := strings.TrimSpace(r.PostForm.Get("code"))
	code = strings.ToLower(strings.ReplaceAll(code, " ", ""))
	if t, err := p.Store.GetEnrollmentTokenByValue(code); err == nil && t.Usable() {
		http.Redirect(w, r, "/enroll/"+t.Token, http.StatusSeeOther)
		return
	}
	pg := p.base(r, "Enroll your device")
	pg.Ready = p.enrollReady() == nil
	pg.Error = "That enrollment code isn't valid. Check it with your IT team and try again."
	p.render(w, "enroll.html", pg, http.StatusOK)
}

func (p *portal) serveProfile(w http.ResponseWriter, r *http.Request, ref string) {
	prof, err := p.MDM.EnrollmentProfile(mdm.EnrollmentOptions{Ref: ref, ChallengeTTL: 24 * time.Hour})
	if err != nil {
		pg := p.base(r, "Enroll your device")
		pg.Error = "The enrollment profile couldn't be created: " + err.Error()
		p.render(w, "enroll.html", pg, http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="enroll.mobileconfig"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(prof)
}

func (p *portal) openProfile(w http.ResponseWriter, r *http.Request) {
	if p.MDM.SettingBool(mdm.SettingEnrollRequireToken) {
		http.Redirect(w, r, "/enroll", http.StatusSeeOther)
		return
	}
	p.serveProfile(w, r, "manual")
}

func (p *portal) usableToken(r *http.Request) (*store.EnrollmentToken, string) {
	t, err := p.Store.GetEnrollmentTokenByValue(r.PathValue("token"))
	if err != nil {
		return nil, "This enrollment link isn't valid. Ask your IT team for a new one."
	}
	if !t.Usable() {
		return nil, "This enrollment link has expired or has been used the maximum number of times. Ask your IT team for a new one."
	}
	return t, ""
}

func (p *portal) enrollToken(w http.ResponseWriter, r *http.Request) {
	pg := p.base(r, "Enroll your device")
	t, msg := p.usableToken(r)
	if t == nil {
		pg.Error = msg
		p.render(w, "enroll.html", pg, http.StatusNotFound)
		return
	}
	if err := p.enrollReady(); err != nil {
		pg.Error = "Enrollment isn't available yet. Your IT team still needs to finish setting up the server."
		p.render(w, "enroll.html", pg, http.StatusServiceUnavailable)
		return
	}
	pg.Ready, pg.Token, pg.TokenName = true, t.Token, t.Name
	pg.ProfileURL = "/enroll/" + t.Token + "/profile"
	pg.QRURL = "/enroll/" + t.Token + "/qr.png"
	pg.EnrollURL = p.MDM.PublicURL() + "/enroll/" + t.Token
	p.render(w, "enroll.html", pg, http.StatusOK)
}

func (p *portal) tokenProfile(w http.ResponseWriter, r *http.Request) {
	t, msg := p.usableToken(r)
	if t == nil {
		pg := p.base(r, "Enroll your device")
		pg.Error = msg
		p.render(w, "enroll.html", pg, http.StatusNotFound)
		return
	}
	p.serveProfile(w, r, fmt.Sprintf("token:%d", t.ID))
}

func (p *portal) enrollQR(w http.ResponseWriter, r *http.Request) {
	t, _ := p.usableToken(r)
	if t == nil {
		http.NotFound(w, r)
		return
	}
	code, err := qr.Encode(p.MDM.PublicURL()+"/enroll/"+t.Token, qr.M)
	if err != nil {
		http.Error(w, "qr", http.StatusInternalServerError)
		return
	}
	code.Scale = 8
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(code.PNG())
}

// ---- self-service portal ----

func (p *portal) portalDevice(r *http.Request) (*store.Device, error) {
	return p.Store.GetDeviceByPortalToken(r.PathValue("token"))
}

func (p *portal) availableApps(d *store.Device) []portalApp {
	assigns, err := p.Store.DeviceAssignments(d.UDID)
	if err != nil {
		return nil
	}
	installed, _ := p.Store.DeviceAppBundleIDs(d.UDID)
	states, _ := p.Store.ListAppStates(d.UDID, 0)
	stateOf := map[int64]string{}
	for _, st := range states {
		stateOf[st.AppID] = st.Status
	}
	excluded := map[int64]bool{}
	for _, a := range assigns {
		if a.ItemType == "app" && a.Intent == "exclude" {
			excluded[a.ItemID] = true
		}
	}
	seen := map[int64]bool{}
	var out []portalApp
	for _, a := range assigns {
		if a.ItemType != "app" || (a.Intent != "available" && a.Intent != "install") || seen[a.ItemID] || excluded[a.ItemID] {
			continue
		}
		seen[a.ItemID] = true
		app, err := p.Store.GetApp(a.ItemID)
		if err != nil {
			continue
		}
		pa := portalApp{ID: app.ID, Name: app.Name, Seller: app.Seller, Icon: app.IconURL, Required: a.Intent == "install"}
		switch {
		case installed[app.BundleID].BundleID != "" || stateOf[app.ID] == "installed":
			pa.Status = "installed"
		case stateOf[app.ID] == "pending" || stateOf[app.ID] == "installing":
			pa.Status = "installing"
		case stateOf[app.ID] == "failed":
			pa.Status = "failed"
		}
		out = append(out, pa)
	}
	return out
}

func (p *portal) portalPage(w http.ResponseWriter, r *http.Request) {
	pg := p.base(r, "Company Portal")
	if !p.MDM.SettingBool(mdm.SettingPortalEnabled) {
		pg.Error = "The Company Portal is turned off."
		p.render(w, "portal.html", pg, http.StatusNotFound)
		return
	}
	d, err := p.portalDevice(r)
	if err != nil {
		pg.Error = "This portal link isn't valid for any managed device."
		p.render(w, "portal.html", pg, http.StatusNotFound)
		return
	}
	pg.Device, pg.Token = d, r.PathValue("token")
	pg.Apps = p.availableApps(d)
	pg.Reasons = d.ComplianceReasons
	switch r.URL.Query().Get("done") {
	case "install":
		pg.Notice = "Installation requested. The app will appear on your Home Screen shortly — you may be asked to confirm."
	case "sync":
		pg.Notice = "Sync requested. Your device will check in within a minute."
	case "error":
		pg.Error = "That request couldn't be completed. Try again or contact IT."
	}
	p.render(w, "portal.html", pg, http.StatusOK)
}

func (p *portal) portalInstall(w http.ResponseWriter, r *http.Request) {
	d, err := p.portalDevice(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	allowed := false
	for _, a := range p.availableApps(d) {
		if a.ID == id {
			allowed = true
		}
	}
	back := "/portal/" + r.PathValue("token")
	if !allowed {
		http.Redirect(w, r, back+"?done=error", http.StatusSeeOther)
		return
	}
	app, err := p.Store.GetApp(id)
	if err != nil {
		http.Redirect(w, r, back+"?done=error", http.StatusSeeOther)
		return
	}
	cmd, err := p.MDM.InstallApplicationCommand(app, d)
	if err == nil {
		var c *store.Command
		c, err = p.MDM.EnqueueAndPush(d.UDID, cmd, mdm.Meta{Source: "portal", Ref: fmt.Sprintf("app:%d", app.ID), CreatedBy: "self-service"})
		if err == nil {
			_ = p.Store.SetAppState(&store.AppState{DeviceID: d.UDID, AppID: app.ID, BundleID: app.BundleID, Intent: "available", Status: "pending", CommandUUID: c.UUID, Attempts: 1})
			_ = p.Store.InsertEvent(&store.Event{DeviceID: d.UDID, Type: "portal.install", Message: "User requested " + app.Name + " from the Company Portal"})
		}
	}
	if err != nil {
		p.Log.Warn("portal install failed", "err", err)
		http.Redirect(w, r, back+"?done=error", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, back+"?done=install", http.StatusSeeOther)
}

func (p *portal) portalSync(w http.ResponseWriter, r *http.Request) {
	d, err := p.portalDevice(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	p.MDM.QueueInventory(d.UDID, "self-service sync")
	p.MDM.Push(d.UDID)
	http.Redirect(w, r, "/portal/"+r.PathValue("token")+"?done=sync", http.StatusSeeOther)
}

// ---- companion agent ----

// AgentReport is posted by an optional companion app on the device. Apple's
// MDM protocol does not expose the connected Wi-Fi network or continuous
// location, so an on-device app (configured through managed app
// configuration with {{device.agent_token}} and {{server.agent_url}}) can
// report them with the user's consent.
type AgentReport struct {
	Battery      *float64       `json:"battery"`
	BatteryState string         `json:"battery_state"`
	SSID         string         `json:"ssid"`
	BSSID        string         `json:"bssid"`
	LocalIP      string         `json:"local_ip"`
	Latitude     *float64       `json:"latitude"`
	Longitude    *float64       `json:"longitude"`
	Accuracy     float64        `json:"accuracy"`
	Altitude     float64        `json:"altitude"`
	Speed        *float64       `json:"speed"`
	Course       *float64       `json:"course"`
	Timestamp    any            `json:"timestamp"`
	Extra        map[string]any `json:"extra"`
}

func agentToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return r.Header.Get("X-Orchard-Agent-Token")
}

func parseTS(v any) int64 {
	switch t := v.(type) {
	case float64:
		if t > 1e12 {
			return int64(t / 1000)
		}
		return int64(t)
	case string:
		if ts, err := time.Parse(time.RFC3339, t); err == nil {
			return ts.Unix()
		}
	}
	return 0
}

func (p *portal) reportInterval() int {
	n, _ := strconv.Atoi(p.MDM.Setting("portal_agent_report_seconds"))
	if n <= 0 {
		n = 900
	}
	return n
}

func (p *portal) agentReport(w http.ResponseWriter, r *http.Request) {
	d, err := p.Store.GetDeviceByAgentToken(agentToken(r))
	if err != nil {
		http.Error(w, `{"error":"unknown device token"}`, http.StatusUnauthorized)
		return
	}
	var rep AgentReport
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&rep); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	ts := parseTS(rep.Timestamp)
	now := store.Now()
	if ts == 0 || ts > now+300 || ts < now-7*86400 {
		ts = now
	}
	sample := &store.TelemetrySample{DeviceID: d.UDID, TS: ts, Source: "agent", Battery: -1, AvailableGB: -1, BatteryState: rep.BatteryState,
		SSID: trim(rep.SSID, 64), BSSID: trim(rep.BSSID, 32), LocalIP: trim(rep.LocalIP, 64), IP: mdm.ClientIP(r, p.TrustProxy), Data: rep.Extra}
	fields := map[string]any{"agent_last_seen": now, "telemetry_at": now}
	if rep.Battery != nil && *rep.Battery >= 0 && *rep.Battery <= 1 {
		sample.Battery = *rep.Battery
		fields["battery_level"] = *rep.Battery
	}
	if rep.BatteryState != "" {
		fields["battery_state"] = trim(rep.BatteryState, 32)
	}
	fields["ssid"], fields["bssid"] = sample.SSID, sample.BSSID
	if sample.LocalIP != "" {
		fields["local_ip"] = sample.LocalIP
	}
	if sample.IP != "" {
		fields["last_ip"] = sample.IP
	}
	_ = p.Store.InsertTelemetry(sample)
	_ = p.Store.UpdateDevice(d.UDID, fields)
	if rep.Latitude != nil && rep.Longitude != nil && *rep.Latitude >= -90 && *rep.Latitude <= 90 && *rep.Longitude >= -180 && *rep.Longitude <= 180 {
		loc := &store.Location{DeviceID: d.UDID, TS: ts, Source: "agent", Latitude: *rep.Latitude, Longitude: *rep.Longitude, Accuracy: rep.Accuracy, Altitude: rep.Altitude, Speed: -1, Course: -1}
		if rep.Speed != nil {
			loc.Speed = *rep.Speed
		}
		if rep.Course != nil {
			loc.Course = *rep.Course
		}
		p.MDM.SaveLocation(loc)
	}
	p.MDM.Bus().Publish(events.DeviceTelemetry, d.UDID, map[string]any{"source": "agent", "ssid": sample.SSID, "battery": sample.Battery})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "next_report_seconds": p.reportInterval()})
}

func (p *portal) agentConfig(w http.ResponseWriter, r *http.Request) {
	d, err := p.Store.GetDeviceByAgentToken(agentToken(r))
	if err != nil {
		http.Error(w, `{"error":"unknown device token"}`, http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"report_url": p.MDM.PublicURL() + "/agent/v1/report", "report_interval_seconds": p.reportInterval(),
		"device": map[string]string{"name": d.DeviceName, "serial_number": d.SerialNumber}, "organization": p.MDM.Setting(mdm.SettingOrgName),
	})
}

func trim(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---- enterprise app files ----

func (p *portal) appForFile(r *http.Request) (*store.App, bool) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	app, err := p.Store.GetApp(id)
	if err != nil || app.Kind != "enterprise" || app.FileSecret == "" || app.FileSecret != r.PathValue("secret") {
		return nil, false
	}
	return app, true
}

func (p *portal) appManifest(w http.ResponseWriter, r *http.Request) {
	app, found := p.appForFile(r)
	if !found {
		http.NotFound(w, r)
		return
	}
	ipaURL := fmt.Sprintf("%s/files/apps/%d/%s/app.ipa", p.MDM.PublicURL(), app.ID, app.FileSecret)
	m, err := apps.Manifest(ipaURL, app.BundleID, app.Version, app.Name)
	if err != nil {
		http.Error(w, "manifest", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/xml")
	_, _ = w.Write(m)
}

func (p *portal) appIPA(w http.ResponseWriter, r *http.Request) {
	app, found := p.appForFile(r)
	if !found || app.IPAFile == "" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(app.IPAFile)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, _ := f.Stat()
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "app.ipa", fi.ModTime(), f)
}

func (p *portal) caPEM(w http.ResponseWriter, r *http.Request) {
	ca, _ := p.MDM.CA()
	w.Header().Set("Content-Type", "application/x-pem-file")
	_, _ = w.Write([]byte(pki.CertPEM(ca)))
}

// modelNames maps common model identifiers to marketing names.
var modelNames = map[string]string{
	"iPhone13,1": "iPhone 12 mini", "iPhone13,2": "iPhone 12", "iPhone13,3": "iPhone 12 Pro", "iPhone13,4": "iPhone 12 Pro Max",
	"iPhone14,4": "iPhone 13 mini", "iPhone14,5": "iPhone 13", "iPhone14,2": "iPhone 13 Pro", "iPhone14,3": "iPhone 13 Pro Max", "iPhone14,6": "iPhone SE (3rd generation)",
	"iPhone14,7": "iPhone 14", "iPhone14,8": "iPhone 14 Plus", "iPhone15,2": "iPhone 14 Pro", "iPhone15,3": "iPhone 14 Pro Max",
	"iPhone15,4": "iPhone 15", "iPhone15,5": "iPhone 15 Plus", "iPhone16,1": "iPhone 15 Pro", "iPhone16,2": "iPhone 15 Pro Max",
	"iPhone17,1": "iPhone 16 Pro", "iPhone17,2": "iPhone 16 Pro Max", "iPhone17,3": "iPhone 16", "iPhone17,4": "iPhone 16 Plus", "iPhone17,5": "iPhone 16e",
	"iPad13,18": "iPad (10th generation)", "iPad13,19": "iPad (10th generation)", "iPad14,1": "iPad mini (6th generation)", "iPad14,2": "iPad mini (6th generation)",
	"iPad13,16": "iPad Air (5th generation)", "iPad13,17": "iPad Air (5th generation)", "iPad14,8": "iPad Air 11-inch (M2)", "iPad14,9": "iPad Air 11-inch (M2)",
	"iPad14,10": "iPad Air 13-inch (M2)", "iPad14,11": "iPad Air 13-inch (M2)", "iPad16,1": "iPad mini (A17 Pro)", "iPad16,2": "iPad mini (A17 Pro)",
	"iPad16,3": "iPad Pro 11-inch (M4)", "iPad16,4": "iPad Pro 11-inch (M4)", "iPad16,5": "iPad Pro 13-inch (M4)", "iPad16,6": "iPad Pro 13-inch (M4)",
}

// ModelName returns the marketing name of a model identifier (or the identifier itself).
func ModelName(id string) string {
	if n, ok := modelNames[id]; ok {
		return n
	}
	return id
}
