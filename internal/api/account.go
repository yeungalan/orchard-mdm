package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/yeungalan/orchard-mdm/internal/portal"
)

type accountConfigView struct {
	Enabled       bool     `json:"enabled"`
	Mode          string   `json:"mode"`
	Domains       []string `json:"domains"`
	Auth          string   `json:"auth"`
	GroupIDs      []int64  `json:"group_ids"`
	OIDCIssuer    string   `json:"oidc_issuer"`
	OIDCClientID  string   `json:"oidc_client_id"`
	OIDCHasSecret bool     `json:"oidc_has_secret"`
	OIDCClaim     string   `json:"oidc_identity_claim"`
	OIDCMatch     bool     `json:"oidc_require_match"`
}

func (a *API) accountEnrollment(w http.ResponseWriter, r *http.Request, p *Principal) error {
	cfg := portal.LoadAccountConfig(a.Store)
	base := a.MDM.PublicURL()
	view := accountConfigView{
		Enabled: cfg.Enabled, Mode: cfg.Mode, Domains: cfg.Domains, Auth: a.Store.GetSetting(portal.SettingAccountAuth, "code"), GroupIDs: cfg.GroupIDs,
		OIDCIssuer: cfg.OIDCIssuer, OIDCClientID: cfg.OIDCClientID, OIDCHasSecret: cfg.OIDCSecret != "", OIDCClaim: cfg.OIDCClaim, OIDCMatch: cfg.OIDCMatch,
	}
	if view.Domains == nil {
		view.Domains = []string{}
	}
	if view.GroupIDs == nil {
		view.GroupIDs = []int64{}
	}
	recent, err := a.Store.ListAccountEnrollments(50)
	if err != nil {
		return err
	}
	servers, _ := a.Store.ListDEPServers()
	type ade struct {
		ID         int64  `json:"id"`
		Name       string `json:"name"`
		HasToken   bool   `json:"has_token"`
		Discovery  string `json:"service_discovery_url"`
		Registered bool   `json:"registered"`
	}
	var ades []ade
	wellKnown := base + "/.well-known/com.apple.remotemanagement"
	for _, s := range servers {
		reg := a.Store.GetSetting(fmt.Sprintf("ade_service_discovery_%d", s.ID), "")
		ades = append(ades, ade{ID: s.ID, Name: firstNonEmpty(s.ServerName, s.Name), HasToken: s.HasToken, Discovery: reg, Registered: reg == wellKnown})
	}
	if ades == nil {
		ades = []ade{}
	}
	return ok(w, map[string]any{
		"config": view, "recent": recent, "ade_servers": ades,
		"well_known_url": wellKnown, "enroll_url": base + "/enroll/account/" + cfg.Mode, "oidc_redirect_url": base + "/enroll/account/callback",
	})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func (a *API) putAccountEnrollment(w http.ResponseWriter, r *http.Request, p *Principal) error {
	var req struct {
		Enabled      bool     `json:"enabled"`
		Mode         string   `json:"mode"`
		Domains      []string `json:"domains"`
		Auth         string   `json:"auth"`
		GroupIDs     []int64  `json:"group_ids"`
		OIDCIssuer   string   `json:"oidc_issuer"`
		OIDCClientID string   `json:"oidc_client_id"`
		OIDCSecret   *string  `json:"oidc_client_secret"`
		OIDCClaim    string   `json:"oidc_identity_claim"`
		OIDCMatch    bool     `json:"oidc_require_match"`
	}
	if err := decode(r, &req); err != nil {
		return err
	}
	if req.Mode != "byod" && req.Mode != "adde" {
		return badRequest("mode must be byod (User Enrollment) or adde (device enrollment)")
	}
	switch req.Auth {
	case "code", "sso", "both":
	default:
		return badRequest("auth must be code, sso or both")
	}
	issuer := strings.TrimRight(strings.TrimSpace(req.OIDCIssuer), "/")
	if issuer != "" && !strings.HasPrefix(issuer, "https://") && !strings.HasPrefix(issuer, "http://127.0.0.1") && !strings.HasPrefix(issuer, "http://localhost") {
		return badRequest("the identity provider issuer must be an https:// URL")
	}
	if (req.Auth == "sso" || req.Auth == "both") && (issuer == "" || strings.TrimSpace(req.OIDCClientID) == "") {
		return badRequest("single sign-on needs an issuer URL and a client ID")
	}
	var domains []string
	for _, d := range req.Domains {
		d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), "@"))
		if d == "" {
			continue
		}
		if !strings.Contains(d, ".") || strings.ContainsAny(d, " /:@") {
			return badRequest("%q is not a domain name", d)
		}
		domains = append(domains, d)
	}
	for _, g := range req.GroupIDs {
		grp, err := a.Store.GetGroup(g)
		if err != nil || grp.Kind != "static" {
			return badRequest("group %d is not a static group", g)
		}
	}
	if req.Enabled && a.MDM.PublicURL() == "" {
		return badRequest("set the public server URL before enabling account sign-in")
	}
	claim := strings.TrimSpace(req.OIDCClaim)
	if claim == "" {
		claim = "email"
	}
	set := map[string]string{
		portal.SettingAccountEnabled: boolStr(req.Enabled), portal.SettingAccountMode: req.Mode, portal.SettingAccountDomains: strings.Join(domains, ","),
		portal.SettingAccountAuth: req.Auth, portal.SettingOIDCIssuer: issuer, portal.SettingOIDCClientID: strings.TrimSpace(req.OIDCClientID),
		portal.SettingOIDCClaim: claim, portal.SettingOIDCMatch: boolStr(req.OIDCMatch),
	}
	for k, v := range set {
		if err := a.Store.SetSetting(k, v); err != nil {
			return err
		}
	}
	if req.GroupIDs == nil {
		req.GroupIDs = []int64{}
	}
	if err := a.Store.SetSettingJSON(portal.SettingAccountGroupIDs, req.GroupIDs); err != nil {
		return err
	}
	if req.OIDCSecret != nil {
		if err := a.Store.SetSetting(portal.SettingOIDCClientSecret, *req.OIDCSecret); err != nil {
			return err
		}
	}
	a.audit(r, p, "enrollment.account_updated", req.Mode, map[string]any{"enabled": req.Enabled, "auth": req.Auth, "domains": domains, "issuer": issuer})
	return a.accountEnrollment(w, r, p)
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (a *API) adeServiceDiscovery(w http.ResponseWriter, r *http.Request, p *Principal) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	u, err := a.DEP.AssignServiceDiscovery(r.Context(), id)
	if err != nil {
		return errf(http.StatusBadGateway, "Apple Business Manager rejected the request: %v", err)
	}
	a.audit(r, p, "ade.service_discovery", fmt.Sprint(id), map[string]string{"url": u})
	return ok(w, map[string]string{"service_discovery_url": u})
}
