// Package reconcile drives devices toward their desired state: group
// membership, assigned profiles, apps and declarations.
package reconcile

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/yeungalan/orchard-mdm/internal/store"
)

// RuleFields lists the attributes available to dynamic group rules.
var RuleFields = []map[string]string{
	{"id": "model", "label": "Model (product name)", "type": "string"},
	{"id": "model_name", "label": "Device type (iPhone/iPad)", "type": "string"},
	{"id": "os_version", "label": "OS version", "type": "version"},
	{"id": "name", "label": "Device name", "type": "string"},
	{"id": "serial", "label": "Serial number", "type": "string"},
	{"id": "ownership", "label": "Ownership (corporate/personal)", "type": "string"},
	{"id": "enrollment_type", "label": "Enrollment type (ade/token/manual)", "type": "string"},
	{"id": "supervised", "label": "Supervised", "type": "bool"},
	{"id": "tag", "label": "Tag", "type": "string"},
	{"id": "assigned_user", "label": "Assigned user", "type": "string"},
	{"id": "carrier", "label": "Carrier", "type": "string"},
	{"id": "compliance", "label": "Compliance state", "type": "string"},
	{"id": "app_installed", "label": "App installed (bundle ID)", "type": "string"},
	{"id": "battery", "label": "Battery level (%)", "type": "number"},
	{"id": "available_gb", "label": "Free storage (GB)", "type": "number"},
	{"id": "lost_mode", "label": "In Lost Mode", "type": "bool"},
}

// RuleOps lists the supported operators.
var RuleOps = []map[string]string{
	{"id": "eq", "label": "is"}, {"id": "neq", "label": "is not"}, {"id": "contains", "label": "contains"},
	{"id": "not_contains", "label": "does not contain"}, {"id": "prefix", "label": "starts with"}, {"id": "suffix", "label": "ends with"},
	{"id": "in", "label": "is one of (comma separated)"}, {"id": "gte", "label": "≥"}, {"id": "lte", "label": "≤"},
	{"id": "gt", "label": ">"}, {"id": "lt", "label": "<"}, {"id": "regex", "label": "matches regex"},
}

// CompareVersions compares dotted version strings numerically.
func CompareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(strings.TrimSpace(pa[i]))
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(strings.TrimSpace(pb[i]))
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// deviceValues returns the candidate values of a field for a device.
func deviceValues(d *store.Device, field string, apps map[string]store.DeviceApp) []string {
	switch field {
	case "model":
		return []string{d.ProductName}
	case "model_name":
		return []string{d.ModelName}
	case "os_version":
		return []string{d.OSVersion}
	case "name":
		return []string{d.DeviceName}
	case "serial":
		return []string{d.SerialNumber}
	case "ownership":
		return []string{d.Ownership}
	case "enrollment_type":
		return []string{d.EnrollmentType}
	case "supervised":
		return []string{strconv.FormatBool(d.Supervised)}
	case "lost_mode":
		return []string{strconv.FormatBool(d.LostMode)}
	case "tag":
		return d.Tags
	case "assigned_user":
		return []string{d.AssignedUser, d.AssignedEmail}
	case "carrier":
		return []string{d.Carrier}
	case "compliance":
		return []string{d.Compliance}
	case "battery":
		if d.BatteryLevel < 0 {
			return nil
		}
		return []string{fmt.Sprintf("%.0f", d.BatteryLevel*100)}
	case "available_gb":
		return []string{fmt.Sprintf("%.2f", d.AvailableGB)}
	case "app_installed":
		out := make([]string, 0, len(apps))
		for b := range apps {
			out = append(out, b)
		}
		return out
	}
	return nil
}

func matchOne(v, op, want, typ string) bool {
	lv, lw := strings.ToLower(v), strings.ToLower(strings.TrimSpace(want))
	switch op {
	case "eq":
		return lv == lw
	case "neq":
		return lv != lw
	case "contains":
		return strings.Contains(lv, lw)
	case "not_contains":
		return !strings.Contains(lv, lw)
	case "prefix":
		return strings.HasPrefix(lv, lw)
	case "suffix":
		return strings.HasSuffix(lv, lw)
	case "in":
		for _, x := range strings.Split(lw, ",") {
			if strings.TrimSpace(x) == lv {
				return true
			}
		}
		return false
	case "regex":
		re, err := regexp.Compile(want)
		return err == nil && re.MatchString(v)
	case "gte", "lte", "gt", "lt":
		var c int
		if typ == "version" {
			c = CompareVersions(v, want)
		} else {
			a, err1 := strconv.ParseFloat(v, 64)
			b, err2 := strconv.ParseFloat(strings.TrimSpace(want), 64)
			if err1 != nil || err2 != nil {
				return false
			}
			switch {
			case a < b:
				c = -1
			case a > b:
				c = 1
			}
		}
		switch op {
		case "gte":
			return c >= 0
		case "lte":
			return c <= 0
		case "gt":
			return c > 0
		default:
			return c < 0
		}
	}
	return false
}

func fieldType(field string) string {
	for _, f := range RuleFields {
		if f["id"] == field {
			return f["type"]
		}
	}
	return "string"
}

// MatchRule evaluates one rule.
func MatchRule(d *store.Device, r store.GroupRule, apps map[string]store.DeviceApp) bool {
	vals := deviceValues(d, r.Field, apps)
	negative := r.Op == "neq" || r.Op == "not_contains"
	if len(vals) == 0 {
		return negative
	}
	if negative {
		for _, v := range vals {
			if !matchOne(v, r.Op, r.Value, fieldType(r.Field)) {
				return false
			}
		}
		return true
	}
	for _, v := range vals {
		if matchOne(v, r.Op, r.Value, fieldType(r.Field)) {
			return true
		}
	}
	return false
}

// MatchRules evaluates a rule set.
func MatchRules(d *store.Device, rules *store.GroupRules, apps map[string]store.DeviceApp) bool {
	if rules == nil || len(rules.Rules) == 0 {
		return false
	}
	any := rules.Match == "any"
	for _, r := range rules.Rules {
		ok := MatchRule(d, r, apps)
		if any && ok {
			return true
		}
		if !any && !ok {
			return false
		}
	}
	return !any
}

func needsApps(rules *store.GroupRules) bool {
	if rules == nil {
		return false
	}
	for _, r := range rules.Rules {
		if r.Field == "app_installed" {
			return true
		}
	}
	return false
}
