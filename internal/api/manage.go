package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yeungalan/orchard-mdm/internal/apps"
	"github.com/yeungalan/orchard-mdm/internal/compliance"
	"github.com/yeungalan/orchard-mdm/internal/ddm"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/profiles"
	"github.com/yeungalan/orchard-mdm/internal/reconcile"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

// ---- groups ----

func (a *API) listGroups(w http.ResponseWriter, r *http.Request, p *Principal) error {
	gs, err := a.Store.ListGroups()
	if err != nil {
		return err
	}
	type withCounts struct {
		*store.Group
		Assignments int `json:"assignments"`
	}
	out := make([]withCounts, 0, len(gs))
	for _, g := range gs {
		as, _ := a.Store.ListAssignments("", 0, g.ID)
		out = append(out, withCounts{g, len(as)})
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getGroup(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	g, err := a.Store.GetGroup(id)
	if err != nil {
		return err
	}
	as, _ := a.Store.ListAssignments("", 0, id)
	return ok(w, map[string]any{"group": g, "assignments": a.describeAssignments(as)})
}

type groupReq struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Kind        string            `json:"kind"`
	Rules       *store.GroupRules `json:"rules"`
	Members     []string          `json:"members"`
}

func validateGroup(req *groupReq) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return badRequest("name is required")
	}
	switch req.Kind {
	case "", "static":
		req.Kind, req.Rules = "static", nil
	case "dynamic":
		if req.Rules == nil || len(req.Rules.Rules) == 0 {
			return badRequest("dynamic groups need at least one rule")
		}
		if req.Rules.Match != "any" {
			req.Rules.Match = "all"
		}
		for _, rule := range req.Rules.Rules {
			if rule.Field == "" || rule.Op == "" {
				return badRequest("every rule needs a field and an operator")
			}
		}
	default:
		return badRequest("kind must be static or dynamic")
	}
	return nil
}

func (a *API) createGroup(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req groupReq
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := validateGroup(&req); err != nil {
		return err
	}
	g := &store.Group{Name: req.Name, Description: req.Description, Kind: req.Kind, Rules: req.Rules}
	if err := a.Store.CreateGroup(g); err != nil {
		return err
	}
	if g.Kind == "static" && len(req.Members) > 0 {
		_ = a.Store.AddGroupMembers(g.ID, req.Members)
	}
	if g.Kind == "dynamic" {
		a.Reconciler.RefreshGroups()
	}
	a.Bus.Publish(events.GroupsChanged, "", map[string]any{"group_id": g.ID})
	a.audit(r, p, "group.created", g.Name, map[string]string{"kind": g.Kind})
	g, _ = a.Store.GetGroup(g.ID)
	return ok(w, g)
}

func (a *API) updateGroup(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	g, err := a.Store.GetGroup(id)
	if err != nil {
		return err
	}
	var req groupReq
	if err := decode(r, &req); err != nil {
		return err
	}
	if g.Kind == "all" {
		g.Description = req.Description
	} else {
		if err := validateGroup(&req); err != nil {
			return err
		}
		if g.Kind != req.Kind {
			_, _ = a.Store.SetGroupMembers(g.ID, nil)
		}
		g.Name, g.Description, g.Kind, g.Rules = req.Name, req.Description, req.Kind, req.Rules
	}
	if err := a.Store.UpdateGroup(g); err != nil {
		return err
	}
	a.Reconciler.RefreshGroups()
	a.Bus.Publish(events.GroupsChanged, "", map[string]any{"group_id": g.ID})
	a.audit(r, p, "group.updated", g.Name, nil)
	g, _ = a.Store.GetGroup(id)
	return ok(w, g)
}

func (a *API) deleteGroup(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	g, err := a.Store.GetGroup(id)
	if err != nil {
		return err
	}
	if g.Kind == "all" {
		return badRequest("the built-in All Devices group cannot be deleted")
	}
	if err := a.Store.DeleteGroup(id); err != nil {
		return err
	}
	a.Bus.Publish(events.AssignmentsChanged, "", nil)
	a.audit(r, p, "group.deleted", g.Name, nil)
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) groupMembers(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	devs, total, err := a.Store.ListDevices(store.DeviceFilter{GroupID: id, Query: r.URL.Query().Get("q"), Limit: queryInt(r, "limit", 500), Offset: queryInt(r, "offset", 0), Sort: "name"})
	if err != nil {
		return err
	}
	items := make([]DeviceSummary, 0, len(devs))
	for _, d := range devs {
		items = append(items, summarize(d))
	}
	return ok(w, map[string]any{"items": items, "total": total})
}

func (a *API) changeMembers(w http.ResponseWriter, r *http.Request, p *Principal, add bool) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	g, err := a.Store.GetGroup(id)
	if err != nil {
		return err
	}
	if g.Kind != "static" {
		return badRequest("membership of %s groups is computed automatically", g.Kind)
	}
	var req struct {
		UDIDs []string `json:"udids"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if add {
		err = a.Store.AddGroupMembers(id, req.UDIDs)
	} else {
		err = a.Store.RemoveGroupMembers(id, req.UDIDs)
	}
	if err != nil {
		return err
	}
	a.Bus.Publish(events.GroupsChanged, "", map[string]any{"group_id": id})
	action := "group.members_removed"
	if add {
		action = "group.members_added"
	}
	a.audit(r, p, action, g.Name, map[string]int{"devices": len(req.UDIDs)})
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) addGroupMembers(w http.ResponseWriter, r *http.Request, p *Principal) error {
	return a.changeMembers(w, r, p, true)
}

func (a *API) removeGroupMembers(w http.ResponseWriter, r *http.Request, p *Principal) error {
	return a.changeMembers(w, r, p, false)
}

func (a *API) previewGroup(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Rules *store.GroupRules `json:"rules"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	devs, err := a.Store.EnrolledDevices()
	if err != nil {
		return err
	}
	var items []DeviceSummary
	for _, d := range devs {
		apps, _ := a.Store.DeviceAppBundleIDs(d.UDID)
		if reconcile.MatchRules(d, req.Rules, apps) {
			items = append(items, summarize(d))
		}
	}
	if items == nil {
		items = []DeviceSummary{}
	}
	return ok(w, map[string]any{"items": items, "total": len(items)})
}

func (a *API) groupRuleFields(w http.ResponseWriter, r *http.Request, p *Principal) error {
	return ok(w, map[string]any{"fields": reconcile.RuleFields, "ops": reconcile.RuleOps})
}

// ---- assignments ----

type assignmentView struct {
	*store.Assignment
	ItemName string `json:"item_name"`
}

func (a *API) itemName(itemType string, id int64) string {
	switch itemType {
	case "profile":
		if x, err := a.Store.GetProfile(id); err == nil {
			return x.Name
		}
	case "app":
		if x, err := a.Store.GetApp(id); err == nil {
			return x.Name
		}
	case "declaration":
		if x, err := a.Store.GetDeclaration(id); err == nil {
			return x.Name
		}
	case "compliance":
		if x, err := a.Store.GetCompliancePolicy(id); err == nil {
			return x.Name
		}
	}
	return fmt.Sprintf("%s #%d", itemType, id)
}

func (a *API) describeAssignments(as []*store.Assignment) []assignmentView {
	out := make([]assignmentView, 0, len(as))
	for _, x := range as {
		out = append(out, assignmentView{x, a.itemName(x.ItemType, x.ItemID)})
	}
	return out
}

func (a *API) listAssignments(w http.ResponseWriter, r *http.Request, p *Principal) error {
	itemID, _ := strconv.ParseInt(r.URL.Query().Get("item_id"), 10, 64)
	groupID, _ := strconv.ParseInt(r.URL.Query().Get("group_id"), 10, 64)
	as, err := a.Store.ListAssignments(r.URL.Query().Get("item_type"), itemID, groupID)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": a.describeAssignments(as)})
}

var validIntents = map[string][]string{
	"profile":     {"install", "exclude"},
	"app":         {"install", "available", "uninstall", "exclude"},
	"declaration": {"install", "exclude"},
	"compliance":  {"install", "exclude"},
}

func (a *API) createAssignment(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req store.Assignment
	if err := decode(r, &req); err != nil {
		return err
	}
	intents, okType := validIntents[req.ItemType]
	if !okType {
		return badRequest("item_type must be profile, app, declaration or compliance")
	}
	if req.Intent == "" {
		req.Intent = "install"
	}
	valid := false
	for _, i := range intents {
		valid = valid || i == req.Intent
	}
	if !valid {
		return badRequest("intent %q is not valid for %s", req.Intent, req.ItemType)
	}
	if _, err := a.Store.GetGroup(req.GroupID); err != nil {
		return badRequest("group not found")
	}
	if name := a.itemName(req.ItemType, req.ItemID); strings.HasPrefix(name, req.ItemType+" #") {
		return badRequest("%s %d not found", req.ItemType, req.ItemID)
	}
	if err := a.Store.UpsertAssignment(&req); err != nil {
		return err
	}
	a.Bus.Publish(events.AssignmentsChanged, "", map[string]any{"item_type": req.ItemType, "item_id": req.ItemID})
	a.audit(r, p, "assignment.created", fmt.Sprintf("%s:%d", req.ItemType, req.ItemID), map[string]any{"group_id": req.GroupID, "intent": req.Intent})
	return ok(w, req)
}

func (a *API) deleteAssignment(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	as, err := a.Store.DeleteAssignment(id)
	if err != nil {
		return err
	}
	a.Bus.Publish(events.AssignmentsChanged, "", map[string]any{"item_type": as.ItemType, "item_id": as.ItemID})
	a.audit(r, p, "assignment.deleted", fmt.Sprintf("%s:%d", as.ItemType, as.ItemID), map[string]any{"group_id": as.GroupID})
	return ok(w, map[string]bool{"ok": true})
}

// ---- profiles ----

func (a *API) profileSchemas(w http.ResponseWriter, r *http.Request, p *Principal) error {
	return ok(w, map[string]any{"items": profiles.Schemas, "variables": mdm.VariableNames()})
}

type profileView struct {
	*store.Profile
	Assignments int            `json:"assignments"`
	States      map[string]int `json:"states"`
}

func (a *API) listProfiles(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ps, err := a.Store.ListProfiles()
	if err != nil {
		return err
	}
	out := make([]profileView, 0, len(ps))
	for _, pr := range ps {
		as, _ := a.Store.ListAssignments("profile", pr.ID, 0)
		states, _ := a.Store.ListProfileStates("", pr.ID)
		counts := map[string]int{}
		for _, st := range states {
			counts[st.Status]++
		}
		pr.Raw = nil
		pr.Payloads = nil
		out = append(out, profileView{pr, len(as), counts})
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	pr, err := a.Store.GetProfile(id)
	if err != nil {
		return err
	}
	as, _ := a.Store.ListAssignments("profile", id, 0)
	return ok(w, map[string]any{"profile": pr, "xml": string(pr.Raw), "assignments": a.describeAssignments(as)})
}

type profileReq struct {
	Name        string                  `json:"name"`
	Identifier  string                  `json:"identifier"`
	Description string                  `json:"description"`
	Removable   bool                    `json:"removable"`
	Payloads    []profiles.PayloadInput `json:"payloads"`
}

// MarshalPayloads stores builder payloads as JSON.
func MarshalPayloads(p []profiles.PayloadInput) (json.RawMessage, error) {
	b, err := json.Marshal(p)
	return json.RawMessage(b), err
}

func (a *API) buildProfile(req *profileReq) (*profiles.Result, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Identifier = strings.TrimSpace(req.Identifier)
	if req.Identifier == "" {
		req.Identifier = "com.orchardmdm.profile." + strings.ToLower(randomHex(4))
	}
	res, err := profiles.Build(profiles.Meta{Name: req.Name, Identifier: req.Identifier, Description: req.Description,
		Organization: a.MDM.Setting(mdm.SettingOrgName), Removable: req.Removable}, req.Payloads)
	if err != nil {
		return nil, badRequest("%v", err)
	}
	return res, nil
}

func (a *API) createProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req profileReq
	if err := decode(r, &req); err != nil {
		return err
	}
	res, err := a.buildProfile(&req)
	if err != nil {
		return err
	}
	raw, _ := MarshalPayloads(res.Payloads)
	pr := &store.Profile{Name: req.Name, Identifier: req.Identifier, Description: req.Description, Source: "builder", Payloads: raw, Raw: res.XML,
		Removable: req.Removable, Scope: "System", PayloadTypes: res.PayloadTypes}
	if err := a.Store.CreateProfile(pr); err != nil {
		return err
	}
	a.audit(r, p, "profile.created", pr.Name, map[string]any{"identifier": pr.Identifier, "payloads": pr.PayloadTypes})
	return ok(w, map[string]any{"profile": pr, "warnings": res.Warnings})
}

func (a *API) uploadProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Data        string `json:"data"` // base64 .mobileconfig
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil || len(data) == 0 {
		return badRequest("upload a .mobileconfig file")
	}
	parsed, err := profiles.Parse(data)
	if err != nil {
		return badRequest("%v", err)
	}
	xml := parsed.XML
	if req.Name != "" || req.Description != "" {
		if xml, err = profiles.Rewrap(xml, "", req.Name, req.Description); err != nil {
			return err
		}
		if req.Name != "" {
			parsed.Name = req.Name
		}
		if req.Description != "" {
			parsed.Description = req.Description
		}
	}
	existing, err := a.Store.GetProfileByIdentifier(parsed.Identifier)
	if err == nil {
		existing.Name, existing.Description, existing.Raw, existing.Source = parsed.Name, parsed.Description, xml, "upload"
		existing.PayloadTypes, existing.Payloads, existing.Removable = parsed.PayloadTypes, nil, parsed.Removable
		existing.Version++
		if err := a.Store.UpdateProfile(existing); err != nil {
			return err
		}
		a.Bus.Publish(events.ProfileChanged, "", map[string]any{"profile_id": existing.ID})
		a.audit(r, p, "profile.replaced", existing.Name, map[string]any{"identifier": existing.Identifier, "version": existing.Version})
		return ok(w, map[string]any{"profile": existing, "warnings": parsed.Warnings, "replaced": true})
	}
	pr := &store.Profile{Name: parsed.Name, Identifier: parsed.Identifier, Description: parsed.Description, Source: "upload", Raw: xml,
		Removable: parsed.Removable, Scope: "System", PayloadTypes: parsed.PayloadTypes}
	if err := a.Store.CreateProfile(pr); err != nil {
		return err
	}
	a.audit(r, p, "profile.uploaded", pr.Name, map[string]any{"identifier": pr.Identifier, "payloads": pr.PayloadTypes})
	return ok(w, map[string]any{"profile": pr, "warnings": parsed.Warnings})
}

func (a *API) updateProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	pr, err := a.Store.GetProfile(id)
	if err != nil {
		return err
	}
	var req profileReq
	if err := decode(r, &req); err != nil {
		return err
	}
	var warnings []string
	if pr.Source == "builder" {
		req.Identifier = pr.Identifier // the identifier must stay stable
		res, err := a.buildProfile(&req)
		if err != nil {
			return err
		}
		raw, _ := MarshalPayloads(res.Payloads)
		pr.Payloads, pr.Raw, pr.PayloadTypes, warnings = raw, res.XML, res.PayloadTypes, res.Warnings
		pr.Name, pr.Description, pr.Removable = req.Name, req.Description, req.Removable
	} else {
		xml, err := profiles.Rewrap(pr.Raw, "", req.Name, req.Description)
		if err != nil {
			return err
		}
		pr.Raw = xml
		if strings.TrimSpace(req.Name) != "" {
			pr.Name = req.Name
		}
		pr.Description = req.Description
	}
	pr.Version++
	if err := a.Store.UpdateProfile(pr); err != nil {
		return err
	}
	a.Bus.Publish(events.ProfileChanged, "", map[string]any{"profile_id": pr.ID})
	a.audit(r, p, "profile.updated", pr.Name, map[string]any{"version": pr.Version})
	return ok(w, map[string]any{"profile": pr, "warnings": warnings})
}

func (a *API) deleteProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	pr, err := a.Store.GetProfile(id)
	if err != nil {
		return err
	}
	if err := a.Store.DeleteProfile(id); err != nil {
		return err
	}
	// devices that have it installed get it removed by the reconciler
	a.Bus.Publish(events.AssignmentsChanged, "", nil)
	a.audit(r, p, "profile.deleted", pr.Name, map[string]string{"identifier": pr.Identifier})
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) downloadProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	pr, err := a.Store.GetProfile(id)
	if err != nil {
		return err
	}
	data := pr.Raw
	if r.URL.Query().Get("signed") == "1" {
		data, _ = a.MDM.SignProfileBytes(pr.Raw)
	}
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.mobileconfig"`, safeFilename(pr.Name)))
	_, _ = w.Write(data)
	return nil
}

func safeFilename(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`"\/:*?<>|`, r) {
			return '_'
		}
		return r
	}, s)
	if s == "" {
		return "profile"
	}
	return s
}

func (a *API) profileStatus(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	states, err := a.Store.ListProfileStates("", id)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": states})
}

func (a *API) retryProfile(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	states, _ := a.Store.ListProfileStates("", id)
	n := 0
	for _, st := range states {
		if st.Status == "failed" || st.Status == "remove_failed" || st.Status == "missing" {
			_ = a.Store.DeleteProfileState(st.DeviceID, id)
			n++
		}
	}
	a.Reconciler.TriggerAll()
	a.audit(r, p, "profile.retry", fmt.Sprint(id), map[string]int{"reset": n})
	return ok(w, map[string]int{"reset": n})
}

// ---- apps ----

type appView struct {
	*store.App
	Assignments int            `json:"assignments"`
	Installed   int            `json:"installed"`
	States      map[string]int `json:"states"`
}

func (a *API) listApps(w http.ResponseWriter, r *http.Request, p *Principal) error {
	list, err := a.Store.ListApps()
	if err != nil {
		return err
	}
	counts, _ := a.Store.AppInstallCounts()
	out := make([]appView, 0, len(list))
	for _, app := range list {
		as, _ := a.Store.ListAssignments("app", app.ID, 0)
		states, _ := a.Store.ListAppStates("", app.ID)
		sc := map[string]int{}
		for _, st := range states {
			sc[st.Status]++
		}
		out = append(out, appView{app, len(as), counts[app.BundleID], sc})
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) searchApps(w http.ResponseWriter, r *http.Request, p *Principal) error {
	term := strings.TrimSpace(r.URL.Query().Get("term"))
	if term == "" {
		return badRequest("term is required")
	}
	country := r.URL.Query().Get("country")
	var res []apps.StoreApp
	var err error
	if id := apps.ParseStoreID(term); id != "" {
		res, err = a.ITunes.Lookup(r.Context(), []string{id}, country)
	} else if strings.Count(term, ".") >= 2 && !strings.Contains(term, " ") {
		var one *apps.StoreApp
		one, err = a.ITunes.LookupBundle(r.Context(), term, country)
		if one != nil {
			res = []apps.StoreApp{*one}
		}
	} else {
		res, err = a.ITunes.Search(r.Context(), term, country, 25)
	}
	if err != nil {
		return errf(http.StatusBadGateway, "App Store lookup failed: %v", err)
	}
	if res == nil {
		res = []apps.StoreApp{}
	}
	return ok(w, map[string]any{"items": res})
}

type appReq struct {
	Kind             string          `json:"kind"`
	ITunesID         string          `json:"itunes_id"`
	Country          string          `json:"country"`
	Name             string          `json:"name"`
	BundleID         string          `json:"bundle_id"`
	Config           json.RawMessage `json:"config"`
	Attributes       json.RawMessage `json:"attributes"`
	RemoveOnUnenroll *bool           `json:"remove_on_unenroll"`
	PreventBackup    *bool           `json:"prevent_backup"`
	UseVPP           *bool           `json:"use_vpp"`
	TakeManagement   *bool           `json:"take_management"`
	Description      *string         `json:"description"`
}

func validJSONObject(raw json.RawMessage) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return badRequest("configuration must be a JSON object: %v", err)
	}
	return nil
}

func applyAppReq(app *store.App, req *appReq) error {
	if err := validJSONObject(req.Config); err != nil {
		return err
	}
	if err := validJSONObject(req.Attributes); err != nil {
		return err
	}
	if req.Config != nil {
		app.Config = req.Config
	}
	if req.Attributes != nil {
		app.Attributes = req.Attributes
	}
	if req.RemoveOnUnenroll != nil {
		app.RemoveOnUnenroll = *req.RemoveOnUnenroll
	}
	if req.PreventBackup != nil {
		app.PreventBackup = *req.PreventBackup
	}
	if req.UseVPP != nil {
		app.UseVPP = *req.UseVPP
	}
	if req.TakeManagement != nil {
		app.TakeManagement = *req.TakeManagement
	}
	if req.Description != nil {
		app.Description = *req.Description
	}
	if strings.TrimSpace(req.Name) != "" {
		app.Name = strings.TrimSpace(req.Name)
	}
	return nil
}

func (a *API) createApp(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req appReq
	if err := decode(r, &req); err != nil {
		return err
	}
	id := apps.ParseStoreID(req.ITunesID)
	if id == "" {
		return badRequest("enter an App Store ID or apps.apple.com URL")
	}
	res, err := a.ITunes.Lookup(r.Context(), []string{id}, req.Country)
	if err != nil {
		return errf(http.StatusBadGateway, "App Store lookup failed: %v", err)
	}
	if len(res) == 0 {
		return badRequest("no App Store app with ID %s", id)
	}
	info := res[0]
	app := &store.App{Kind: "appstore", Name: info.TrackName, BundleID: info.BundleID, ITunesID: info.TrackID, Version: info.Version, IconURL: info.Icon(),
		Seller: info.SellerName, Description: truncate(info.Description, 2000), RemoveOnUnenroll: true, TakeManagement: true}
	if req.Kind == "vpp" {
		app.Kind, app.UseVPP = "vpp", true
	}
	if err := applyAppReq(app, &req); err != nil {
		return err
	}
	if err := a.Store.CreateApp(app); err != nil {
		return err
	}
	a.audit(r, p, "app.created", app.Name, map[string]any{"itunes_id": app.ITunesID, "bundle_id": app.BundleID})
	return ok(w, app)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (a *API) appsDir() string { return filepath.Join(a.Cfg.DataDir, "apps") }

func (a *API) uploadEnterpriseApp(w http.ResponseWriter, r *http.Request, p *Principal) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4<<30)
	mr, err := r.MultipartReader()
	if err != nil {
		return badRequest("expected a multipart upload with an .ipa file")
	}
	if err := os.MkdirAll(a.appsDir(), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(a.appsDir(), "upload-*.ipa")
	if err != nil {
		return err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()
	fields := map[string]string{}
	var size int64
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return badRequest("upload failed: %v", err)
		}
		if part.FormName() == "file" {
			size, err = io.Copy(tmp, part)
			if err != nil {
				return badRequest("upload failed: %v", err)
			}
		} else {
			b, _ := io.ReadAll(io.LimitReader(part, 1<<20))
			fields[part.FormName()] = string(b)
		}
	}
	if size == 0 {
		return badRequest("no .ipa file received")
	}
	info, err := apps.ParseIPA(tmp, size)
	if err != nil {
		return badRequest("%v", err)
	}
	secret := pki.RandomToken(16)
	app := &store.App{Kind: "enterprise", Name: info.Name, BundleID: info.BundleID, Version: info.Version, IPASize: size, FileSecret: secret,
		Description: "Minimum iOS " + info.MinOS, RemoveOnUnenroll: true, TakeManagement: true}
	if v := fields["name"]; strings.TrimSpace(v) != "" {
		app.Name = strings.TrimSpace(v)
	}
	if err := a.Store.CreateApp(app); err != nil {
		return err
	}
	final := filepath.Join(a.appsDir(), fmt.Sprintf("%d.ipa", app.ID))
	tmp.Close()
	if err := os.Rename(tmp.Name(), final); err != nil {
		_ = a.Store.DeleteApp(app.ID)
		return err
	}
	app.IPAFile = final
	_ = a.Store.UpdateApp(app)
	a.audit(r, p, "app.uploaded", app.Name, map[string]any{"bundle_id": app.BundleID, "version": app.Version, "size": size})
	return ok(w, app)
}

func (a *API) getApp(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	app, err := a.Store.GetApp(id)
	if err != nil {
		return err
	}
	as, _ := a.Store.ListAssignments("app", id, 0)
	out := map[string]any{"app": app, "assignments": a.describeAssignments(as)}
	if app.Kind == "enterprise" && app.FileSecret != "" {
		out["manifest_url"] = fmt.Sprintf("%s/files/apps/%d/%s/manifest.plist", a.MDM.PublicURL(), app.ID, app.FileSecret)
	}
	if app.ITunesID > 0 {
		assets, _ := a.Store.ListVPPAssets(0)
		for _, as := range assets {
			if as.AdamID == fmt.Sprint(app.ITunesID) {
				out["licenses"] = as
			}
		}
	}
	return ok(w, out)
}

func (a *API) updateApp(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	app, err := a.Store.GetApp(id)
	if err != nil {
		return err
	}
	var req appReq
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := applyAppReq(app, &req); err != nil {
		return err
	}
	if err := a.Store.UpdateApp(app); err != nil {
		return err
	}
	a.Bus.Publish(events.AppChanged, "", map[string]any{"app_id": app.ID})
	a.audit(r, p, "app.updated", app.Name, nil)
	return ok(w, app)
}

func (a *API) deleteApp(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	app, err := a.Store.GetApp(id)
	if err != nil {
		return err
	}
	if err := a.Store.DeleteApp(id); err != nil {
		return err
	}
	if app.IPAFile != "" {
		_ = os.Remove(app.IPAFile)
	}
	a.audit(r, p, "app.deleted", app.Name, nil)
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) appStatus(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	states, err := a.Store.ListAppStates("", id)
	if err != nil {
		return err
	}
	return ok(w, map[string]any{"items": states})
}

func (a *API) retryApp(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	states, _ := a.Store.ListAppStates("", id)
	n := 0
	for _, st := range states {
		if st.Status == "failed" || st.Status == "missing" {
			_ = a.Store.DeleteAppState(st.DeviceID, id)
			n++
		}
	}
	a.Reconciler.TriggerAll()
	a.audit(r, p, "app.retry", fmt.Sprint(id), map[string]int{"reset": n})
	return ok(w, map[string]int{"reset": n})
}

func (a *API) installAppOnDevices(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var req struct {
		UDIDs []string `json:"udids"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	res := a.MDM.SendCatalogCommand("InstallApplication", req.UDIDs, mdm.Params{"app_id": float64(id)}, mdm.Meta{Source: "manual", CreatedBy: p.Actor()})
	a.audit(r, p, "app.install", fmt.Sprint(id), map[string]int{"devices": len(req.UDIDs)})
	return ok(w, map[string]any{"results": res})
}

// ---- declarations ----

type declTemplate struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Payload     json.RawMessage `json:"payload"`
}

var declarationTemplates = []declTemplate{
	{"com.apple.configuration.passcode.settings", "Passcode", "Passcode requirements (iOS 16+).",
		json.RawMessage(`{"RequirePasscode": true, "RequireAlphanumericPasscode": false, "MinimumLength": 6, "MaximumInactivityInMinutes": 5, "MaximumGracePeriodInMinutes": 0, "PasscodeReuseLimit": 3}`)},
	{"com.apple.configuration.softwareupdate.enforcement.specific", "Enforce OS update", "Install a specific OS version by a deadline (iOS 17+).",
		json.RawMessage(`{"TargetOSVersion": "18.6", "TargetLocalDateTime": "2026-12-31T18:00:00", "DetailsURL": "https://support.apple.com/en-us/100100"}`)},
	{"com.apple.configuration.softwareupdate.settings", "Software update settings", "Deferrals, automatic actions and notifications (iOS 18+).",
		json.RawMessage(`{"Notifications": true, "Deferrals": {"CombinedPeriodInDays": 14}, "AutomaticActions": {"Download": "AlwaysOn", "InstallOSUpdates": "AlwaysOn"}}`)},
	{"com.apple.configuration.account.mail", "Mail account", "IMAP/SMTP account (iOS 17+).",
		json.RawMessage(`{"UserIdentityAssetReference": "", "IncomingServer": {"ServerType": "IMAP", "HostName": "imap.example.com", "Port": 993, "AuthenticationMethod": "Password"}, "OutgoingServer": {"HostName": "smtp.example.com", "Port": 587, "AuthenticationMethod": "Password"}}`)},
	{"com.apple.configuration.legacy", "Legacy profile", "Install a configuration profile through DDM. Point ProfileURL at a profile download URL.",
		json.RawMessage(`{"ProfileURL": "https://example.com/profile.mobileconfig"}`)},
	{"com.apple.configuration.app.managed", "Managed app", "Install and manage an App Store app declaratively (iOS 17.2+).",
		json.RawMessage(`{"AppStoreID": "618783545", "InstallBehavior": {"Install": "Required"}}`)},
	{"com.apple.configuration.safari.bookmarks", "Safari bookmarks", "Managed Safari bookmarks (iOS 18+).",
		json.RawMessage(`{"ManagedBookmarks": [{"GroupIdentifier": "corp", "Title": "Company", "Bookmarks": [{"Title": "Intranet", "URL": "https://intranet.example.com"}]}]}`)},
	{"com.apple.configuration.math.settings", "Math settings", "Calculator and Math Notes settings (iOS 18+).",
		json.RawMessage(`{"Calculator": {"BasicMode": {"AddSquareRoot": false}, "ScientificMode": {"Enabled": true}, "ProgrammerMode": {"Enabled": true}, "MathNotes": {"Enabled": true}}}`)},
	{"com.apple.configuration.management.status-subscriptions", "Status subscriptions", "Additional status items to report (Orchard already subscribes to common items).",
		json.RawMessage(`{"StatusItems": [{"Name": "device.operating-system.version"}]}`)},
	{"com.apple.activation.simple", "Activation", "Activate configurations only under a predicate (advanced).",
		json.RawMessage(`{"StandardConfigurations": [], "Predicate": "@status(device.model.family) == 'iPad'"}`)},
	{"com.apple.asset.credential.userpassword", "Credential asset", "Username/password asset referenced by accounts.",
		json.RawMessage(`{"Reference": {"DataURL": "https://example.com/credential.json", "ContentType": "application/json"}}`)},
	{"com.apple.management.organization-info", "Organization info", "Organization details shown to the user.",
		json.RawMessage(`{"Name": "Example Inc", "Email": "it@example.com", "URL": "https://example.com"}`)},
}

func (a *API) declarationTemplates(w http.ResponseWriter, r *http.Request, p *Principal) error {
	return ok(w, map[string]any{"items": declarationTemplates})
}

type declView struct {
	*store.Declaration
	Kind        string `json:"kind"`
	Assignments int    `json:"assignments"`
}

func (a *API) listDeclarations(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ds, err := a.Store.ListDeclarations()
	if err != nil {
		return err
	}
	out := make([]declView, 0, len(ds))
	for _, d := range ds {
		as, _ := a.Store.ListAssignments("declaration", d.ID, 0)
		out = append(out, declView{d, ddm.KindOf(d.Type), len(as)})
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getDeclaration(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := a.Store.GetDeclaration(id)
	if err != nil {
		return err
	}
	as, _ := a.Store.ListAssignments("declaration", id, 0)
	return ok(w, map[string]any{"declaration": d, "kind": ddm.KindOf(d.Type), "assignments": a.describeAssignments(as)})
}

type declReq struct {
	Identifier  string          `json:"identifier"`
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Payload     json.RawMessage `json:"payload"`
}

func validateDecl(req *declReq) error {
	req.Type = strings.TrimSpace(req.Type)
	if ddm.KindOf(req.Type) == "" {
		return badRequest("type must start with com.apple.configuration., com.apple.asset., com.apple.activation. or com.apple.management.")
	}
	if strings.TrimSpace(req.Name) == "" {
		req.Name = req.Type
	}
	if len(req.Payload) == 0 {
		req.Payload = json.RawMessage(`{}`)
	}
	var m map[string]any
	if err := json.Unmarshal(req.Payload, &m); err != nil {
		return badRequest("payload must be a JSON object: %v", err)
	}
	compact, _ := json.Marshal(m)
	req.Payload = compact
	if strings.TrimSpace(req.Identifier) == "" {
		req.Identifier = "com.orchardmdm.declaration." + strings.ToLower(randomHex(4))
	}
	if strings.HasPrefix(req.Identifier, "orchard.") {
		return badRequest("identifiers starting with orchard. are reserved")
	}
	return nil
}

func (a *API) createDeclaration(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req declReq
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := validateDecl(&req); err != nil {
		return err
	}
	d := &store.Declaration{Identifier: req.Identifier, Type: req.Type, Name: req.Name, Description: req.Description, Payload: req.Payload,
		ServerToken: ddm.TokenFor(req.Type, req.Payload)}
	if err := a.Store.CreateDeclaration(d); err != nil {
		return err
	}
	a.audit(r, p, "declaration.created", d.Name, map[string]string{"type": d.Type})
	return ok(w, d)
}

func (a *API) updateDeclaration(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := a.Store.GetDeclaration(id)
	if err != nil {
		return err
	}
	var req declReq
	if err := decode(r, &req); err != nil {
		return err
	}
	req.Identifier = d.Identifier
	if err := validateDecl(&req); err != nil {
		return err
	}
	d.Type, d.Name, d.Description, d.Payload = req.Type, req.Name, req.Description, req.Payload
	d.ServerToken = ddm.TokenFor(d.Type, d.Payload)
	if err := a.Store.UpdateDeclaration(d); err != nil {
		return err
	}
	a.Bus.Publish(events.DeclarationsChanged, "", nil)
	a.audit(r, p, "declaration.updated", d.Name, nil)
	return ok(w, d)
}

func (a *API) deleteDeclaration(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	d, err := a.Store.GetDeclaration(id)
	if err != nil {
		return err
	}
	if err := a.Store.DeleteDeclaration(id); err != nil {
		return err
	}
	a.Bus.Publish(events.DeclarationsChanged, "", nil)
	a.audit(r, p, "declaration.deleted", d.Name, nil)
	return ok(w, map[string]bool{"ok": true})
}

// ---- compliance ----

type policyView struct {
	*store.CompliancePolicy
	Assignments int `json:"assignments"`
}

func (a *API) listPolicies(w http.ResponseWriter, r *http.Request, p *Principal) error {
	ps, err := a.Store.ListCompliancePolicies()
	if err != nil {
		return err
	}
	out := make([]policyView, 0, len(ps))
	for _, x := range ps {
		as, _ := a.Store.ListAssignments("compliance", x.ID, 0)
		out = append(out, policyView{x, len(as)})
	}
	return ok(w, map[string]any{"items": out})
}

func (a *API) getPolicy(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	x, err := a.Store.GetCompliancePolicy(id)
	if err != nil {
		return err
	}
	as, _ := a.Store.ListAssignments("compliance", id, 0)
	return ok(w, map[string]any{"policy": x, "assignments": a.describeAssignments(as)})
}

type policyReq struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Enabled     bool               `json:"enabled"`
	Rules       compliance.Rules   `json:"rules"`
	Actions     compliance.Actions `json:"actions"`
	GraceHours  int                `json:"grace_hours"`
	Assign      []int64            `json:"assign_group_ids,omitempty"`
}

func (a *API) savePolicy(x *store.CompliancePolicy, req *policyReq) error {
	if strings.TrimSpace(req.Name) == "" {
		return badRequest("name is required")
	}
	if req.GraceHours < 0 || req.Actions.LockAfterHours < 0 {
		return badRequest("hours cannot be negative")
	}
	rules, _ := json.Marshal(req.Rules)
	actions, _ := json.Marshal(req.Actions)
	x.Name, x.Description, x.Enabled, x.Rules, x.Actions, x.GraceHours = strings.TrimSpace(req.Name), req.Description, req.Enabled, rules, actions, req.GraceHours
	return nil
}

func (a *API) createPolicy(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req policyReq
	if err := decode(r, &req); err != nil {
		return err
	}
	x := &store.CompliancePolicy{}
	if err := a.savePolicy(x, &req); err != nil {
		return err
	}
	if err := a.Store.CreateCompliancePolicy(x); err != nil {
		return err
	}
	a.audit(r, p, "compliance.created", x.Name, nil)
	go a.Compliance.EvaluateAll()
	return ok(w, x)
}

func (a *API) updatePolicy(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	x, err := a.Store.GetCompliancePolicy(id)
	if err != nil {
		return err
	}
	var req policyReq
	if err := decode(r, &req); err != nil {
		return err
	}
	if err := a.savePolicy(x, &req); err != nil {
		return err
	}
	if err := a.Store.UpdateCompliancePolicy(x); err != nil {
		return err
	}
	a.audit(r, p, "compliance.updated", x.Name, nil)
	go a.Compliance.EvaluateAll()
	return ok(w, x)
}

func (a *API) deletePolicy(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	x, err := a.Store.GetCompliancePolicy(id)
	if err != nil {
		return err
	}
	if err := a.Store.DeleteCompliancePolicy(id); err != nil {
		return err
	}
	a.audit(r, p, "compliance.deleted", x.Name, nil)
	go a.Compliance.EvaluateAll()
	return ok(w, map[string]bool{"ok": true})
}

func (a *API) complianceSummary(w http.ResponseWriter, r *http.Request, p *Principal) error {
	counts, err := a.Store.CountBy("compliance")
	if err != nil {
		return err
	}
	devs, _, err := a.Store.ListDevices(store.DeviceFilter{Status: "enrolled", Sort: "name"})
	if err != nil {
		return err
	}
	var failing []DeviceSummary
	for _, d := range devs {
		if d.Compliance == "noncompliant" || d.Compliance == "grace" {
			failing = append(failing, summarize(d))
		}
	}
	if failing == nil {
		failing = []DeviceSummary{}
	}
	return ok(w, map[string]any{"counts": counts, "devices": failing})
}

func (a *API) complianceEvaluate(w http.ResponseWriter, r *http.Request, p *Principal) error {
	a.Compliance.EvaluateAll()
	a.audit(r, p, "compliance.evaluate", "all", nil)
	return a.complianceSummary(w, r, p)
}
