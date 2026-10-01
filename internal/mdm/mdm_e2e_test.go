package mdm_test

import (
	"slices"
	"testing"

	"github.com/yeungalan/orchard-mdm/internal/devicesim"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"github.com/yeungalan/orchard-mdm/internal/testenv"
)

func enroll(t *testing.T, env *testenv.Env, ref string) *devicesim.Device {
	t.Helper()
	prof, err := env.MDM.EnrollmentProfile(mdm.EnrollmentOptions{Ref: ref})
	if err != nil {
		t.Fatal(err)
	}
	dev := devicesim.New("Test iPhone", "iPhone16,1")
	if err := dev.Enroll(prof); err != nil {
		t.Fatal(err)
	}
	return dev
}

func TestEnrollAndInventory(t *testing.T) {
	env := testenv.New(t)
	dev := enroll(t, env, "manual")

	d, err := env.Store.GetDevice(dev.UDID)
	if err != nil {
		t.Fatal(err)
	}
	if d.EnrollmentStatus != "enrolled" || !d.HasPushToken || len(d.UnlockToken) == 0 {
		t.Fatalf("unexpected device state: %+v", d)
	}
	if d.Topic != testenv.Topic {
		t.Fatalf("topic = %q", d.Topic)
	}
	handled, err := dev.Poll()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DeviceInformation", "SecurityInfo", "InstalledApplicationList", "ProfileList", "CertificateList"} {
		if !slices.Contains(handled, want) {
			t.Errorf("inventory command %s not delivered (got %v)", want, handled)
		}
	}
	d, _ = env.Store.GetDevice(dev.UDID)
	if d.SerialNumber != dev.Serial || d.OSVersion != "18.6" || d.BatteryLevel <= 0 || !d.Supervised || d.EncryptionCaps != 3 || !d.PasscodePresent {
		t.Fatalf("inventory not applied: %+v", d)
	}
	if d.Carrier != "NTT DOCOMO" || d.WiFiMAC == "" {
		t.Fatalf("network info missing: carrier=%q wifi=%q", d.Carrier, d.WiFiMAC)
	}
	apps, _ := env.Store.ListDeviceApps(dev.UDID)
	if len(apps) != 3 {
		t.Fatalf("apps = %d", len(apps))
	}
	samples, _ := env.Store.ListTelemetry(dev.UDID, 0, 100)
	var mdmSample, serverSample bool
	for _, s := range samples {
		if s.Source == "mdm" && s.Battery > 0 {
			mdmSample = true
		}
		if s.Source == "server" && s.IP != "" {
			serverSample = true
		}
	}
	if !mdmSample || !serverSample {
		t.Fatalf("telemetry samples missing: %+v", samples)
	}
	cmds, _, _ := env.Store.ListCommands(store.CommandFilter{DeviceID: dev.UDID, Status: "pending"})
	if len(cmds) != 0 {
		t.Fatalf("%d commands still pending", len(cmds))
	}
}

func TestEraseIsRefused(t *testing.T) {
	env := testenv.New(t)
	dev := enroll(t, env, "manual")
	if _, err := env.MDM.Enqueue(dev.UDID, map[string]any{"RequestType": "EraseDevice"}, mdm.Meta{}); err == nil {
		t.Fatal("EraseDevice was queued")
	}
	if _, err := mdm.ParseCustomCommand(`{"RequestType":"EraseDevice","PIN":"123456"}`); err == nil {
		t.Fatal("custom EraseDevice accepted")
	}
	if _, err := mdm.ParseCustomCommand(`<dict><key>RequestType</key><string>DeleteUser</string></dict>`); err == nil {
		t.Fatal("custom DeleteUser accepted")
	}
	res := env.MDM.SendCatalogCommand("EraseDevice", []string{dev.UDID}, nil, mdm.Meta{Source: "manual"})
	if res[0].Error == "" {
		t.Fatal("catalog accepted EraseDevice")
	}
	if mdm.AccessRights&8 != 0 {
		t.Fatal("enrollment profile grants erase right")
	}
}

func TestLostModeLocationAndActions(t *testing.T) {
	env := testenv.New(t)
	dev := enroll(t, env, "manual")
	if _, err := dev.Poll(); err != nil {
		t.Fatal(err)
	}
	res := env.MDM.SendCatalogCommand("EnableLostMode", []string{dev.UDID}, mdm.Params{"Message": "Please return", "PhoneNumber": "+81 3 0000 0000"}, mdm.Meta{Source: "manual"})
	if res[0].Error != "" {
		t.Fatal(res[0].Error)
	}
	if _, err := dev.Poll(); err != nil {
		t.Fatal(err)
	}
	d, _ := env.Store.GetDevice(dev.UDID)
	if !d.LostMode || d.Location == nil {
		t.Fatalf("lost mode/location not recorded: lost=%v loc=%v", d.LostMode, d.Location)
	}
	locs, _ := env.Store.ListLocations(dev.UDID, 0, 10)
	if len(locs) != 1 || locs[0].Source != "mdm" {
		t.Fatalf("locations = %+v", locs)
	}
	res = env.MDM.SendCatalogCommand("Settings.DeviceName", []string{dev.UDID}, mdm.Params{"DeviceName": "Kiosk {{device.serial}}"}, mdm.Meta{Source: "manual"})
	if res[0].Error != "" {
		t.Fatal(res[0].Error)
	}
	if _, err := dev.Poll(); err != nil {
		t.Fatal(err)
	}
	d, _ = env.Store.GetDevice(dev.UDID)
	if d.DeviceName != "Kiosk "+dev.Serial {
		t.Fatalf("device name = %q", d.DeviceName)
	}
	res = env.MDM.SendCatalogCommand("ClearPasscode", []string{dev.UDID}, nil, mdm.Meta{Source: "manual"})
	if res[0].Error != "" {
		t.Fatal(res[0].Error)
	}
}

func TestUnenrollChecksOut(t *testing.T) {
	env := testenv.New(t)
	dev := enroll(t, env, "manual")
	if _, err := dev.Poll(); err != nil {
		t.Fatal(err)
	}
	if _, err := env.MDM.Unenroll(dev.UDID, mdm.Meta{Source: "manual"}); err != nil {
		t.Fatal(err)
	}
	if _, err := dev.Poll(); err != nil {
		t.Fatal(err)
	}
	d, _ := env.Store.GetDevice(dev.UDID)
	if d.EnrollmentStatus != "unenrolled" {
		t.Fatalf("status = %s", d.EnrollmentStatus)
	}
}

func TestBadChallengeRejected(t *testing.T) {
	env := testenv.New(t)
	prof, err := env.MDM.EnrollmentProfile(mdm.EnrollmentOptions{Ref: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	dev := devicesim.New("A", "iPhone16,1")
	if err := dev.Enroll(prof); err != nil {
		t.Fatal(err)
	}
	// reusing the same profile (and therefore the same one-time challenge) must fail
	dev2 := devicesim.New("B", "iPhone16,1")
	if err := dev2.Enroll(prof); err == nil {
		t.Fatal("challenge was accepted twice")
	}
}
