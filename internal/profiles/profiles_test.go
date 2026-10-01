package profiles

import (
	"strings"
	"testing"

	"howett.net/plist"
)

func TestBuildWiFiAndRestrictions(t *testing.T) {
	res, err := Build(Meta{Name: "Office", Identifier: "com.example.office"}, []PayloadInput{
		{Type: "com.apple.wifi.managed", Values: map[string]any{"SSID_STR": "Corp", "EncryptionType": "WPA2", "Password": "secret", "ProxyType": "None",
			"EAPClientConfiguration.AcceptEAPTypes": []any{"25"}, "EAPClientConfiguration.UserName": "{{user.email}}"}},
		{Type: "com.apple.applicationaccess", Values: map[string]any{"allowCamera": false, "allowSafari": true, "ratingApps": float64(300), "blockedAppBundleIDs": "com.a\ncom.b"}},
		{Type: "com.apple.mobiledevice.passwordpolicy", Values: map[string]any{"minLength": float64(6), "maxInactivity": float64(5)}},
		{Type: "com.apple.homescreenlayout", Values: map[string]any{"Dock": "com.apple.mobilephone", "Pages": "com.apple.mobilesafari\nWork: com.microsoft.Office.Outlook, com.slack\n\ncom.apple.camera"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if _, err := plist.Unmarshal(res.XML, &top); err != nil {
		t.Fatal(err)
	}
	content := top["PayloadContent"].([]any)
	wifi := content[0].(map[string]any)
	if wifi["SSID_STR"] != "Corp" || wifi["Password"] != "secret" {
		t.Fatalf("wifi = %v", wifi)
	}
	eap := wifi["EAPClientConfiguration"].(map[string]any)
	if eap["UserName"] != "{{user.email}}" || eap["AcceptEAPTypes"].([]any)[0].(uint64) != 25 {
		t.Fatalf("eap = %v", eap)
	}
	if _, ok := wifi["ProxyServer"]; ok {
		t.Fatal("hidden proxy field emitted")
	}
	r := content[1].(map[string]any)
	if r["allowCamera"] != false || r["ratingApps"].(uint64) != 300 {
		t.Fatalf("restrictions = %v", r)
	}
	if _, ok := r["allowSafari"]; ok {
		t.Fatal("default restriction value emitted")
	}
	if len(r["blockedAppBundleIDs"].([]any)) != 2 {
		t.Fatal("blocked apps")
	}
	pass := content[2].(map[string]any)
	if pass["minLength"].(uint64) != 6 || pass["forcePIN"] != true {
		t.Fatalf("passcode = %v", pass)
	}
	if _, ok := pass["maxFailedAttempts"]; ok {
		t.Fatal("maxFailedAttempts must never be emitted")
	}
	hs := content[3].(map[string]any)
	if len(hs["Pages"].([]any)) != 2 || len(hs["Dock"].([]any)) != 1 {
		t.Fatalf("home screen = %v", hs)
	}
}

func TestUploadStripsWipeKey(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict>
<key>PayloadType</key><string>Configuration</string><key>PayloadIdentifier</key><string>x.y</string>
<key>PayloadUUID</key><string>1</string><key>PayloadVersion</key><integer>1</integer>
<key>PayloadContent</key><array><dict><key>PayloadType</key><string>com.apple.mobiledevice.passwordpolicy</string>
<key>maxFailedAttempts</key><integer>4</integer><key>forcePIN</key><true/></dict></array></dict></plist>`
	p, err := Parse([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Warnings) != 1 || strings.Contains(string(p.XML), "maxFailedAttempts") {
		t.Fatalf("wipe key not stripped: %v", p.Warnings)
	}
}

func TestRejectEnrollmentProfileUpload(t *testing.T) {
	xml := `<plist version="1.0"><dict><key>PayloadType</key><string>Configuration</string><key>PayloadIdentifier</key><string>a</string>
<key>PayloadContent</key><array><dict><key>PayloadType</key><string>com.apple.mdm</string></dict></array></dict></plist>`
	if _, err := Parse([]byte(xml)); err == nil {
		t.Fatal("accepted MDM payload")
	}
}

func TestVPNOnDemand(t *testing.T) {
	res, err := Build(Meta{Name: "VPN", Identifier: "com.example.vpn"}, []PayloadInput{
		{Type: "com.apple.vpn.managed", Values: map[string]any{"UserDefinedName": "Corp VPN", "VPNType": "IKEv2", "IKEv2.RemoteAddress": "vpn.example.com",
			"IKEv2.AuthenticationMethod": "SharedSecret", "IKEv2.SharedSecret": "s3cret", "_connect_on_demand": true, "IPSec.RemoteAddress": "ignored"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	plist.Unmarshal(res.XML, &top)
	vpn := top["PayloadContent"].([]any)[0].(map[string]any)
	ike := vpn["IKEv2"].(map[string]any)
	if ike["RemoteAddress"] != "vpn.example.com" || ike["OnDemandEnabled"].(uint64) != 1 {
		t.Fatalf("ikev2 = %v", ike)
	}
	if _, ok := vpn["IPSec"]; ok {
		t.Fatal("hidden IPSec section emitted")
	}
}
