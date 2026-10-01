package api

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/ddm"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"howett.net/plist"
)

// DeviceSummary is the compact device representation used in lists.
type DeviceSummary struct {
	UDID              string         `json:"udid"`
	SerialNumber      string         `json:"serial_number"`
	DeviceName        string         `json:"device_name"`
	ProductName       string         `json:"product_name"`
	ModelName         string         `json:"model_name"`
	OSVersion         string         `json:"os_version"`
	EnrollmentStatus  string         `json:"enrollment_status"`
	EnrollmentType    string         `json:"enrollment_type"`
	Ownership         string         `json:"ownership"`
	Supervised        bool           `json:"supervised"`
	Compliance        string         `json:"compliance"`
	BatteryLevel      float64        `json:"battery_level"`
	CapacityGB        float64        `json:"capacity_gb"`
	AvailableGB       float64        `json:"available_gb"`
	LastSeen          int64          `json:"last_seen"`
	EnrolledAt        int64          `json:"enrolled_at"`
	AssignedUser      string         `json:"assigned_user"`
	Tags              []string       `json:"tags"`
	LostMode          bool           `json:"lost_mode"`
	Carrier           string         `json:"carrier"`
	SSID              string         `json:"ssid"`
	LastIP            string         `json:"last_ip"`
	PasscodePresent   bool           `json:"passcode_present"`
	HasPushToken      bool           `json:"has_push_token"`
	Location          map[string]any `json:"location,omitempty"`
	PendingCommands   int            `json:"pending_commands"`
	ComplianceReasons []string       `json:"compliance_reasons"`
}

func summarize(d *store.Device) DeviceSummary {
	return DeviceSummary{
		UDID: d.UDID, SerialNumber: d.SerialNumber, DeviceName: d.DeviceName, ProductName: d.ProductName, ModelName: d.ModelName, OSVersion: d.OSVersion,
		EnrollmentStatus: d.EnrollmentStatus, EnrollmentType: d.EnrollmentType, Ownership: d.Ownership, Supervised: d.Supervised, Compliance: d.Compliance,
		BatteryLevel: d.BatteryLevel, CapacityGB: d.CapacityGB, AvailableGB: d.AvailableGB, LastSeen: d.LastSeen, EnrolledAt: d.EnrolledAt,
		AssignedUser: d.AssignedUser, Tags: d.Tags, LostMode: d.LostMode, Carrier: d.Carrier, SSID: d.SSID, LastIP: d.LastIP,
		PasscodePresent: d.PasscodePresent, HasPushToken: d.HasPushToken, Location: d.Location, ComplianceReasons: d.ComplianceReasons,
	}
}

func filterFromQuery(r *http.Request) store.DeviceFilter {
	q := r.URL.Query()
	f := store.DeviceFilter{
		Query: q.Get("q"), Status: q.Get("status"), Compliance: q.Get("compliance"), Model: q.Get("model"), OSVersion: q.Get("os"),
		Ownership: q.Get("ownership"), Supervised: q.Get("supervised"), Sort: q.Get("sort"), Desc: q.Get("dir") == "desc",
		Limit: queryInt(r, "limit", 50), Offset: queryInt(r, "offset", 0),
	}
	if g := queryInt(r, "group", 0); g > 0 {
		f.GroupID = int64(g)
	}
	if f.Limit > 1000 {
		f.Limit = 1000
	}
	return f
}

func (a *API) listDevices(w http.ResponseWriter, r *http.Request, p *Principal) error {
	devs, total, err := a.Store.ListDevices(filterFromQuery(r))
	if err != nil {
		return err
	}
	items := make([]DeviceSummary, 0, len(devs))
	for _, d := range devs {
		s := summarize(d)
		s.PendingCommands = a.Store.PendingCommandCount(d.UDID)
		items = append(items, s)
	}
	return ok(w, map[string]any{"items": items, "total": total})
}

func (a *API) deviceFacets(w http.ResponseWriter, r *http.Request, p *Principal) error {
	models, _ := a.Store.DistinctValues("product_name")
	oses, _ := a.Store.DistinctValues("os_version")
	return ok(w, map[string]any{"models": models, "os_versions": oses})
}

func (a *API) exportDevices(w http.ResponseWriter, r *http.Request, p *Principal) error {
	f := filterFromQuery(r)
	f.Limit, f.Offset = 0, 0
	devs, _, err := a.Store.ListDevices(f)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="orchard-devices-`+time.Now().Format("20060102")+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"udid", "serial_number", "device_name", "product_name", "os_version", "build", "status", "enrollment_type", "ownership", "supervised",
		"compliance", "battery_percent", "capacity_gb", "available_gb", "imei", "phone_number", "carrier", "wifi_mac", "last_ip", "ssid",
		"assigned_user", "assigned_email", "asset_tag", "tags", "lost_mode", "latitude", "longitude", "enrolled_at", "last_seen"})
	ts := func(v int64) string {
		if v == 0 {
			return ""
		}
		return time.Unix(v, 0).UTC().Format(time.RFC3339)
	}
	for _, d := range devs {
		lat, lon := "", ""
		if d.Location != nil {
			lat, lon = fmt.Sprint(d.Location["latitude"]), fmt.Sprint(d.Location["longitude"])
		}
		battery := ""
		if d.BatteryLevel >= 0 {
			battery = fmt.Sprintf("%.0f", d.BatteryLevel*100)
		}
		_ = cw.Write([]string{d.UDID, d.SerialNumber, d.DeviceName, d.ProductName, d.OSVersion, d.BuildVersion, d.EnrollmentStatus, d.EnrollmentType, d.Ownership,
			fmt.Sprint(d.Supervised), d.Compliance, battery, fmt.Sprintf("%.1f", d.CapacityGB), fmt.Sprintf("%.1f", d.AvailableGB), d.IMEI, d.PhoneNumber, d.Carrier,
			d.WiFiMAC, d.LastIP, d.SSID, d.AssignedUser, d.AssignedEmail, d.AssetTag, strings.Join(d.Tags, ";"), fmt.Sprint(d.LostMode), lat, lon, ts(d.EnrolledAt), ts(d.LastSeen)})
	}
	cw.Flush()
	return nil
}

func (a *API) device(r *http.Request) (*store.Device, error) {
	return a.Store.GetDevice(r.PathValue("udid"))
}

func (a *API) getDevice(w http.ResponseWriter, r *http.Request, p *Principal) error {
	d, err := a.device(r)
	if err != nil {
		return err
	}
	groups, _ := a.Store.DeviceGroups(d.UDID)
	out := map[string]any{
		"device":           d,
		"groups":           groups,
		"pending_commands": a.Store.PendingCommandCount(d.UDID),
		"has_unlock_token": len(d.UnlockToken) > 0,
		"has_bypass_code":  d.ActivationLockBypass != "",
		"ddm_supported":    ddm.Supported(d),
		"identity_expires": d.CertNotAfter,
	}
	if d.SerialNumber != "" {
		if dd, err := a.Store.GetDEPDevice(d.SerialNumber); err == nil {
			out["ade"] = dd
		}
	}
	if d.EnrollmentTokenID > 0 {
		if t, err := a.Store.GetEnrollmentToken(d.EnrollmentTokenID); err == nil {
			out["enrollment_token"] = map[string]any{"id": t.ID, "name": t.Name}
		}
	}
	return ok(w, out)
}

func (a *API) deviceSecrets(w http.ResponseWriter, r *http.Request, p *Principal) error {
	d, err := a.device(r)
	if err != nil {
		return err
	}
	a.audit(r, p, "device.secrets_viewed", d.UDID, nil)
	return ok(w, map[string]any{"activation_lock_bypass": d.ActivationLockBypass, "has_unlock_token": len(d.UnlockToken) > 0,
		"agent_token": d.AgentToken, "portal_url": a.MDM.PublicURL() + "/portal/" + d.PortalToken})
}

func (a *API) patchDevice(w http.ResponseWriter, r *http.Request, p *Principal) error {
	d, err := a.device(r)
	if err != nil {
		return err
	}
	var req struct {
		Tags          *[]string `json:"tags"`
		Notes         *string   `json:"notes"`
		Ownership     *string   `json:"ownership"`
		AssignedUser  *string   `json:"assigned_user"`
		AssignedEmail *string   `json:"assigned_email"`
		AssetTag      *string   `json:"asset_tag"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	fields := map[string]any{}
	if req.Tags != nil {
		fields["tags"] = store.JoinTags(*req.Tags)
	}
	if req.Notes != nil {
		fields["notes"] = *req.Notes
	}
	if req.Ownership != nil {
		switch *req.Ownership {
		case "corporate", "personal", "unknown":
			fields["ownership"] = *req.Ownership
		default:
			return badRequest("ownership must be corporate, personal or unknown")
		}
	}
	if req.AssignedUser != nil {
		fields["assigned_user"] = strings.TrimSpace(*req.AssignedUser)
	}
	if req.AssignedEmail != nil {
		fields["assigned_email"] = strings.TrimSpace(*req.AssignedEmail)
	}
	if req.AssetTag != nil {
		fields["asset_tag"] = strings.TrimSpace(*req.AssetTag)
	}
	if err := a.Store.UpdateDevice(d.UDID, fields); err != nil {
		return err
	}
	a.audit(r, p, "device.updated", d.UDID, fields)
	a.Reconciler.TriggerDevice(d.UDID)
	nd, _ := a.Store.GetDevice(d.UDID)
	return ok(w, nd)
}

func (a *API) deleteDevice(w http.ResponseWriter, r *http.Request, p *Principal) error {
	d, err := a.device(r)
	if err != nil {
		return err
	}
	if d.EnrollmentStatus == "enrolled" && r.URL.Query().Get("force") != "1" {
		return badRequest("the device is still enrolled; unenroll it first (or pass force=1 to forget the record — the device would stay enrolled but unmanaged)")
	}
	if err := a.Store.DeleteDevice(d.UDID); err != nil {
		return err
	}
	a.audit(r, p, "device.deleted", d.UDID, map[string]string{"serial": d.SerialNumber, "name": d.DeviceName})
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) deviceApps(w http.ResponseWriter, r *http.Request, p *Principal) error {
	apps, err := a.Store.ListDeviceApps(r.PathValue("udid"))
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": apps})
}

func (a *API) deviceProfiles(w http.ResponseWriter, r *http.Request, p *Principal) error {
	udid := r.PathValue("udid")
	installed, err := a.Store.ListDeviceProfiles(udid)
	if err != nil {
		return err
	}
	states, _ := a.Store.ListProfileStates(udid, 0)
	return ok(w, map[string]any{"items": installed, "managed": states, "mdm_identifier": a.MDM.MDMIdentifier()})
}

func (a *API) deviceCertificates(w http.ResponseWriter, r *http.Request, p *Principal) error {
	certs, err := a.Store.ListDeviceCertificates(r.PathValue("udid"))
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": certs})
}

func (a *API) deviceCommands(w http.ResponseWriter, r *http.Request, p *Principal) error {
	cmds, total, err := a.Store.ListCommands(store.CommandFilter{DeviceID: r.PathValue("udid"), Status: r.URL.Query().Get("status"),
		Limit: queryInt(r, "limit", 100), Offset: queryInt(r, "offset", 0)})
	if err != nil {
		return err
	}
	if cmds == nil {
		cmds = []*store.Command{}
	}
	return ok(w, map[string]any{"items": cmds, "total": total})
}

func (a *API) deviceEvents(w http.ResponseWriter, r *http.Request, p *Principal) error {
	evs, err := a.Store.ListEvents(r.PathValue("udid"), r.URL.Query().Get("type"), queryInt(r, "limit", 100), queryInt(r, "offset", 0))
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": evs})
}

func (a *API) deviceTelemetry(w http.ResponseWriter, r *http.Request, p *Principal) error {
	hours := queryInt(r, "hours", 168)
	if hours <= 0 || hours > 24*400 {
		hours = 168
	}
	samples, err := a.Store.ListTelemetry(r.PathValue("udid"), store.Now()-int64(hours)*3600, queryInt(r, "limit", 2000))
	if err != nil {
		return err
	}
	// distinct networks seen
	type netSeen struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
		First int64  `json:"first_seen"`
		Last  int64  `json:"last_seen"`
		Count int    `json:"count"`
	}
	nets := map[string]*netSeen{}
	add := func(kind, value string, ts int64) {
		if value == "" {
			return
		}
		k := kind + "|" + value
		n := nets[k]
		if n == nil {
			n = &netSeen{Kind: kind, Value: value, First: ts}
			nets[k] = n
		}
		n.Last, n.Count = ts, n.Count+1
	}
	for _, s := range samples {
		add("ip", s.IP, s.TS)
		add("ssid", s.SSID, s.TS)
		add("carrier", s.Carrier, s.TS)
	}
	list := make([]*netSeen, 0, len(nets))
	for _, n := range nets {
		list = append(list, n)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Last > list[j].Last })
	return ok(w, map[string]any{"items": samples, "networks": list, "hours": hours})
}

func (a *API) deviceLocations(w http.ResponseWriter, r *http.Request, p *Principal) error {
	days := queryInt(r, "days", 30)
	locs, err := a.Store.ListLocations(r.PathValue("udid"), store.Now()-int64(days)*86400, queryInt(r, "limit", 500))
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": locs})
}

func (a *API) deviceDeclarations(w http.ResponseWriter, r *http.Request, p *Principal) error {
	udid := r.PathValue("udid")
	items, err := a.DDM.DeviceItems(udid)
	if err != nil {
		return err
	}
	statuses, _ := a.Store.ListDeclarationStatus(udid)
	d, _ := a.Store.GetDevice(udid)
	out := map[string]any{"items": items, "status": statuses, "token": ddm.DeclarationsToken(items)}
	if d != nil {
		out["device_token"] = d.DDMToken
		out["status_items"] = d.DDMStatus
		out["last_status"] = d.DDMLastStatus
		out["supported"] = ddm.Supported(d)
	}
	return ok(w, out)
}

func (a *API) deviceAssignments(w http.ResponseWriter, r *http.Request, p *Principal) error {
	udid := r.PathValue("udid")
	assigns, err := a.Store.DeviceAssignments(udid)
	if err != nil {
		return err
	}
	pStates, _ := a.Store.ListProfileStates(udid, 0)
	aStates, _ := a.Store.ListAppStates(udid, 0)
	type item struct {
		ItemType string `json:"item_type"`
		ItemID   int64  `json:"item_id"`
		Name     string `json:"name"`
		Intent   string `json:"intent"`
		Group    string `json:"group"`
		Status   string `json:"status"`
		Error    string `json:"error,omitempty"`
		Updated  int64  `json:"updated_at,omitempty"`
	}
	var out []item
	for _, as := range assigns {
		it := item{ItemType: as.ItemType, ItemID: as.ItemID, Intent: as.Intent, Group: as.GroupName}
		switch as.ItemType {
		case "profile":
			if pr, err := a.Store.GetProfile(as.ItemID); err == nil {
				it.Name = pr.Name
			}
			for _, st := range pStates {
				if st.ProfileID == as.ItemID {
					it.Status, it.Error, it.Updated = st.Status, st.Error, st.UpdatedAt
				}
			}
		case "app":
			if ap, err := a.Store.GetApp(as.ItemID); err == nil {
				it.Name = ap.Name
			}
			for _, st := range aStates {
				if st.AppID == as.ItemID {
					it.Status, it.Error, it.Updated = st.Status, st.Error, st.UpdatedAt
				}
			}
		case "declaration":
			if dc, err := a.Store.GetDeclaration(as.ItemID); err == nil {
				it.Name = dc.Name
			}
		case "compliance":
			if cp, err := a.Store.GetCompliancePolicy(as.ItemID); err == nil {
				it.Name = cp.Name
			}
		}
		if it.Status == "" && as.Intent != "exclude" {
			it.Status = "—"
		}
		out = append(out, it)
	}
	if out == nil {
		out = []item{}
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) deviceCompliance(w http.ResponseWriter, r *http.Request, p *Principal) error {
	d, err := a.device(r)
	if err != nil {
		return err
	}
	return ok(w, a.Compliance.Evaluate(d))
}

func (a *API) deviceSendCommand(w http.ResponseWriter, r *http.Request, p *Principal) error {
	d, err := a.device(r)
	if err != nil {
		return err
	}
	var req struct {
		Command string     `json:"command"`
		Params  mdm.Params `json:"params"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if mdm.IsDenied(req.Command) {
		return badRequest("%v", mdm.ErrForbiddenCommand)
	}
	source := "manual"
	if p.Kind == "apikey" {
		source = "api"
	}
	res := a.MDM.SendCatalogCommand(req.Command, []string{d.UDID}, req.Params, mdm.Meta{Source: source, CreatedBy: p.Actor()})
	if len(res) == 1 && res[0].Error != "" {
		return badRequest("%s", res[0].Error)
	}
	a.audit(r, p, "device.command", d.UDID, map[string]any{"command": req.Command})
	return ok(w, res[0])
}

// runAction executes a named device action.
func (a *API) runAction(ctx context.Context, udid, action string, p *Principal) (any, error) {
	meta := mdm.Meta{Source: "manual", CreatedBy: p.Actor()}
	d, err := a.Store.GetDevice(udid)
	if err != nil {
		return nil, err
	}
	switch action {
	case "sync":
		n := a.MDM.QueueInventory(udid, "manual sync by "+p.Actor())
		a.MDM.Push(udid)
		return map[string]int{"queued": n}, nil
	case "telemetry":
		queued := a.MDM.QueueTelemetry(udid)
		a.MDM.Push(udid)
		return map[string]bool{"queued": queued}, nil
	case "push":
		res := a.MDM.PushNow(ctx, []string{udid})
		if len(res) == 0 {
			return nil, badRequest("no push was sent (APNs not configured, or device has no push token)")
		}
		if res[0].Err != nil {
			return nil, badRequest("push failed: %v", res[0].Err)
		}
		return map[string]any{"status": res[0].Status, "apns_id": res[0].ID}, nil
	case "unenroll":
		c, err := a.MDM.Unenroll(udid, meta)
		if err != nil {
			return nil, err
		}
		return map[string]string{"command_uuid": c.UUID}, nil
	case "renew-identity":
		c, err := a.MDM.RenewIdentity(udid, meta)
		if err != nil {
			return nil, err
		}
		return map[string]string{"command_uuid": c.UUID}, nil
	case "reconcile":
		return map[string]int{"queued": a.Reconciler.ReconcileDevice(udid)}, nil
	case "retry-failed":
		n := 0
		if states, err := a.Store.ListProfileStates(udid, 0); err == nil {
			for _, st := range states {
				if st.Status == "failed" || st.Status == "remove_failed" {
					_ = a.Store.DeleteProfileState(udid, st.ProfileID)
					n++
				}
			}
		}
		if states, err := a.Store.ListAppStates(udid, 0); err == nil {
			for _, st := range states {
				if st.Status == "failed" {
					_ = a.Store.DeleteAppState(udid, st.AppID)
					n++
				}
			}
		}
		_ = a.Store.UpdateDevice(udid, map[string]any{"ddm_token": ""})
		return map[string]int{"reset": n, "queued": a.Reconciler.ReconcileDevice(udid)}, nil
	case "clear-queue":
		n, err := a.Store.CancelDeviceCommands(udid)
		return map[string]int64{"canceled": n}, err
	case "locate":
		if !d.LostMode {
			return nil, badRequest("location is only available while the device is in Lost Mode (supervised devices)")
		}
		c, err := a.MDM.EnqueueAndPush(udid, map[string]any{"RequestType": "DeviceLocation"}, meta)
		if err != nil {
			return nil, err
		}
		return map[string]string{"command_uuid": c.UUID}, nil
	case "evaluate-compliance":
		return a.Compliance.EvaluateDevice(udid), nil
	case "erase", "wipe":
		return nil, badRequest("%v", mdm.ErrForbiddenCommand)
	}
	return nil, errf(http.StatusNotFound, "unknown action %q", action)
}

func (a *API) deviceAction(w http.ResponseWriter, r *http.Request, p *Principal) error {
	udid, action := r.PathValue("udid"), r.PathValue("action")
	if (action == "unenroll" || action == "renew-identity") && Roles[p.Role] < PermManage {
		return errf(http.StatusForbidden, "this action requires the operator role")
	}
	out, err := a.runAction(r.Context(), udid, action, p)
	if err != nil {
		return err
	}
	a.audit(r, p, "device.action."+action, udid, nil)
	return ok(w, out)
}

func (a *API) bulkDevices(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		UDIDs   []string   `json:"udids"`
		Command string     `json:"command"`
		Params  mdm.Params `json:"params"`
		Action  string     `json:"action"`
		GroupID int64      `json:"group_id"`
		Tags    []string   `json:"tags"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if len(req.UDIDs) == 0 {
		return badRequest("select at least one device")
	}
	if len(req.UDIDs) > 5000 {
		return badRequest("too many devices in one request")
	}
	switch {
	case req.Command != "":
		if mdm.IsDenied(req.Command) {
			return badRequest("%v", mdm.ErrForbiddenCommand)
		}
		res := a.MDM.SendCatalogCommand(req.Command, req.UDIDs, req.Params, mdm.Meta{Source: "manual", CreatedBy: p.Actor()})
		a.audit(r, p, "device.bulk_command", fmt.Sprintf("%d devices", len(req.UDIDs)), map[string]any{"command": req.Command})
		return ok(w, map[string]any{"results": res})
	case req.Action == "add_to_group" || req.Action == "remove_from_group":
		if Roles[p.Role] < PermManage {
			return errf(http.StatusForbidden, "changing groups requires the operator role")
		}
		g, err := a.Store.GetGroup(req.GroupID)
		if err != nil {
			return err
		}
		if g.Kind != "static" {
			return badRequest("membership of %s groups is computed automatically", g.Kind)
		}
		if req.Action == "add_to_group" {
			err = a.Store.AddGroupMembers(g.ID, req.UDIDs)
		} else {
			err = a.Store.RemoveGroupMembers(g.ID, req.UDIDs)
		}
		if err != nil {
			return err
		}
		a.Bus.Publish(events.GroupsChanged, "", map[string]any{"group_id": g.ID})
		a.audit(r, p, "group."+req.Action, g.Name, map[string]int{"devices": len(req.UDIDs)})
		return ok(w, map[string]bool{"ok": true})
	case req.Action == "add_tags":
		for _, u := range req.UDIDs {
			if d, err := a.Store.GetDevice(u); err == nil {
				_ = a.Store.UpdateDevice(u, map[string]any{"tags": store.JoinTags(append(d.Tags, req.Tags...))})
			}
		}
		a.audit(r, p, "device.bulk_tags", fmt.Sprintf("%d devices", len(req.UDIDs)), req.Tags)
		return ok(w, map[string]bool{"ok": true})
	case req.Action != "":
		if (req.Action == "unenroll" || req.Action == "renew-identity") && Roles[p.Role] < PermManage {
			return errf(http.StatusForbidden, "this action requires the operator role")
		}
		var results []map[string]any
		for _, u := range req.UDIDs {
			out, err := a.runAction(r.Context(), u, req.Action, p)
			res := map[string]any{"udid": u, "result": out}
			if err != nil {
				res["error"] = err.Error()
			}
			results = append(results, res)
		}
		a.audit(r, p, "device.bulk_action."+req.Action, fmt.Sprintf("%d devices", len(req.UDIDs)), nil)
		return ok(w, map[string]any{"results": results})
	}
	return badRequest("specify a command or an action")
}

// ---- commands ----

func (a *API) commandCatalog(w http.ResponseWriter, r *http.Request, p *Principal) error {
	return ok(w, map[string]any{"items": mdm.Catalog, "categories": mdm.CatalogCategories()})
}

func (a *API) listCommands(w http.ResponseWriter, r *http.Request, p *Principal) error {
	cmds, total, err := a.Store.ListCommands(store.CommandFilter{DeviceID: r.URL.Query().Get("device"), Status: r.URL.Query().Get("status"),
		RequestType: r.URL.Query().Get("type"), Limit: queryInt(r, "limit", 100), Offset: queryInt(r, "offset", 0)})
	if err != nil {
		return err
	}
	if cmds == nil {
		cmds = []*store.Command{}
	}
	stats, _ := a.Store.CommandStats(7 * 86400)
	return ok(w, map[string]any{"items": cmds, "total": total, "stats": stats})
}

// redactCommand makes a decoded command/result JSON friendly and hides secrets.
func redactCommand(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, vv := range t {
			switch k {
			case "UnlockToken", "Password", "ProxyPassword", "SharedSecret", "PIN":
				out[k] = "••••••"
			default:
				out[k] = redactCommand(vv)
			}
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			out[i] = redactCommand(vv)
		}
		return out
	case []byte:
		return fmt.Sprintf("<%d bytes>", len(t))
	case time.Time:
		return t.UTC().Format(time.RFC3339)
	}
	return v
}

func (a *API) getCommand(w http.ResponseWriter, r *http.Request, p *Principal) error {
	c, err := a.Store.GetCommand(r.PathValue("uuid"))
	if err != nil {
		return err
	}
	out := map[string]any{"command": c}
	var payload map[string]any
	if _, err := plist.Unmarshal(c.Payload, &payload); err == nil {
		out["payload"] = redactCommand(payload["Command"])
	}
	if len(c.Result) > 0 {
		var res map[string]any
		if _, err := plist.Unmarshal(c.Result, &res); err == nil {
			out["result"] = redactCommand(res)
		}
	}
	return ok(w, out)
}

func (a *API) cancelCommand(w http.ResponseWriter, r *http.Request, p *Principal) error {
	uuid := r.PathValue("uuid")
	done, err := a.Store.CancelCommand(uuid)
	if err != nil {
		return err
	}
	if !done {
		return badRequest("command is not pending")
	}
	a.audit(r, p, "command.canceled", uuid, nil)
	return ok(w, map[string]bool{"ok": true})
}

var errNotStatic = errors.New("not a static group")
