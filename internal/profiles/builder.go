package profiles

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/yeungalan/orchard-mdm/internal/pki"
	"howett.net/plist"
)

// PayloadInput is one payload as edited in the console.
type PayloadInput struct {
	Type        string         `json:"type"`
	UUID        string         `json:"uuid"`
	DisplayName string         `json:"display_name"`
	Values      map[string]any `json:"values"`
}

// Meta is the top level of a profile.
type Meta struct {
	Name         string
	Identifier   string
	Description  string
	Organization string
	Removable    bool
}

// Result of building a profile.
type Result struct {
	XML          []byte
	PayloadTypes []string
	Payloads     []PayloadInput // normalised (UUIDs assigned)
	Warnings     []string
}

// StableUUID derives a deterministic UUID from a string.
func StableUUID(s string) string {
	sum := sha256.Sum256([]byte(s))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// Build renders a profile from builder payloads.
func Build(meta Meta, inputs []PayloadInput) (*Result, error) {
	if strings.TrimSpace(meta.Identifier) == "" {
		return nil, errors.New("profile identifier is required")
	}
	if strings.TrimSpace(meta.Name) == "" {
		return nil, errors.New("profile name is required")
	}
	if len(inputs) == 0 {
		return nil, errors.New("add at least one payload")
	}
	res := &Result{}
	uuids := map[string]string{}
	seenUnique := map[string]bool{}
	for i := range inputs {
		in := &inputs[i]
		if in.UUID == "" {
			in.UUID = pki.NewUUID()
		}
		if in.Values == nil {
			in.Values = map[string]any{}
		}
		uuids[in.UUID] = in.Type
	}
	var content []any
	for i, in := range inputs {
		sch, ok := SchemaByType(in.Type)
		if !ok {
			return nil, fmt.Errorf("payload %d: unknown type %q", i+1, in.Type)
		}
		if sch.Unique {
			if seenUnique[sch.Type] {
				return nil, fmt.Errorf("only one %s payload is allowed per profile", sch.Name)
			}
			seenUnique[sch.Type] = true
		}
		p, warnings, err := buildPayload(meta, i, sch, in, uuids)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", sch.Name, err)
		}
		res.Warnings = append(res.Warnings, warnings...)
		content = append(content, p)
		res.PayloadTypes = append(res.PayloadTypes, p["PayloadType"].(string))
	}
	top := map[string]any{
		"PayloadType":              "Configuration",
		"PayloadVersion":           1,
		"PayloadIdentifier":        meta.Identifier,
		"PayloadUUID":              StableUUID("profile:" + meta.Identifier),
		"PayloadDisplayName":       meta.Name,
		"PayloadScope":             "System",
		"PayloadRemovalDisallowed": !meta.Removable,
		"PayloadContent":           content,
	}
	if meta.Description != "" {
		top["PayloadDescription"] = meta.Description
	}
	if meta.Organization != "" {
		top["PayloadOrganization"] = meta.Organization
	}
	xml, err := plist.MarshalIndent(top, plist.XMLFormat, "\t")
	if err != nil {
		return nil, err
	}
	res.XML = xml
	res.Payloads = inputs
	return res, nil
}

func showIfOK(f Field, values map[string]any, sch *Schema) bool {
	for key, allowed := range f.ShowIf {
		v, ok := values[key]
		if !ok || v == nil || v == "" {
			for _, of := range sch.Fields {
				if of.Key == key {
					v = of.Default
				}
			}
		}
		match := false
		for _, a := range allowed {
			if fmt.Sprint(a) == fmt.Sprint(v) {
				match = true
			}
		}
		if !match {
			return false
		}
	}
	return true
}

func buildPayload(meta Meta, idx int, sch *Schema, in PayloadInput, uuids map[string]string) (map[string]any, []string, error) {
	ptype := sch.Type
	if sch.Type == "custom" {
		ptype = strings.TrimSpace(fmt.Sprint(in.Values["_PayloadType"]))
		if ptype == "" || ptype == "<nil>" {
			return nil, nil, errors.New("PayloadType is required")
		}
		if ptype == "com.apple.mdm" || ptype == "Configuration" {
			return nil, nil, errors.New("this payload type cannot be used in a managed profile")
		}
	}
	short := strings.TrimPrefix(ptype, "com.apple.")
	out := map[string]any{
		"PayloadType":        ptype,
		"PayloadUUID":        in.UUID,
		"PayloadVersion":     1,
		"PayloadIdentifier":  fmt.Sprintf("%s.%s.%d", meta.Identifier, short, idx+1),
		"PayloadDisplayName": firstNonEmpty(in.DisplayName, sch.Name),
	}
	omitDefaults := sch.Type == "com.apple.applicationaccess"
	connectOnDemand := false
	for _, f := range sch.Fields {
		if !showIfOK(f, in.Values, sch) {
			continue
		}
		raw, present := in.Values[f.Key]
		if !present || raw == nil {
			raw = f.Default
		}
		if f.Key == "_connect_on_demand" {
			connectOnDemand = truthy(raw)
			continue
		}
		if f.Key == "_PayloadType" {
			continue
		}
		if f.Type == "plistdict" {
			text := strings.TrimSpace(fmt.Sprint(raw))
			if text == "" || text == "<nil>" {
				continue
			}
			dict, err := parsePlistDict(text)
			if err != nil {
				return nil, nil, fmt.Errorf("payload keys: %w", err)
			}
			for k, v := range dict {
				if strings.HasPrefix(k, "Payload") && k != "PayloadContent" {
					continue
				}
				out[k] = v
			}
			continue
		}
		if omitDefaults && f.Type == "bool" && f.Default != nil && truthy(raw) == truthy(f.Default) {
			continue
		}
		val, skip, err := convert(f, raw, uuids)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", f.Label, err)
		}
		if skip {
			if f.Required {
				return nil, nil, fmt.Errorf("%s is required", f.Label)
			}
			continue
		}
		if f.Key == "PayloadDisplayName" {
			out["PayloadDisplayName"] = val
			continue
		}
		setPath(out, f.Key, val)
	}
	if connectOnDemand {
		vt := fmt.Sprint(out["VPNType"])
		if vt == "" || vt == "<nil>" {
			vt = "IKEv2"
		}
		dict, _ := out[vt].(map[string]any)
		if dict == nil {
			dict = map[string]any{}
			out[vt] = dict
		}
		dict["OnDemandEnabled"] = 1
		dict["OnDemandRules"] = []any{map[string]any{"Action": "Connect"}}
	}
	if sch.Type == "com.apple.vpn.managed" {
		if vt, _ := out["VPNType"].(string); vt == "VPN" {
			if _, ok := out["VPNSubType"]; !ok {
				return nil, nil, errors.New("VPN app bundle ID is required for app VPNs")
			}
		}
	}
	warnings := Sanitize(out)
	return out, warnings, nil
}

// Sanitize removes keys Orchard deliberately does not deploy. Passcode
// payloads lose maxFailedAttempts, which makes a device erase itself after too
// many wrong passcodes — Orchard does not configure device wipes.
func Sanitize(payload map[string]any) []string {
	var warnings []string
	if payload["PayloadType"] == "com.apple.mobiledevice.passwordpolicy" {
		if _, ok := payload["maxFailedAttempts"]; ok {
			delete(payload, "maxFailedAttempts")
			warnings = append(warnings, "Removed maxFailedAttempts from the passcode payload: Orchard MDM does not configure automatic device erase.")
		}
	}
	return warnings
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1" || t == "on"
	case float64:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	}
	return false
}

func setPath(m map[string]any, path string, v any) {
	parts := strings.Split(path, ".")
	cur := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[p] = next
		}
		cur = next
	}
	cur[parts[len(parts)-1]] = v
}

func toInt(v any) (int64, error) {
	switch t := v.(type) {
	case float64:
		if t != math.Trunc(t) {
			return 0, fmt.Errorf("%v is not a whole number", t)
		}
		return int64(t), nil
	case int:
		return int64(t), nil
	case int64:
		return t, nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(t), 10, 64)
	case json.Number:
		return t.Int64()
	}
	return 0, fmt.Errorf("invalid number %v", v)
}

func toStrings(v any) []string {
	var out []string
	switch t := v.(type) {
	case []any:
		for _, x := range t {
			if s := strings.TrimSpace(fmt.Sprint(x)); s != "" {
				out = append(out, s)
			}
		}
	case []string:
		for _, s := range t {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	case string:
		for _, s := range strings.FieldsFunc(t, func(r rune) bool { return r == '\n' || r == ',' }) {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func decodeData(v any) ([]byte, error) {
	s := strings.TrimSpace(fmt.Sprint(v))
	if i := strings.Index(s, ";base64,"); strings.HasPrefix(s, "data:") && i > 0 {
		s = s[i+8:]
	}
	return base64.StdEncoding.DecodeString(s)
}

// convert coerces a JSON value to its plist representation. skip is true when
// the value is empty and should be omitted.
func convert(f Field, v any, uuids map[string]string) (any, bool, error) {
	if v == nil {
		return nil, true, nil
	}
	if s, ok := v.(string); ok && strings.TrimSpace(s) == "" && f.Type != "bool" {
		return nil, true, nil
	}
	switch f.Type {
	case "string", "password":
		return strings.TrimSpace(fmt.Sprint(v)), false, nil
	case "text":
		return fmt.Sprint(v), false, nil
	case "int":
		n, err := toInt(v)
		return n, false, err
	case "intbool":
		if truthy(v) {
			return 1, false, nil
		}
		return 0, false, nil
	case "real":
		switch t := v.(type) {
		case float64:
			return t, false, nil
		case string:
			f, err := strconv.ParseFloat(t, 64)
			return f, false, err
		}
		return nil, false, fmt.Errorf("invalid number")
	case "bool":
		return truthy(v), false, nil
	case "enum":
		for _, o := range f.Options {
			if fmt.Sprint(o.Value) == fmt.Sprint(v) {
				switch ov := o.Value.(type) {
				case int:
					return int64(ov), false, nil
				case float64:
					return ov, false, nil
				default:
					return ov, false, nil
				}
			}
		}
		return nil, false, fmt.Errorf("invalid choice %v", v)
	case "multienum":
		var out []any
		for _, s := range toStrings(v) {
			for _, o := range f.Options {
				if fmt.Sprint(o.Value) == s {
					if n, ok := o.Value.(int); ok {
						out = append(out, int64(n))
					} else {
						out = append(out, o.Value)
					}
				}
			}
		}
		if len(out) == 0 {
			return nil, true, nil
		}
		return out, false, nil
	case "stringlist":
		list := toStrings(v)
		if len(list) == 0 {
			return nil, true, nil
		}
		return list, false, nil
	case "data", "image":
		b, err := decodeData(v)
		if err != nil {
			return nil, false, errors.New("invalid file data")
		}
		if len(b) == 0 {
			return nil, true, nil
		}
		return b, false, nil
	case "datastring":
		return []byte(fmt.Sprint(v)), false, nil
	case "cert":
		b, err := decodeData(v)
		if err != nil || len(b) == 0 {
			// maybe raw PEM text
			b = []byte(fmt.Sprint(v))
		}
		c, err := pki.ParseCertPEM(b)
		if err != nil {
			return nil, false, errors.New("not a valid PEM or DER certificate")
		}
		return c.Raw, false, nil
	case "x500":
		return parseX500(fmt.Sprint(v))
	case "payloadref":
		id := strings.TrimSpace(fmt.Sprint(v))
		if _, ok := uuids[id]; !ok {
			return nil, false, errors.New("referenced certificate payload is not in this profile")
		}
		return id, false, nil
	case "payloadrefs":
		var out []string
		for _, id := range toStrings(v) {
			if _, ok := uuids[id]; !ok {
				return nil, false, errors.New("referenced certificate payload is not in this profile")
			}
			out = append(out, id)
		}
		if len(out) == 0 {
			return nil, true, nil
		}
		return out, false, nil
	case "dock":
		ids := toStrings(v)
		if len(ids) == 0 {
			return nil, true, nil
		}
		return HomeScreenDock(ids), false, nil
	case "pages":
		pages := parsePages(fmt.Sprint(v))
		if len(pages) == 0 {
			return nil, true, nil
		}
		return pages, false, nil
	case "dictlist":
		list, ok := v.([]any)
		if !ok {
			return nil, false, errors.New("expected a list")
		}
		var out []any
		for i, raw := range list {
			item, ok := raw.(map[string]any)
			if !ok {
				return nil, false, fmt.Errorf("row %d is not an object", i+1)
			}
			row := map[string]any{}
			for _, sf := range f.Fields {
				sv, present := item[sf.Key]
				if !present || sv == nil {
					sv = sf.Default
				}
				cv, skip, err := convert(sf, sv, uuids)
				if err != nil {
					return nil, false, fmt.Errorf("row %d %s: %w", i+1, sf.Label, err)
				}
				if skip {
					if sf.Required {
						return nil, false, fmt.Errorf("row %d: %s is required", i+1, sf.Label)
					}
					continue
				}
				setPath(row, sf.Key, cv)
			}
			out = append(out, row)
		}
		if len(out) == 0 {
			return nil, true, nil
		}
		return out, false, nil
	}
	return nil, false, fmt.Errorf("unsupported field type %s", f.Type)
}

// parseX500 turns "/O=Org/CN=name" into the SCEP Subject array.
func parseX500(s string) (any, bool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, true, nil
	}
	var out []any
	for _, part := range strings.Split(strings.Trim(s, "/"), "/") {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 || strings.TrimSpace(kv[0]) == "" {
			return nil, false, fmt.Errorf("invalid subject component %q (use /O=Org/CN=Name)", part)
		}
		out = append(out, []any{[]any{strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])}})
	}
	return out, false, nil
}

// parsePages turns the page editor text into Home Screen layout pages.
func parsePages(text string) []any {
	var pages []any
	var cur []any
	flush := func() {
		if len(cur) > 0 {
			pages = append(pages, cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		if i := strings.Index(line, ":"); i > 0 {
			name := strings.TrimSpace(line[:i])
			var apps []any
			for _, id := range strings.Split(line[i+1:], ",") {
				if id = strings.TrimSpace(id); id != "" {
					apps = append(apps, map[string]any{"Type": "Application", "BundleID": id})
				}
			}
			cur = append(cur, map[string]any{"Type": "Folder", "DisplayName": name, "Pages": []any{apps}})
			continue
		}
		cur = append(cur, map[string]any{"Type": "Application", "BundleID": line})
	}
	flush()
	return pages
}

func parsePlistDict(text string) (map[string]any, error) {
	if !strings.Contains(text, "<plist") {
		text = `<?xml version="1.0" encoding="UTF-8"?><plist version="1.0">` + text + `</plist>`
	}
	var m map[string]any
	if _, err := plist.Unmarshal([]byte(text), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// HomeScreenDock converts dock bundle IDs to layout items.
func HomeScreenDock(ids []string) []any {
	var out []any
	for _, id := range ids {
		out = append(out, map[string]any{"Type": "Application", "BundleID": id})
	}
	return out
}
