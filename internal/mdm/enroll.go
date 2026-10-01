package mdm

import (
	"crypto/x509"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"howett.net/plist"
)

// AccessRights granted by the enrollment profile: every right (8191) except
// "device erase" (8). Without that bit, devices refuse EraseDevice even if a
// command were somehow queued — Orchard deliberately cannot wipe devices.
const AccessRights = 8191 &^ 8

// ErrNoPushCert is returned when enrollment is attempted before APNs is set up.
var ErrNoPushCert = errors.New("APNs push certificate is not configured (Settings → Apple Push)")

// ErrNoPublicURL is returned when the server URL is unknown.
var ErrNoPublicURL = errors.New("the public server URL is not configured (Settings → General, or the -url flag)")

func randomToken(n int) string { return pki.RandomToken(n) }

// EnrollmentOptions controls enrollment profile generation.
type EnrollmentOptions struct {
	// Ref records how the device enrolled: "token:<id>", "ade", "manual" or "renew:<udid>".
	Ref string
	// ChallengeTTL is how long the embedded SCEP challenge stays valid.
	ChallengeTTL time.Duration
}

// EnrollmentProfile builds a (signed when possible) enrollment profile.
func (s *Service) EnrollmentProfile(o EnrollmentOptions) ([]byte, error) {
	topic := ""
	if s.push != nil {
		topic = s.push.Topic()
	}
	if topic == "" {
		return nil, ErrNoPushCert
	}
	base := s.PublicURL()
	if base == "" {
		return nil, ErrNoPublicURL
	}
	if o.ChallengeTTL <= 0 {
		o.ChallengeTTL = 24 * time.Hour
	}
	if o.Ref == "" {
		o.Ref = "manual"
	}
	challenge := randomToken(20)
	if err := s.store.CreateChallenge(challenge, "enroll", o.Ref, int64(o.ChallengeTTL.Seconds())); err != nil {
		return nil, err
	}
	org := s.Setting(SettingOrgName)
	ident := s.MDMIdentifier()
	scepUUID := pki.NewUUID()

	var content []any
	for i, c := range s.extraTrustCerts() {
		content = append(content, map[string]any{
			"PayloadType":                "com.apple.security.root",
			"PayloadIdentifier":          fmt.Sprintf("%s.trust.%d", ident, i),
			"PayloadUUID":                pki.NewUUID(),
			"PayloadVersion":             1,
			"PayloadDisplayName":         "Trusted certificate: " + c.Subject.CommonName,
			"PayloadCertificateFileName": c.Subject.CommonName + ".cer",
			"PayloadContent":             c.Raw,
		})
	}
	content = append(content,
		map[string]any{
			"PayloadType":        "com.apple.security.scep",
			"PayloadIdentifier":  ident + ".scep",
			"PayloadUUID":        scepUUID,
			"PayloadVersion":     1,
			"PayloadDisplayName": org + " Device Identity",
			"PayloadDescription": "Certificate this device uses to authenticate with the management server.",
			"PayloadContent": map[string]any{
				"URL":       base + "/scep",
				"Name":      "OrchardMDM",
				"Subject":   [][][]string{{{"O", org}}, {{"CN", "Orchard MDM Identity " + strings.ToUpper(randomToken(4))}}},
				"Challenge": challenge,
				"Keysize":   2048,
				"Key Type":  "RSA",
				"Key Usage": 5,
			},
		},
		map[string]any{
			"PayloadType":             "com.apple.mdm",
			"PayloadIdentifier":       ident + ".mdm",
			"PayloadUUID":             pki.NewUUID(),
			"PayloadVersion":          1,
			"PayloadDisplayName":      org + " Device Management",
			"PayloadDescription":      "Allows " + org + " to manage this device.",
			"IdentityCertificateUUID": scepUUID,
			"Topic":                   topic,
			"ServerURL":               base + "/mdm/connect",
			"CheckInURL":              base + "/mdm/checkin",
			"CheckOutWhenRemoved":     true,
			"SignMessage":             true,
			"AccessRights":            AccessRights,
			"ServerCapabilities":      []string{"com.apple.mdm.per-user-connections", "com.apple.mdm.bootstraptoken"},
		},
	)
	profile := map[string]any{
		"PayloadType":         "Configuration",
		"PayloadVersion":      1,
		"PayloadIdentifier":   ident,
		"PayloadUUID":         pki.NewUUID(),
		"PayloadDisplayName":  org + " Enrollment",
		"PayloadDescription":  "Enrolls this device in " + org + " mobile device management.",
		"PayloadOrganization": org,
		"PayloadScope":        "System",
		"PayloadContent":      content,
	}
	if consent := s.Setting(SettingEnrollConsentText); consent != "" {
		profile["ConsentText"] = map[string]string{"default": consent}
	}
	raw, err := plist.MarshalIndent(profile, plist.XMLFormat, "\t")
	if err != nil {
		return nil, err
	}
	return s.SignProfileBytes(raw)
}

func (s *Service) extraTrustCerts() []*x509.Certificate {
	pemText := s.Setting(SettingExtraTrustCerts)
	if strings.TrimSpace(pemText) == "" {
		return nil
	}
	certs, err := pki.ParseCertsPEM([]byte(pemText))
	if err != nil {
		return nil
	}
	return certs
}

// SignProfileBytes signs a profile when signing is enabled and an identity is available.
func (s *Service) SignProfileBytes(raw []byte) ([]byte, error) {
	if !s.SettingBool(SettingSignProfiles) {
		return raw, nil
	}
	cert, key, chain := s.SigningIdentity()
	if cert == nil || key == nil {
		return raw, nil
	}
	signed, err := pki.SignProfile(raw, cert, key, chain)
	if err != nil {
		s.log.Warn("profile signing failed; sending unsigned", "err", err)
		return raw, nil
	}
	return signed, nil
}

// ProfileForDevice renders a managed profile for one device: variables are
// expanded and the result is signed.
func (s *Service) ProfileForDevice(p *store.Profile, d *store.Device) ([]byte, error) {
	if len(p.Raw) == 0 {
		return nil, errors.New("profile has no content")
	}
	raw := p.Raw
	if d != nil && strings.Contains(string(raw), "{{") {
		raw = []byte(Expand(string(raw), s.Vars(d), true))
	}
	return s.SignProfileBytes(raw)
}

// RenewIdentity sends a fresh enrollment profile so the device obtains a new
// identity certificate without re-enrolling.
func (s *Service) RenewIdentity(udid string, meta Meta) (*store.Command, error) {
	prof, err := s.EnrollmentProfile(EnrollmentOptions{Ref: "renew:" + udid, ChallengeTTL: 7 * 24 * time.Hour})
	if err != nil {
		return nil, err
	}
	meta.Ref = "renew-identity"
	return s.EnqueueAndPush(udid, map[string]any{"RequestType": "InstallProfile", "Payload": prof}, meta)
}

// Unenroll removes the enrollment profile from the device, which ends
// management (the device keeps its data; managed apps flagged for removal
// are removed by iOS). This is not a wipe.
func (s *Service) Unenroll(udid string, meta Meta) (*store.Command, error) {
	meta.Ref = "unenroll"
	return s.EnqueueAndPush(udid, map[string]any{"RequestType": "RemoveProfile", "Identifier": s.MDMIdentifier()}, meta)
}
