package reconcile_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"testing"

	"github.com/yeungalan/orchard-mdm/internal/compliance"
	"github.com/yeungalan/orchard-mdm/internal/ddm"
	"github.com/yeungalan/orchard-mdm/internal/devicesim"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/profiles"
	"github.com/yeungalan/orchard-mdm/internal/reconcile"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"github.com/yeungalan/orchard-mdm/internal/testenv"
)

func setup(t *testing.T) (*testenv.Env, *reconcile.Reconciler, *devicesim.Device) {
	env := testenv.New(t)
	rec := reconcile.New(env.Store, env.MDM, slog.Default(), "")
	prof, err := env.MDM.EnrollmentProfile(mdm.EnrollmentOptions{Ref: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	dev := devicesim.New("Ops iPad", "iPad14,1")
	if err := dev.Enroll(prof); err != nil {
		t.Fatal(err)
	}
	if _, err := dev.Poll(); err != nil {
		t.Fatal(err)
	}
	return env, rec, dev
}

func TestProfileAppAndDeclarationAssignment(t *testing.T) {
	env, rec, dev := setup(t)
	st := env.Store

	res, err := profiles.Build(profiles.Meta{Name: "Wi-Fi", Identifier: "com.example.wifi"}, []profiles.PayloadInput{
		{Type: "com.apple.wifi.managed", Values: map[string]any{"SSID_STR": "Corp-{{device.serial}}", "EncryptionType": "WPA2", "Password": "x"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := &store.Profile{Name: "Wi-Fi", Identifier: "com.example.wifi", Source: "builder", Raw: res.XML, Removable: true, Scope: "System", PayloadTypes: res.PayloadTypes}
	if err := st.CreateProfile(p); err != nil {
		t.Fatal(err)
	}
	app := &store.App{Kind: "appstore", Name: "Slack", BundleID: "store.app.618783545", ITunesID: 618783545, RemoveOnUnenroll: true, TakeManagement: true,
		Config: json.RawMessage(`{"serial":"{{device.serial}}","n":3}`)}
	if err := st.CreateApp(app); err != nil {
		t.Fatal(err)
	}
	decl := &store.Declaration{Identifier: "com.example.passcode", Type: "com.apple.configuration.passcode.settings", Name: "Passcode",
		Payload: json.RawMessage(`{"RequirePasscode":true,"MinimumLength":6}`)}
	decl.ServerToken = ddm.TokenFor(decl.Type, decl.Payload)
	if err := st.CreateDeclaration(decl); err != nil {
		t.Fatal(err)
	}
	g := &store.Group{Name: "iPads", Kind: "dynamic", Rules: &store.GroupRules{Match: "all", Rules: []store.GroupRule{{Field: "model_name", Op: "eq", Value: "iPad"}}}}
	if err := st.CreateGroup(g); err != nil {
		t.Fatal(err)
	}
	for _, a := range []*store.Assignment{
		{ItemType: "profile", ItemID: p.ID, GroupID: g.ID, Intent: "install"},
		{ItemType: "app", ItemID: app.ID, GroupID: g.ID, Intent: "install"},
		{ItemType: "declaration", ItemID: decl.ID, GroupID: g.ID, Intent: "install"},
	} {
		if err := st.UpsertAssignment(a); err != nil {
			t.Fatal(err)
		}
	}
	rec.ReconcileAll(context.Background())
	members, _ := st.GroupMemberIDs(g.ID)
	if !slices.Contains(members, dev.UDID) {
		t.Fatalf("dynamic group did not pick up device: %v", members)
	}
	handled, err := dev.Poll()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"InstallProfile", "InstallApplication", "DeclarativeManagement"} {
		if !slices.Contains(handled, want) {
			t.Fatalf("%s not delivered: %v", want, handled)
		}
	}
	if !dev.HasProfile("com.example.wifi") || !dev.HasApp("store.app.618783545") {
		t.Fatal("profile/app not installed on device")
	}
	fetched := dev.DDMFetched()
	if !slices.Contains(fetched, "declaration/configuration/com.example.passcode") || !slices.Contains(fetched, "declaration/activation/"+ddm.DefaultActivationID) {
		t.Fatalf("declarations not fetched: %v", fetched)
	}
	pst, _ := st.ListProfileStates(dev.UDID, 0)
	if len(pst) != 1 || pst[0].Status != "installed" {
		t.Fatalf("profile state = %+v", pst)
	}
	ast, _ := st.ListAppStates(dev.UDID, 0)
	if len(ast) != 1 || ast[0].Status != "installed" {
		t.Fatalf("app state = %+v", ast[0])
	}
	statuses, _ := st.ListDeclarationStatus(dev.UDID)
	if len(statuses) == 0 {
		t.Fatal("no declaration status recorded")
	}
	d, _ := st.GetDevice(dev.UDID)
	if d.BatteryHealth != "normal" {
		t.Fatalf("battery health from DDM = %q", d.BatteryHealth)
	}
	// nothing more to do on a second pass
	if n := rec.ReconcileDevice(dev.UDID); n != 0 {
		t.Fatalf("second reconcile queued %d commands", n)
	}
	// unassign the profile: it must be removed
	as, _ := st.ListAssignments("profile", p.ID, 0)
	st.DeleteAssignment(as[0].ID)
	rec.ReconcileAll(context.Background())
	if _, err := dev.Poll(); err != nil {
		t.Fatal(err)
	}
	if dev.HasProfile("com.example.wifi") {
		t.Fatal("profile not removed after unassignment")
	}
	pst, _ = st.ListProfileStates(dev.UDID, 0)
	if len(pst) != 0 {
		t.Fatalf("profile state not cleaned: %+v", pst)
	}
}

func TestCompliance(t *testing.T) {
	env, _, dev := setup(t)
	st := env.Store
	eng := compliance.New(st, env.MDM, slog.Default())
	p := &store.CompliancePolicy{Name: "Baseline", Enabled: true, Rules: json.RawMessage(`{"min_os_version":"18.0","require_passcode":true,"blocked_apps":["com.google.*"]}`), Actions: json.RawMessage(`{}`)}
	st.CreateCompliancePolicy(p)
	st.UpsertAssignment(&store.Assignment{ItemType: "compliance", ItemID: p.ID, GroupID: st.AllDevicesGroupID(), Intent: "install"})
	st.AddGroupMembers(st.AllDevicesGroupID(), []string{dev.UDID})
	res := eng.EvaluateDevice(dev.UDID)
	if res.State != "noncompliant" || len(res.Reasons) != 1 {
		t.Fatalf("result = %+v", res)
	}
	p.Rules = json.RawMessage(`{"min_os_version":"18.0","require_passcode":true}`)
	st.UpdateCompliancePolicy(p)
	res = eng.EvaluateDevice(dev.UDID)
	if res.State != "compliant" {
		t.Fatalf("result = %+v", res)
	}
	d, _ := st.GetDevice(dev.UDID)
	if d.Compliance != "compliant" {
		t.Fatalf("stored = %s", d.Compliance)
	}
}

func TestDynamicRules(t *testing.T) {
	d := &store.Device{OSVersion: "17.5.1", ProductName: "iPhone15,2", Tags: []string{"Sales", "Tokyo"}, Supervised: true}
	cases := []struct {
		r    store.GroupRule
		want bool
	}{
		{store.GroupRule{Field: "os_version", Op: "gte", Value: "17.5"}, true},
		{store.GroupRule{Field: "os_version", Op: "lt", Value: "17.10"}, true},
		{store.GroupRule{Field: "tag", Op: "eq", Value: "tokyo"}, true},
		{store.GroupRule{Field: "tag", Op: "neq", Value: "Sales"}, false},
		{store.GroupRule{Field: "model", Op: "prefix", Value: "iPhone15"}, true},
		{store.GroupRule{Field: "supervised", Op: "eq", Value: "true"}, true},
	}
	for _, c := range cases {
		if got := reconcile.MatchRule(d, c.r, nil); got != c.want {
			t.Errorf("%+v = %v, want %v", c.r, got, c.want)
		}
	}
}
