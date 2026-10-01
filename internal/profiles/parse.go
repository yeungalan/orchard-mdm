package profiles

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yeungalan/orchard-mdm/internal/pki"
	"howett.net/plist"
)

// Parsed describes an uploaded .mobileconfig.
type Parsed struct {
	Identifier   string
	Name         string
	Description  string
	Organization string
	Removable    bool
	PayloadTypes []string
	Encrypted    bool
	XML          []byte
	Warnings     []string
}

// Parse validates an uploaded profile (signed or unsigned, XML or binary)
// and returns its unsigned XML form.
func Parse(data []byte) (*Parsed, error) {
	content := pki.UnwrapSigned(data)
	var top map[string]any
	if _, err := plist.Unmarshal(content, &top); err != nil {
		return nil, fmt.Errorf("not a configuration profile: %w", err)
	}
	if t, _ := top["PayloadType"].(string); t != "Configuration" {
		return nil, fmt.Errorf("top-level PayloadType is %q, expected Configuration", t)
	}
	p := &Parsed{}
	p.Identifier, _ = top["PayloadIdentifier"].(string)
	p.Name, _ = top["PayloadDisplayName"].(string)
	p.Description, _ = top["PayloadDescription"].(string)
	p.Organization, _ = top["PayloadOrganization"].(string)
	removalDisallowed, _ := top["PayloadRemovalDisallowed"].(bool)
	p.Removable = !removalDisallowed
	if p.Identifier == "" {
		return nil, errors.New("profile has no PayloadIdentifier")
	}
	if p.Name == "" {
		p.Name = p.Identifier
	}
	if _, ok := top["EncryptedPayloadContent"]; ok {
		p.Encrypted = true
		p.PayloadTypes = []string{"(encrypted)"}
	}
	content2, _ := top["PayloadContent"].([]any)
	for _, raw := range content2 {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		t, _ := m["PayloadType"].(string)
		if t == "com.apple.mdm" {
			return nil, errors.New("enrollment (MDM) profiles cannot be deployed as managed profiles")
		}
		p.Warnings = append(p.Warnings, Sanitize(m)...)
		p.PayloadTypes = append(p.PayloadTypes, t)
	}
	xml, err := plist.MarshalIndent(top, plist.XMLFormat, "\t")
	if err != nil {
		return nil, err
	}
	p.XML = xml
	return p, nil
}

// Rewrap changes top-level metadata of an uploaded profile.
func Rewrap(xml []byte, identifier, name, description string) ([]byte, error) {
	var top map[string]any
	if _, err := plist.Unmarshal(xml, &top); err != nil {
		return nil, err
	}
	if identifier != "" {
		top["PayloadIdentifier"] = identifier
	}
	if name != "" {
		top["PayloadDisplayName"] = name
	}
	if strings.TrimSpace(description) != "" {
		top["PayloadDescription"] = description
	}
	return plist.MarshalIndent(top, plist.XMLFormat, "\t")
}
