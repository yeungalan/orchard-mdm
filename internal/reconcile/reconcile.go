package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/ddm"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/profiles"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"github.com/yeungalan/orchard-mdm/internal/vpp"
)

// MaxAppAttempts bounds automatic re-installs of a failing required app.
const MaxAppAttempts = 3

// Reconciler computes and applies desired state.
type Reconciler struct {
	store  *store.Store
	mdm    *mdm.Service
	ddm    *ddm.Service
	log    *slog.Logger
	vppURL string

	mu sync.Mutex // serialises reconciliation

	trigMu   sync.Mutex
	trigAll  *time.Timer
	trigDevs map[string]bool
	trigDev  *time.Timer
	delay    time.Duration
}

// New creates a reconciler.
func New(s *store.Store, m *mdm.Service, log *slog.Logger, vppURL string) *Reconciler {
	return &Reconciler{store: s, mdm: m, ddm: m.DDM(), log: log, vppURL: vppURL, trigDevs: map[string]bool{}, delay: 2 * time.Second}
}

// SetDelay sets the debounce delay for triggered reconciliations.
func (r *Reconciler) SetDelay(d time.Duration) { r.delay = d }

// Subscribe wires the reconciler to the event bus.
func (r *Reconciler) Subscribe(bus *events.Bus) {
	bus.Subscribe(func(e events.Event) {
		awaiting, _ := e.Data["awaiting_configuration"].(bool)
		r.mu.Lock()
		r.refreshDeviceGroupsLocked(e.DeviceID)
		n := r.reconcileDeviceLocked(e.DeviceID)
		r.mu.Unlock()
		if awaiting {
			// release Setup Assistant once the initial configuration is queued
			_, _ = r.mdm.Enqueue(e.DeviceID, map[string]any{"RequestType": "DeviceConfigured"}, mdm.Meta{Source: "system", Ref: "await-configuration"})
			n++
		}
		if n > 0 {
			r.mdm.Push(e.DeviceID)
		}
	}, events.DeviceEnrolled)
	bus.Subscribe(func(e events.Event) { r.TriggerDevice(e.DeviceID) }, events.DeviceInventory, events.ComplianceChanged)
	bus.Subscribe(func(e events.Event) {
		if e.Data["request_type"] == "DeclarativeManagement" {
			_ = r.store.UpdateDevice(e.DeviceID, map[string]any{"ddm_token": ""})
		}
	}, events.CommandFailed)
	bus.Subscribe(func(events.Event) { r.TriggerAll() },
		events.AssignmentsChanged, events.GroupsChanged, events.ProfileChanged, events.AppChanged, events.DeclarationsChanged)
}

// TriggerAll schedules a full reconciliation (debounced).
func (r *Reconciler) TriggerAll() {
	r.trigMu.Lock()
	defer r.trigMu.Unlock()
	if r.trigAll != nil {
		r.trigAll.Stop()
	}
	r.trigAll = time.AfterFunc(r.delay, func() { r.ReconcileAll(context.Background()) })
}

// TriggerDevice schedules reconciliation of one device (debounced).
func (r *Reconciler) TriggerDevice(udid string) {
	if udid == "" {
		return
	}
	r.trigMu.Lock()
	defer r.trigMu.Unlock()
	r.trigDevs[udid] = true
	if r.trigDev == nil {
		r.trigDev = time.AfterFunc(r.delay, r.flushDevices)
	}
}

func (r *Reconciler) flushDevices() {
	r.trigMu.Lock()
	ids := make([]string, 0, len(r.trigDevs))
	for u := range r.trigDevs {
		ids = append(ids, u)
	}
	r.trigDevs = map[string]bool{}
	r.trigDev = nil
	r.trigMu.Unlock()
	for _, u := range ids {
		r.ReconcileDevice(u)
	}
}

// RefreshGroups recomputes built-in and dynamic group membership.
func (r *Reconciler) RefreshGroups() (changed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.refreshGroupsLocked()
}

func (r *Reconciler) refreshGroupsLocked() bool {
	groups, err := r.store.ListGroups()
	if err != nil {
		return false
	}
	devices, err := r.store.EnrolledDevices()
	if err != nil {
		return false
	}
	appCache := map[string]map[string]store.DeviceApp{}
	changed := false
	for _, g := range groups {
		var members []string
		switch g.Kind {
		case "all":
			for _, d := range devices {
				members = append(members, d.UDID)
			}
		case "dynamic":
			for _, d := range devices {
				var apps map[string]store.DeviceApp
				if needsApps(g.Rules) {
					if apps = appCache[d.UDID]; apps == nil {
						apps, _ = r.store.DeviceAppBundleIDs(d.UDID)
						appCache[d.UDID] = apps
					}
				}
				if MatchRules(d, g.Rules, apps) {
					members = append(members, d.UDID)
				}
			}
		default:
			continue
		}
		c, err := r.store.SetGroupMembers(g.ID, members)
		if err != nil {
			r.log.Error("set group members", "group", g.Name, "err", err)
		}
		changed = changed || c
	}
	return changed
}

// refreshDeviceGroupsLocked updates built-in/dynamic membership for one device.
func (r *Reconciler) refreshDeviceGroupsLocked(udid string) {
	d, err := r.store.GetDevice(udid)
	if err != nil {
		return
	}
	groups, err := r.store.ListGroups()
	if err != nil {
		return
	}
	var apps map[string]store.DeviceApp
	for _, g := range groups {
		if g.Kind != "all" && g.Kind != "dynamic" {
			continue
		}
		in := d.EnrollmentStatus == "enrolled"
		if in && g.Kind == "dynamic" {
			if needsApps(g.Rules) && apps == nil {
				apps, _ = r.store.DeviceAppBundleIDs(udid)
			}
			in = MatchRules(d, g.Rules, apps)
		}
		if in {
			_ = r.store.AddGroupMembers(g.ID, []string{udid})
		} else {
			_ = r.store.RemoveGroupMembers(g.ID, []string{udid})
		}
	}
}

// ReconcileAll refreshes groups and reconciles every enrolled device.
func (r *Reconciler) ReconcileAll(ctx context.Context) {
	r.mu.Lock()
	r.refreshGroupsLocked()
	udids, err := r.store.EnrolledUDIDs()
	if err != nil {
		r.mu.Unlock()
		return
	}
	var pushed []string
	for _, u := range udids {
		if ctx.Err() != nil {
			break
		}
		if r.reconcileDeviceLocked(u) > 0 {
			pushed = append(pushed, u)
		}
	}
	r.mu.Unlock()
	r.mdm.Push(pushed...)
}

// ReconcileDevice reconciles one device and pushes it when work was queued.
func (r *Reconciler) ReconcileDevice(udid string) int {
	r.mu.Lock()
	r.refreshDeviceGroupsLocked(udid)
	n := r.reconcileDeviceLocked(udid)
	r.mu.Unlock()
	if n > 0 {
		r.mdm.Push(udid)
	}
	return n
}

type desired struct {
	profiles map[int64]bool
	apps     map[int64]string // install|uninstall|available
	excluded map[string]bool  // "type:id"
}

func (r *Reconciler) desiredFor(udid string) (*desired, error) {
	assigns, err := r.store.DeviceAssignments(udid)
	if err != nil {
		return nil, err
	}
	out := &desired{profiles: map[int64]bool{}, apps: map[int64]string{}, excluded: map[string]bool{}}
	for _, a := range assigns {
		if a.Intent == "exclude" {
			out.excluded[fmt.Sprintf("%s:%d", a.ItemType, a.ItemID)] = true
		}
	}
	for _, a := range assigns {
		if a.Intent == "exclude" || out.excluded[fmt.Sprintf("%s:%d", a.ItemType, a.ItemID)] {
			continue
		}
		switch a.ItemType {
		case "profile":
			out.profiles[a.ItemID] = true
		case "app":
			prev := out.apps[a.ItemID]
			// install beats uninstall beats available
			rank := map[string]int{"install": 3, "uninstall": 2, "available": 1}
			if rank[a.Intent] > rank[prev] {
				out.apps[a.ItemID] = a.Intent
			}
		}
	}
	return out, nil
}

// reconcileDeviceLocked queues the commands needed for a device and returns how many were queued.
func (r *Reconciler) reconcileDeviceLocked(udid string) int {
	d, err := r.store.GetDevice(udid)
	if err != nil || d.EnrollmentStatus != "enrolled" {
		return 0
	}
	want, err := r.desiredFor(udid)
	if err != nil {
		r.log.Error("desired state", "udid", udid, "err", err)
		return 0
	}
	n := r.reconcileProfiles(d, want)
	n += r.reconcileApps(d, want)
	n += r.reconcileDeclarations(d)
	return n
}

func (r *Reconciler) reconcileProfiles(d *store.Device, want *desired) int {
	states, _ := r.store.ListProfileStates(d.UDID, 0)
	byID := map[int64]*store.ProfileState{}
	for _, st := range states {
		byID[st.ProfileID] = st
	}
	n := 0
	for pid := range want.profiles {
		p, err := r.store.GetProfile(pid)
		if err != nil {
			continue
		}
		st := byID[pid]
		ref := fmt.Sprintf("profile:%d", pid)
		if st != nil && st.Version == p.Version {
			switch st.Status {
			case "installed", "failed", "skipped":
				continue
			case "pending":
				if r.store.HasPendingCommand(d.UDID, "InstallProfile", ref) {
					continue
				}
			}
		}
		if d.UserEnrollment {
			if bad := profiles.UnsupportedOnUserEnrollment(p.PayloadTypes); len(bad) > 0 {
				_ = r.store.SetProfileState(&store.ProfileState{DeviceID: d.UDID, ProfileID: pid, Identifier: p.Identifier, Version: p.Version, Status: "skipped",
					Error: "Personal devices enrolled with User Enrollment don't accept " + strings.Join(bad, ", ")})
				continue
			}
		}
		payload, err := r.mdm.ProfileForDevice(p, d)
		if err != nil {
			_ = r.store.SetProfileState(&store.ProfileState{DeviceID: d.UDID, ProfileID: pid, Identifier: p.Identifier, Version: p.Version, Status: "failed", Error: err.Error()})
			continue
		}
		cmd, err := r.mdm.Enqueue(d.UDID, map[string]any{"RequestType": "InstallProfile", "Payload": payload}, mdm.Meta{Source: "reconcile", Ref: ref})
		if err != nil {
			continue
		}
		_ = r.store.SetProfileState(&store.ProfileState{DeviceID: d.UDID, ProfileID: pid, Identifier: p.Identifier, Version: p.Version, Status: "pending", CommandUUID: cmd.UUID})
		n++
	}
	for pid, st := range byID {
		if want.profiles[pid] {
			continue
		}
		if st.Status == "skipped" {
			_ = r.store.DeleteProfileState(d.UDID, pid)
			continue
		}
		if st.Status == "failed" && st.Version > 0 && st.CommandUUID != "" {
			// never installed: forget it
			if c, err := r.store.GetCommand(st.CommandUUID); err == nil && c.RequestType == "InstallProfile" {
				_ = r.store.DeleteProfileState(d.UDID, pid)
				continue
			}
		}
		ref := fmt.Sprintf("profile:%d", pid)
		if st.Status == "removing" && r.store.HasPendingCommand(d.UDID, "RemoveProfile", ref) {
			continue
		}
		if st.Status == "remove_failed" {
			continue
		}
		cmd, err := r.mdm.Enqueue(d.UDID, map[string]any{"RequestType": "RemoveProfile", "Identifier": st.Identifier}, mdm.Meta{Source: "reconcile", Ref: ref})
		if err != nil {
			continue
		}
		st.Status, st.CommandUUID, st.Error = "removing", cmd.UUID, ""
		_ = r.store.SetProfileState(st)
		n++
	}
	return n
}

func (r *Reconciler) reconcileApps(d *store.Device, want *desired) int {
	states, _ := r.store.ListAppStates(d.UDID, 0)
	byID := map[int64]*store.AppState{}
	for _, st := range states {
		byID[st.AppID] = st
	}
	installed, _ := r.store.DeviceAppBundleIDs(d.UDID)
	n := 0
	needAppInventory := false
	for appID, intent := range want.apps {
		app, err := r.store.GetApp(appID)
		if err != nil {
			continue
		}
		st := byID[appID]
		ref := fmt.Sprintf("app:%d", appID)
		inst, present := installed[app.BundleID]
		switch intent {
		case "install":
			if present && app.BundleID != "" && (inst.IsManaged || !app.TakeManagement) {
				if st == nil || st.Status != "installed" {
					_ = r.store.SetAppState(&store.AppState{DeviceID: d.UDID, AppID: appID, BundleID: app.BundleID, Intent: intent, Status: "installed"})
				}
				continue
			}
			if st != nil {
				if r.store.HasPendingCommand(d.UDID, "InstallApplication", ref) {
					continue
				}
				if st.Status == "installing" {
					if store.Now()-st.UpdatedAt < 30*60 {
						if store.Now()-st.UpdatedAt > 120 {
							needAppInventory = true
						}
						continue
					}
				}
				if st.Status == "installed" {
					// trust the device until inventory reports the app missing
					continue
				}
				if st.Status == "failed" && st.Attempts >= MaxAppAttempts {
					continue
				}
				if st.Status == "failed" && store.Now()-st.UpdatedAt < 3600 {
					continue // back off before retrying
				}
			}
			attempts := 1
			if st != nil {
				attempts = st.Attempts + 1
			}
			if app.UseVPP || app.Kind == "vpp" {
				if err := r.ensureLicense(app, d); err != nil {
					_ = r.store.SetAppState(&store.AppState{DeviceID: d.UDID, AppID: appID, BundleID: app.BundleID, Intent: intent, Status: "failed", Error: "license: " + err.Error(), Attempts: attempts})
					continue
				}
			}
			cmdDict, err := r.mdm.InstallApplicationCommand(app, d)
			if err != nil {
				_ = r.store.SetAppState(&store.AppState{DeviceID: d.UDID, AppID: appID, BundleID: app.BundleID, Intent: intent, Status: "failed", Error: err.Error(), Attempts: attempts})
				continue
			}
			cmd, err := r.mdm.Enqueue(d.UDID, cmdDict, mdm.Meta{Source: "reconcile", Ref: ref})
			if err != nil {
				continue
			}
			_ = r.store.SetAppState(&store.AppState{DeviceID: d.UDID, AppID: appID, BundleID: app.BundleID, Intent: intent, Status: "pending", CommandUUID: cmd.UUID, Attempts: attempts})
			n++
		case "uninstall":
			if !present || app.BundleID == "" {
				if st == nil || st.Status != "removed" {
					_ = r.store.SetAppState(&store.AppState{DeviceID: d.UDID, AppID: appID, BundleID: app.BundleID, Intent: intent, Status: "removed"})
				}
				continue
			}
			if st != nil && st.Intent == "uninstall" && (r.store.HasPendingCommand(d.UDID, "RemoveApplication", ref) || (st.Status == "failed" && st.Attempts >= MaxAppAttempts)) {
				continue
			}
			attempts := 1
			if st != nil && st.Intent == "uninstall" {
				attempts = st.Attempts + 1
			}
			cmd, err := r.mdm.Enqueue(d.UDID, map[string]any{"RequestType": "RemoveApplication", "Identifier": app.BundleID}, mdm.Meta{Source: "reconcile", Ref: ref})
			if err != nil {
				continue
			}
			_ = r.store.SetAppState(&store.AppState{DeviceID: d.UDID, AppID: appID, BundleID: app.BundleID, Intent: intent, Status: "removing", CommandUUID: cmd.UUID, Attempts: attempts})
			n++
		}
	}
	for appID, st := range byID {
		if _, ok := want.apps[appID]; !ok && st.Status != "pending" {
			_ = r.store.DeleteAppState(d.UDID, appID)
		}
	}
	if needAppInventory && !r.store.HasPendingCommand(d.UDID, "InstalledApplicationList", "") {
		for _, t := range []string{"ManagedApplicationList", "InstalledApplicationList"} {
			if c, _, err := r.mdm.BuildCommand(t, d, nil); err == nil {
				if _, err := r.mdm.Enqueue(d.UDID, c, mdm.Meta{Source: "system", Ref: "app-status"}); err == nil {
					n++
				}
			}
		}
	}
	return n
}

func (r *Reconciler) reconcileDeclarations(d *store.Device) int {
	if !ddm.Supported(d) {
		return 0
	}
	tok, err := r.ddm.Token(d.UDID)
	if err != nil || tok == d.DDMToken {
		return 0
	}
	if r.store.HasPendingCommand(d.UDID, "DeclarativeManagement", "") {
		return 0
	}
	if _, err := r.mdm.Enqueue(d.UDID, map[string]any{"RequestType": "DeclarativeManagement", "Data": mdm.SyncTokens(tok)}, mdm.Meta{Source: "reconcile", Ref: "ddm"}); err != nil {
		return 0
	}
	_ = r.store.UpdateDevice(d.UDID, map[string]any{"ddm_token": tok})
	return 1
}

// VPPClientUserID is the Apps and Books user identifier Orchard registers
// for a Managed Apple Account (user-based licensing for personal devices).
func VPPClientUserID(managedAppleID string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(managedAppleID))))
	return "orchard-" + hex.EncodeToString(sum[:12])
}

// ensureLicense associates an Apps and Books license with the device, or
// with the person's Managed Apple Account on User Enrollment devices.
func (r *Reconciler) ensureLicense(app *store.App, d *store.Device) error {
	userBased := d.UserEnrollment || (d.SerialNumber == "" && d.ManagedAppleID != "")
	if userBased && d.ManagedAppleID == "" {
		return fmt.Errorf("personal device has no Managed Apple Account")
	}
	if !userBased && d.SerialNumber == "" {
		return fmt.Errorf("device serial number unknown")
	}
	adam := fmt.Sprint(app.ITunesID)
	tokens, err := r.store.ListVPPTokens()
	if err != nil || len(tokens) == 0 {
		return fmt.Errorf("no Apps and Books token configured")
	}
	assets, _ := r.store.ListVPPAssets(0)
	var lastErr error
	for _, t := range tokens {
		for _, a := range assets {
			if a.VPPTokenID != t.ID || a.AdamID != adam {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			c := vpp.New(r.vppURL, t.Token)
			var err error
			if userBased {
				uid := VPPClientUserID(d.ManagedAppleID)
				_, _ = c.CreateUsers(ctx, []vpp.User{{ClientUserID: uid, Email: d.ManagedAppleID, ManagedAppleID: d.ManagedAppleID}})
				_, err = c.AssociateUsers(ctx, adam, a.PricingParam, []string{uid})
			} else {
				_, err = c.Associate(ctx, adam, a.PricingParam, []string{d.SerialNumber})
			}
			cancel()
			if err == nil {
				return nil
			}
			lastErr = err
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no Apps and Books licenses for App Store ID %s", adam)
}
