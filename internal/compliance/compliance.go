// Package compliance evaluates devices against compliance policies.
package compliance

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/reconcile"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

// Rules are the checks of a policy. Zero values disable a check.
type Rules struct {
	MinOSVersion             string   `json:"min_os_version,omitempty"`
	MaxOSVersion             string   `json:"max_os_version,omitempty"`
	RequirePasscode          bool     `json:"require_passcode,omitempty"`
	RequirePasscodeCompliant bool     `json:"require_passcode_compliant,omitempty"`
	RequireEncryption        bool     `json:"require_encryption,omitempty"`
	RequireSupervised        bool     `json:"require_supervised,omitempty"`
	MaxInactiveDays          int      `json:"max_inactive_days,omitempty"`
	MinFreeStorageGB         float64  `json:"min_free_storage_gb,omitempty"`
	BlockedApps              []string `json:"blocked_apps,omitempty"`
	RequiredApps             []string `json:"required_apps,omitempty"`
	RequireProfilesInstalled bool     `json:"require_profiles_installed,omitempty"`
	BlockRoaming             bool     `json:"block_roaming,omitempty"`
	BlockActivationLock      bool     `json:"block_activation_lock,omitempty"`
	RequireRecentCheckinHrs  int      `json:"require_recent_inventory_hours,omitempty"`
}

// Actions run when a device stays non-compliant. Orchard never wipes or
// retires devices automatically; the strongest action is a remote lock.
type Actions struct {
	LockAfterHours int    `json:"lock_after_hours,omitempty"`
	LockMessage    string `json:"lock_message,omitempty"`
}

// PolicyResult is the outcome of one policy.
type PolicyResult struct {
	PolicyID   int64    `json:"policy_id"`
	PolicyName string   `json:"policy_name"`
	Compliant  bool     `json:"compliant"`
	Reasons    []string `json:"reasons"`
}

// Result is the evaluation of a device.
type Result struct {
	State    string         `json:"state"` // compliant|noncompliant|grace|unknown
	Reasons  []string       `json:"reasons"`
	Policies []PolicyResult `json:"policies"`
}

// Engine evaluates compliance.
type Engine struct {
	store *store.Store
	mdm   *mdm.Service
	bus   *events.Bus
	log   *slog.Logger
	mu    sync.Mutex
}

// New creates an engine.
func New(s *store.Store, m *mdm.Service, log *slog.Logger) *Engine {
	return &Engine{store: s, mdm: m, bus: m.Bus(), log: log}
}

// Subscribe re-evaluates devices when their inventory changes.
func (e *Engine) Subscribe(bus *events.Bus) {
	bus.Subscribe(func(ev events.Event) { e.EvaluateDevice(ev.DeviceID) }, events.DeviceInventory, events.DDMStatus)
	bus.Subscribe(func(events.Event) { e.EvaluateAll() }, events.AssignmentsChanged, events.GroupsChanged)
}

func (e *Engine) policiesFor(udid string) ([]*store.CompliancePolicy, error) {
	assigns, err := e.store.DeviceAssignments(udid)
	if err != nil {
		return nil, err
	}
	include, exclude := map[int64]bool{}, map[int64]bool{}
	for _, a := range assigns {
		if a.ItemType != "compliance" {
			continue
		}
		if a.Intent == "exclude" {
			exclude[a.ItemID] = true
		} else {
			include[a.ItemID] = true
		}
	}
	var out []*store.CompliancePolicy
	for id := range include {
		if exclude[id] {
			continue
		}
		p, err := e.store.GetCompliancePolicy(id)
		if err == nil && p.Enabled {
			out = append(out, p)
		}
	}
	return out, nil
}

func matchBundle(pattern, bundle string) bool {
	if ok, _ := path.Match(pattern, bundle); ok {
		return true
	}
	return strings.EqualFold(pattern, bundle)
}

// Check evaluates one policy against a device.
func (e *Engine) Check(d *store.Device, p *store.CompliancePolicy) PolicyResult {
	res := PolicyResult{PolicyID: p.ID, PolicyName: p.Name, Compliant: true}
	var r Rules
	_ = json.Unmarshal(p.Rules, &r)
	fail := func(format string, args ...any) {
		res.Compliant = false
		res.Reasons = append(res.Reasons, fmt.Sprintf(format, args...))
	}
	if r.MinOSVersion != "" && d.OSVersion != "" && reconcile.CompareVersions(d.OSVersion, r.MinOSVersion) < 0 {
		fail("OS %s is older than the minimum %s", d.OSVersion, r.MinOSVersion)
	}
	if r.MaxOSVersion != "" && d.OSVersion != "" && reconcile.CompareVersions(d.OSVersion, r.MaxOSVersion) > 0 {
		fail("OS %s is newer than the maximum %s", d.OSVersion, r.MaxOSVersion)
	}
	if r.RequirePasscode && !d.PasscodePresent {
		fail("No passcode is set")
	}
	if r.RequirePasscodeCompliant && !d.PasscodeCompliant {
		fail("Passcode does not meet the passcode policy")
	}
	if r.RequireEncryption && !(d.EncryptionCaps&2 != 0 && d.PasscodePresent) {
		fail("Data protection (file-level encryption) is not active")
	}
	if r.RequireSupervised && !d.Supervised {
		fail("Device is not supervised")
	}
	if r.MaxInactiveDays > 0 && d.LastSeen > 0 && time.Since(time.Unix(d.LastSeen, 0)) > time.Duration(r.MaxInactiveDays)*24*time.Hour {
		fail("Device has not checked in for more than %d days", r.MaxInactiveDays)
	}
	if r.RequireRecentCheckinHrs > 0 && (d.LastInventory == 0 || time.Since(time.Unix(d.LastInventory, 0)) > time.Duration(r.RequireRecentCheckinHrs)*time.Hour) {
		fail("Inventory is older than %d hours", r.RequireRecentCheckinHrs)
	}
	if r.MinFreeStorageGB > 0 && d.CapacityGB > 0 && d.AvailableGB < r.MinFreeStorageGB {
		fail("Only %.1f GB free (minimum %.1f GB)", d.AvailableGB, r.MinFreeStorageGB)
	}
	if r.BlockRoaming && d.Roaming {
		fail("Device is roaming")
	}
	if r.BlockActivationLock && d.ActivationLock {
		fail("Activation Lock is enabled")
	}
	if len(r.BlockedApps) > 0 || len(r.RequiredApps) > 0 {
		apps, _ := e.store.DeviceAppBundleIDs(d.UDID)
		for _, pat := range r.BlockedApps {
			for b := range apps {
				if matchBundle(pat, b) {
					fail("Blocked app installed: %s", apps[b].Name)
				}
			}
		}
		for _, req := range r.RequiredApps {
			if _, ok := apps[req]; !ok {
				fail("Required app missing: %s", req)
			}
		}
	}
	if r.RequireProfilesInstalled {
		states, _ := e.store.ListProfileStates(d.UDID, 0)
		for _, st := range states {
			if st.Status == "failed" || st.Status == "missing" {
				fail("Profile %s is not installed (%s)", st.ProfileName, st.Status)
			}
		}
	}
	return res
}

// Evaluate computes the compliance of a device without saving it.
func (e *Engine) Evaluate(d *store.Device) Result {
	res := Result{State: "compliant", Reasons: []string{}, Policies: []PolicyResult{}}
	policies, _ := e.policiesFor(d.UDID)
	if d.LastInventory == 0 && d.LastSeen == 0 {
		res.State = "unknown"
		return res
	}
	if len(policies) == 0 {
		if e.mdm.Setting("compliance_default") == "noncompliant" {
			res.State = "noncompliant"
			res.Reasons = append(res.Reasons, "No compliance policy is assigned")
		}
		return res
	}
	for _, p := range policies {
		pr := e.Check(d, p)
		res.Policies = append(res.Policies, pr)
		if !pr.Compliant {
			res.State = "noncompliant"
			for _, reason := range pr.Reasons {
				res.Reasons = append(res.Reasons, p.Name+": "+reason)
			}
		}
	}
	if res.State == "noncompliant" {
		grace := 0
		for _, pr := range res.Policies {
			if pr.Compliant {
				continue
			}
			for _, p := range policies {
				if p.ID == pr.PolicyID && (grace == 0 || p.GraceHours < grace) {
					grace = p.GraceHours
				}
			}
		}
		since := d.NoncompliantSince
		if since == 0 {
			since = store.Now()
		}
		if grace > 0 && store.Now()-since < int64(grace)*3600 {
			res.State = "grace"
		}
	}
	return res
}

// EvaluateDevice evaluates and stores a device's compliance, running actions.
func (e *Engine) EvaluateDevice(udid string) *Result {
	e.mu.Lock()
	defer e.mu.Unlock()
	d, err := e.store.GetDevice(udid)
	if err != nil || d.EnrollmentStatus != "enrolled" {
		return nil
	}
	res := e.Evaluate(d)
	fields := map[string]any{"compliance": res.State, "compliance_reasons": res.Reasons}
	failing := res.State == "noncompliant" || res.State == "grace"
	switch {
	case failing && d.NoncompliantSince == 0:
		fields["noncompliant_since"] = store.Now()
	case !failing:
		fields["noncompliant_since"] = 0
	}
	_ = e.store.UpdateDevice(udid, fields)
	if res.State != d.Compliance {
		level := "info"
		if failing {
			level = "warn"
		}
		_ = e.store.InsertEvent(&store.Event{DeviceID: udid, Type: "compliance.changed", Level: level,
			Message: fmt.Sprintf("Compliance changed from %s to %s", d.Compliance, res.State), Details: strings.Join(res.Reasons, "\n")})
		e.bus.Publish(events.ComplianceChanged, udid, map[string]any{"previous": d.Compliance, "state": res.State, "reasons": res.Reasons,
			"device_name": d.DeviceName, "serial_number": d.SerialNumber})
	}
	if res.State == "noncompliant" {
		e.runActions(d, res)
	}
	return &res
}

func (e *Engine) runActions(d *store.Device, res Result) {
	since := d.NoncompliantSince
	if since == 0 {
		return
	}
	for _, pr := range res.Policies {
		if pr.Compliant {
			continue
		}
		p, err := e.store.GetCompliancePolicy(pr.PolicyID)
		if err != nil {
			continue
		}
		var a Actions
		_ = json.Unmarshal(p.Actions, &a)
		if a.LockAfterHours <= 0 {
			continue
		}
		due := since + int64(p.GraceHours+a.LockAfterHours)*3600
		if store.Now() < due {
			continue
		}
		evs, _ := e.store.ListEvents(d.UDID, "compliance.lock", 20, 0)
		already := false
		for _, ev := range evs {
			if ev.TS >= since {
				already = true
			}
		}
		if already {
			continue
		}
		msg := a.LockMessage
		if msg == "" {
			msg = "This device does not meet " + e.mdm.Setting(mdm.SettingOrgName) + " compliance requirements. Contact IT."
		}
		if _, err := e.mdm.EnqueueAndPush(d.UDID, map[string]any{"RequestType": "DeviceLock", "Message": msg}, mdm.Meta{Source: "compliance", Ref: fmt.Sprintf("compliance:%d", p.ID)}); err == nil {
			_ = e.store.InsertEvent(&store.Event{DeviceID: d.UDID, Type: "compliance.lock", Level: "warn", Message: "Remote lock sent by compliance policy " + p.Name})
		}
	}
}

// EvaluateAll evaluates every enrolled device.
func (e *Engine) EvaluateAll() {
	udids, err := e.store.EnrolledUDIDs()
	if err != nil {
		return
	}
	for _, u := range udids {
		e.EvaluateDevice(u)
	}
}
