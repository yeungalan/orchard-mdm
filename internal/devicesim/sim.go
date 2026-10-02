// Package devicesim simulates an iOS device talking to an MDM server. It
// performs a real SCEP enrollment, signs every request with Mdm-Signature and
// answers commands with plausible data. It is used by the end-to-end tests and
// the orchard-sim demo tool.
package devicesim

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	mrand "math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/smallstep/pkcs7"
	"github.com/smallstep/scep"
	"github.com/smallstep/scep/x509util"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"howett.net/plist"
)

// App is an installed app.
type App struct {
	Identifier, Name, Version string
	Managed                   bool
}

// Device is a simulated iPhone/iPad.
type Device struct {
	UDID, Serial, Name, ProductName, ModelName, OSVersion, BuildVersion string
	IMEI, Phone, Carrier, WiFiMAC                                       string
	Supervised, PasscodePresent                                         bool
	Battery, CapacityGB, AvailableGB                                    float64
	Latitude, Longitude                                                 float64

	HTTP *http.Client

	mu          sync.Mutex
	key         *rsa.PrivateKey
	cert        *x509.Certificate
	checkInURL  string
	serverURL   string
	topic       string
	mdmIdent    string
	pushToken   []byte
	pushMagic   string
	enrolled    bool
	lostMode    bool
	profiles    map[string]map[string]any
	apps        map[string]*App
	ddmToken    string
	ddmFetched  []string
	Log         []string
	signMessage bool

	// UserEnrollment is set when the device enrolled through account-driven
	// User Enrollment (BYOD); it then identifies itself with an EnrollmentID
	// and hides device identifiers.
	UserEnrollment bool
	EnrollmentMode string
	ManagedAppleID string

	// StoreApps maps App Store IDs to the apps installed for them (name and
	// bundle ID); unknown IDs install as "store.app.<id>".
	StoreApps map[string]App
}

func (d *Device) idKey() string {
	if d.UserEnrollment {
		return "EnrollmentID"
	}
	return "UDID"
}

// New returns a device with random identifiers.
func New(name, product string) *Device {
	r := mrand.New(mrand.NewSource(time.Now().UnixNano()))
	udid := fmt.Sprintf("%08X-%016X", r.Uint32(), r.Uint64())
	serial := randomSerial(r)
	d := &Device{
		UDID: udid, Serial: serial, Name: name, ProductName: product, ModelName: modelName(product), OSVersion: "18.6", BuildVersion: "22G86",
		IMEI: fmt.Sprintf("35%013d", r.Int63n(1e13)), Phone: fmt.Sprintf("+81 90-%04d-%04d", r.Intn(10000), r.Intn(10000)), Carrier: "NTT DOCOMO",
		WiFiMAC: randomMAC(r), Supervised: true, PasscodePresent: true, Battery: 0.5 + r.Float64()/2, CapacityGB: 128, AvailableGB: 40 + r.Float64()*60,
		Latitude: 35.6812 + (r.Float64()-0.5)/50, Longitude: 139.7671 + (r.Float64()-0.5)/50,
		HTTP:     http.DefaultClient,
		profiles: map[string]map[string]any{},
		apps: map[string]*App{
			"com.apple.mobilesafari": {Identifier: "com.apple.mobilesafari", Name: "Safari", Version: "18.6"},
			"com.apple.mobilemail":   {Identifier: "com.apple.mobilemail", Name: "Mail", Version: "18.6"},
			"com.google.chrome.ios":  {Identifier: "com.google.chrome.ios", Name: "Chrome", Version: "139.0"},
		},
	}
	if strings.HasPrefix(product, "iPad") {
		d.IMEI, d.Phone, d.Carrier = "", "", ""
	}
	return d
}

func modelName(product string) string {
	if strings.HasPrefix(product, "iPad") {
		return "iPad"
	}
	return "iPhone"
}

func randomSerial(r *mrand.Rand) string {
	const chars = "ABCDEFGHJKLMNPQRSTUVWXYZ0123456789"
	b := make([]byte, 10)
	for i := range b {
		b[i] = chars[r.Intn(len(chars))]
	}
	return string(b)
}

func randomMAC(r *mrand.Rand) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", r.Intn(256)&0xfe, r.Intn(256), r.Intn(256), r.Intn(256), r.Intn(256), r.Intn(256))
}

func (d *Device) logf(format string, args ...any) {
	d.mu.Lock()
	d.Log = append(d.Log, fmt.Sprintf(format, args...))
	d.mu.Unlock()
}

// Enrolled reports whether the device completed enrollment.
func (d *Device) Enrolled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.enrolled
}

// LostMode reports whether Lost Mode is on.
func (d *Device) LostMode() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lostMode
}

// HasProfile reports whether a profile identifier is installed.
func (d *Device) HasProfile(ident string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.profiles[ident]
	return ok
}

// HasApp reports whether an app is installed.
func (d *Device) HasApp(bundle string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.apps[bundle]
	return ok
}

// DDMFetched returns the declaration paths the device downloaded.
func (d *Device) DDMFetched() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.ddmFetched...)
}

// Cert returns the identity certificate.
func (d *Device) Cert() *x509.Certificate { return d.cert }

// Enroll installs an enrollment profile: SCEP, then Authenticate and TokenUpdate.
func (d *Device) Enroll(profile []byte) error {
	var p map[string]any
	if _, err := plist.Unmarshal(pki.UnwrapSigned(profile), &p); err != nil {
		return fmt.Errorf("parse profile: %w", err)
	}
	content, _ := p["PayloadContent"].([]any)
	var scepPayload, mdmPayload map[string]any
	for _, c := range content {
		m, _ := c.(map[string]any)
		switch m["PayloadType"] {
		case "com.apple.security.scep":
			scepPayload, _ = m["PayloadContent"].(map[string]any)
		case "com.apple.mdm":
			mdmPayload = m
		}
	}
	if scepPayload == nil || mdmPayload == nil {
		return errors.New("profile lacks SCEP or MDM payload")
	}
	if ar, ok := mdmPayload["AccessRights"].(uint64); ok && ar&8 != 0 {
		return errors.New("profile grants the device erase right")
	}
	d.EnrollmentMode, _ = mdmPayload["EnrollmentMode"].(string)
	d.ManagedAppleID, _ = mdmPayload["AssignedManagedAppleID"].(string)
	if d.EnrollmentMode != "" && d.ManagedAppleID == "" {
		return errors.New("account-driven profile lacks AssignedManagedAppleID")
	}
	if d.EnrollmentMode == "BYOD" {
		// iOS cancels a User Enrollment whose MDM payload declares AccessRights
		if _, ok := mdmPayload["AccessRights"]; ok {
			return errors.New("User Enrollment profile must not contain AccessRights")
		}
		d.UserEnrollment = true
		d.UDID = "UE-" + strings.ToUpper(pki.RandomToken(12))
		d.IMEI, d.Phone, d.Serial = "", "", ""
		d.Supervised = false // user-enrolled devices are never supervised
	}
	d.checkInURL, _ = mdmPayload["CheckInURL"].(string)
	d.serverURL, _ = mdmPayload["ServerURL"].(string)
	d.topic, _ = mdmPayload["Topic"].(string)
	d.signMessage, _ = mdmPayload["SignMessage"].(bool)
	d.mdmIdent, _ = p["PayloadIdentifier"].(string)
	if err := d.scepEnroll(scepPayload); err != nil {
		return fmt.Errorf("scep: %w", err)
	}
	auth := map[string]any{
		"MessageType": "Authenticate", d.idKey(): d.UDID, "Topic": d.topic, "DeviceName": d.Name,
		"ProductName": d.ProductName, "Model": "MTP03J/A", "ModelName": d.ModelName, "OSVersion": d.OSVersion, "BuildVersion": d.BuildVersion,
	}
	if !d.UserEnrollment {
		auth["SerialNumber"], auth["IMEI"] = d.Serial, d.IMEI
	}
	if _, err := d.checkin(auth); err != nil {
		return fmt.Errorf("authenticate: %w", err)
	}
	d.pushToken = make([]byte, 32)
	_, _ = rand.Read(d.pushToken)
	d.pushMagic = pki.NewUUID()
	unlock := make([]byte, 64)
	_, _ = rand.Read(unlock)
	tu := map[string]any{
		"MessageType": "TokenUpdate", d.idKey(): d.UDID, "Topic": d.topic, "Token": d.pushToken, "PushMagic": d.pushMagic, "UnlockToken": unlock,
		"AwaitingConfiguration": false,
	}
	if d.UserEnrollment {
		delete(tu, "UnlockToken") // no unlock token escrow in User Enrollment
	}
	if _, err := d.checkin(tu); err != nil {
		return fmt.Errorf("token update: %w", err)
	}
	d.mu.Lock()
	d.enrolled = true
	d.profiles[d.mdmIdent] = map[string]any{"PayloadIdentifier": d.mdmIdent, "PayloadDisplayName": p["PayloadDisplayName"], "PayloadUUID": p["PayloadUUID"],
		"PayloadVersion": 1, "IsManaged": true, "PayloadContent": []any{map[string]any{"PayloadType": "com.apple.mdm"}, map[string]any{"PayloadType": "com.apple.security.scep"}}}
	d.mu.Unlock()
	return nil
}

func (d *Device) scepEnroll(sp map[string]any) error {
	url, _ := sp["URL"].(string)
	challenge, _ := sp["Challenge"].(string)
	resp, err := d.HTTP.Get(url + "?operation=GetCACert")
	if err != nil {
		return err
	}
	caDER, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		return fmt.Errorf("GetCACert: %w", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	cn := "Orchard Sim " + d.Serial
	if d.UserEnrollment {
		// personal devices don't reveal their serial number
		cn = "Orchard Sim " + d.Name
	}
	csrDER, err := x509util.CreateCertificateRequest(rand.Reader, &x509util.CertificateRequest{
		CertificateRequest: x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}},
		ChallengePassword:  challenge,
	}, key)
	if err != nil {
		return err
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return err
	}
	// temporary self-signed cert to sign the SCEP request
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "scep-client"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment}
	selfDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	self, _ := x509.ParseCertificate(selfDER)
	msg, err := scep.NewCSRRequest(csr, &scep.PKIMessage{MessageType: scep.PKCSReq, Recipients: []*x509.Certificate{ca}, SignerKey: key, SignerCert: self})
	if err != nil {
		return err
	}
	resp, err = d.HTTP.Post(url+"?operation=PKIOperation", "application/x-pki-message", bytes.NewReader(msg.Raw))
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("PKIOperation: %s %s", resp.Status, body)
	}
	rep, err := scep.ParsePKIMessage(body, scep.WithCACerts([]*x509.Certificate{ca}))
	if err != nil {
		return err
	}
	if rep.PKIStatus != scep.SUCCESS {
		return fmt.Errorf("SCEP failed: status %s fail %s", rep.PKIStatus, rep.FailInfo)
	}
	if err := rep.DecryptPKIEnvelope(self, key); err != nil {
		return err
	}
	d.key, d.cert = key, rep.CertRepMessage.Certificate
	return nil
}

func (d *Device) signedRequest(url string, body []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-apple-aspen-mdm")
	sd, err := pkcs7.NewSignedData(body)
	if err != nil {
		return nil, err
	}
	if err := sd.AddSigner(d.cert, d.key, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, err
	}
	sd.Detach()
	sig, err := sd.Finish()
	if err != nil {
		return nil, err
	}
	req.Header.Set("Mdm-Signature", base64.StdEncoding.EncodeToString(sig))
	return d.HTTP.Do(req)
}

func (d *Device) checkin(msg map[string]any) ([]byte, error) {
	body, err := plist.Marshal(msg, plist.XMLFormat)
	if err != nil {
		return nil, err
	}
	resp, err := d.signedRequest(d.checkInURL, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return out, fmt.Errorf("check-in %s: %s %s", msg["MessageType"], resp.Status, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Unenroll simulates the user removing the management profile.
func (d *Device) Unenroll() error {
	_, err := d.checkin(map[string]any{"MessageType": "CheckOut", d.idKey(): d.UDID, "Topic": d.topic})
	d.mu.Lock()
	d.enrolled = false
	d.mu.Unlock()
	return err
}

// Poll connects to the server and processes commands until the queue is
// empty. It returns the request types handled.
func (d *Device) Poll() ([]string, error) {
	var handled []string
	report := map[string]any{d.idKey(): d.UDID, "Status": "Idle"}
	for i := 0; i < 500; i++ {
		body, _ := plist.Marshal(report, plist.XMLFormat)
		resp, err := d.signedRequest(d.serverURL, body)
		if err != nil {
			return handled, err
		}
		out, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			return handled, fmt.Errorf("connect: %s %s", resp.Status, out)
		}
		if len(bytes.TrimSpace(out)) == 0 {
			return handled, nil
		}
		var cmd struct {
			CommandUUID string
			Command     map[string]any
		}
		if _, err := plist.Unmarshal(out, &cmd); err != nil {
			return handled, fmt.Errorf("decode command: %w", err)
		}
		rt, _ := cmd.Command["RequestType"].(string)
		handled = append(handled, rt)
		d.logf("command %s", rt)
		report = d.handle(rt, cmd.Command)
		report[d.idKey()] = d.UDID
		report["CommandUUID"] = cmd.CommandUUID
		if !d.Enrolled() {
			return handled, nil
		}
	}
	return handled, errors.New("too many commands")
}

func ack(extra map[string]any) map[string]any {
	m := map[string]any{"Status": "Acknowledged"}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

func errorReport(desc string) map[string]any {
	return map[string]any{"Status": "Error", "ErrorChain": []any{map[string]any{"ErrorCode": 12021, "ErrorDomain": "MCMDMErrorDomain", "LocalizedDescription": desc, "USEnglishDescription": desc}}}
}

func (d *Device) handle(rt string, c map[string]any) map[string]any {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.UserEnrollment && !mdm.AllowedOnUserEnrollment(rt) {
		return errorReport("The MDM command " + rt + " is not allowed for user enrollment")
	}
	switch rt {
	case "DeviceInformation":
		d.Battery -= 0.01 + mrand.Float64()*0.03
		if d.Battery < 0.05 {
			d.Battery = 1
		}
		all := map[string]any{
			"UDID": d.UDID, "DeviceName": d.Name, "OSVersion": d.OSVersion, "BuildVersion": d.BuildVersion, "ModelName": d.ModelName, "Model": "MTP03J/A",
			"ProductName": d.ProductName, "SerialNumber": d.Serial, "DeviceCapacity": d.CapacityGB, "AvailableDeviceCapacity": d.AvailableGB,
			"BatteryLevel": d.Battery, "IMEI": d.IMEI, "IsSupervised": d.Supervised, "IsDeviceLocatorServiceEnabled": true, "IsActivationLockEnabled": false,
			"WiFiMAC": d.WiFiMAC, "BluetoothMAC": d.WiFiMAC, "PhoneNumber": d.Phone, "CurrentCarrierNetwork": d.Carrier, "CellularTechnology": 1,
			"IsRoaming": false, "PersonalHotspotEnabled": false, "IsMDMLostModeEnabled": d.lostMode, "DataRoamingEnabled": false, "VoiceRoamingEnabled": true,
			"Languages": []any{"en-JP", "ja-JP"}, "TimeZone": "Asia/Tokyo",
		}
		if d.UserEnrollment {
			for _, k := range []string{"UDID", "SerialNumber", "IMEI", "PhoneNumber", "WiFiMAC", "BluetoothMAC", "CurrentCarrierNetwork", "IsActivationLockEnabled"} {
				delete(all, k)
			}
		}
		resp := map[string]any{}
		queries, _ := c["Queries"].([]any)
		for _, q := range queries {
			if k, ok := q.(string); ok {
				if v, ok := all[k]; ok && v != "" {
					resp[k] = v
				}
			}
		}
		return ack(map[string]any{"QueryResponses": resp})
	case "SecurityInfo":
		return ack(map[string]any{"SecurityInfo": map[string]any{"HardwareEncryptionCaps": 3, "PasscodePresent": d.PasscodePresent, "PasscodeCompliant": d.PasscodePresent,
			"PasscodeCompliantWithProfiles": d.PasscodePresent, "ManagementStatus": map[string]any{"EnrolledViaDEP": false, "IsUserEnrollment": false}}})
	case "InstalledApplicationList":
		var list []any
		managedOnly := d.UserEnrollment || c["ManagedAppsOnly"] == true
		for _, a := range d.apps {
			if managedOnly && !a.Managed {
				continue
			}
			list = append(list, map[string]any{"Identifier": a.Identifier, "Name": a.Name, "Version": a.Version, "ShortVersion": a.Version, "BundleSize": 50_000_000})
		}
		return ack(map[string]any{"InstalledApplicationList": list})
	case "ManagedApplicationList":
		m := map[string]any{}
		for _, a := range d.apps {
			if a.Managed {
				m[a.Identifier] = map[string]any{"Status": "Managed", "ManagementFlags": 1}
			}
		}
		return ack(map[string]any{"ManagedApplicationList": m})
	case "ProfileList":
		var list []any
		for _, p := range d.profiles {
			list = append(list, p)
		}
		return ack(map[string]any{"ProfileList": list})
	case "CertificateList":
		return ack(map[string]any{"CertificateList": []any{map[string]any{"CommonName": d.cert.Subject.CommonName, "Data": d.cert.Raw, "IsIdentity": true}}})
	case "ProvisioningProfileList":
		return ack(map[string]any{"ProvisioningProfileList": []any{}})
	case "Restrictions":
		return ack(map[string]any{"GlobalRestrictions": map[string]any{"restrictedBool": map[string]any{}}})
	case "AvailableOSUpdates":
		return ack(map[string]any{"AvailableOSUpdates": []any{map[string]any{"ProductKey": "iOSUpdate22H20", "HumanReadableName": "iOS 18.7", "Version": "18.7", "Build": "22H20", "IsCritical": false, "RestartRequired": true}}})
	case "OSUpdateStatus":
		return ack(map[string]any{"OSUpdateStatus": []any{}})
	case "InstallProfile":
		data, _ := c["Payload"].([]byte)
		var p map[string]any
		if _, err := plist.Unmarshal(pki.UnwrapSigned(data), &p); err != nil {
			return errorReport("The profile could not be parsed")
		}
		ident, _ := p["PayloadIdentifier"].(string)
		p["IsManaged"] = true
		d.profiles[ident] = p
		return ack(nil)
	case "RemoveProfile":
		ident, _ := c["Identifier"].(string)
		if _, ok := d.profiles[ident]; !ok {
			return errorReport("The profile is not installed")
		}
		delete(d.profiles, ident)
		if ident == d.mdmIdent {
			d.mu.Unlock()
			_ = d.Unenroll()
			d.mu.Lock()
		}
		return ack(nil)
	case "InstallApplication":
		id, name := "", ""
		if v, ok := c["iTunesStoreID"]; ok {
			id = fmt.Sprintf("store.app.%v", v)
			if a, ok := d.StoreApps[fmt.Sprint(v)]; ok {
				id, name = a.Identifier, a.Name
			}
		}
		if u, ok := c["ManifestURL"].(string); ok {
			id = "enterprise." + u[strings.LastIndex(u[:strings.LastIndex(u, "/")], "/")+1:strings.LastIndex(u, "/")]
		}
		if bid, ok := c["Identifier"].(string); ok {
			id = bid
		}
		if name == "" {
			name = id
		}
		d.apps[id] = &App{Identifier: id, Name: name, Version: "1.0", Managed: true}
		return ack(map[string]any{"Identifier": id, "State": "Managed"})
	case "RemoveApplication":
		id, _ := c["Identifier"].(string)
		delete(d.apps, id)
		return ack(nil)
	case "EnableLostMode":
		if !d.Supervised {
			return errorReport("Device is not supervised")
		}
		d.lostMode = true
		return ack(nil)
	case "DisableLostMode":
		d.lostMode = false
		return ack(nil)
	case "DeviceLocation":
		if !d.lostMode {
			return errorReport("Device is not in Lost Mode")
		}
		d.Latitude += (mrand.Float64() - 0.5) / 500
		d.Longitude += (mrand.Float64() - 0.5) / 500
		return ack(map[string]any{"Latitude": d.Latitude, "Longitude": d.Longitude, "HorizontalAccuracy": 12.5, "Altitude": 40.0, "Speed": 0.0, "Course": -1.0,
			"Timestamp": time.Now().UTC().Format(time.RFC3339)})
	case "Settings":
		items, _ := c["Settings"].([]any)
		var results []any
		for _, it := range items {
			m, _ := it.(map[string]any)
			if m["Item"] == "DeviceName" {
				d.Name, _ = m["DeviceName"].(string)
			}
			results = append(results, map[string]any{"Item": m["Item"], "Status": "Acknowledged"})
		}
		return ack(map[string]any{"Settings": results})
	case "DeclarativeManagement":
		d.mu.Unlock()
		err := d.syncDeclarations()
		d.mu.Lock()
		if err != nil {
			return errorReport(err.Error())
		}
		return ack(nil)
	case "ActivationLockBypassCode":
		return ack(map[string]any{"ActivationLockBypassCode": "ABCDE-FGHIJ-KLMNO-PQRST-UVWXY"})
	case "EraseDevice":
		// a real device enrolled by Orchard lacks the erase access right
		return errorReport("Not authorized: the MDM profile does not grant the erase right")
	}
	return ack(nil)
}

func (d *Device) ddm(endpoint string, data []byte) ([]byte, error) {
	msg := map[string]any{"MessageType": "DeclarativeManagement", d.idKey(): d.UDID, "Endpoint": endpoint}
	if data != nil {
		msg["Data"] = data
	}
	return d.checkin(msg)
}

func (d *Device) syncDeclarations() error {
	out, err := d.ddm("tokens", nil)
	if err != nil {
		return err
	}
	var tokens struct {
		SyncTokens struct{ DeclarationsToken string }
	}
	if err := json.Unmarshal(out, &tokens); err != nil {
		return err
	}
	out, err = d.ddm("declaration-items", nil)
	if err != nil {
		return err
	}
	var items struct {
		Declarations map[string][]struct{ Identifier, ServerToken string }
	}
	if err := json.Unmarshal(out, &items); err != nil {
		return err
	}
	kinds := map[string]string{"Activations": "activation", "Configurations": "configuration", "Assets": "asset", "Management": "management"}
	var activations []any
	var statuses []any
	var fetched []string
	for group, list := range items.Declarations {
		for _, it := range list {
			path := "declaration/" + kinds[group] + "/" + it.Identifier
			if _, err := d.ddm(path, nil); err != nil {
				return err
			}
			fetched = append(fetched, path)
			st := map[string]any{"identifier": it.Identifier, "active": true, "valid": "valid", "server-token": it.ServerToken}
			switch kinds[group] {
			case "configuration":
				statuses = append(statuses, st)
			case "activation":
				activations = append(activations, st)
			}
		}
	}
	report, _ := json.Marshal(map[string]any{
		"StatusItems": map[string]any{
			"device": map[string]any{
				"operating-system": map[string]any{"version": d.OSVersion, "build-version": d.BuildVersion},
				"power":            map[string]any{"battery-health": "normal"},
			},
			"passcode":   map[string]any{"is-present": d.PasscodePresent, "is-compliant": d.PasscodePresent},
			"management": map[string]any{"declarations": map[string]any{"configurations": statuses, "activations": activations}},
		},
		"Errors": []any{}, "FullReport": true,
	})
	if _, err := d.ddm("status", report); err != nil {
		return err
	}
	d.mu.Lock()
	d.ddmToken = tokens.SyncTokens.DeclarationsToken
	d.ddmFetched = fetched
	d.mu.Unlock()
	return nil
}

// AddApp puts an app on the device, as if the user (or MDM, when managed) installed it.
func (d *Device) AddApp(bundleID, name, version string, managed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.apps[bundleID] = &App{Identifier: bundleID, Name: name, Version: version, Managed: managed}
}
