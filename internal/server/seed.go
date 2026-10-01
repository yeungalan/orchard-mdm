package server

import (
	"github.com/yeungalan/orchard-mdm/internal/api"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/profiles"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

const portalProfileIdentifier = "com.orchardmdm.portal"

// seed prepares a fresh installation.
func (a *App) seed() error {
	n, err := a.Store.CountUsers()
	if err != nil {
		return err
	}
	if n == 0 && a.Cfg.AdminUser != "" && a.Cfg.AdminPassword != "" {
		if err := a.API.CreateUser(a.Cfg.AdminUser, a.Cfg.AdminPassword, "admin", "Administrator"); err != nil {
			return err
		}
		a.Log.Info("created administrator from flags", "username", a.Cfg.AdminUser)
		n = 1
	}
	if n == 0 {
		tok := a.API.SetupToken()
		a.Log.Warn("═══ First start: open the web console and create the administrator account ═══", "setup_code", tok)
	}
	_ = a.MDM.MDMIdentifier()
	if a.Store.GetSetting("seed_portal_profile", "") == "" {
		if err := a.seedPortalProfile(); err != nil {
			a.Log.Warn("could not create the Company Portal profile", "err", err)
		}
		_ = a.Store.SetSetting("seed_portal_profile", "1")
	}
	return nil
}

// seedPortalProfile creates a web clip that opens the self-service portal and
// assigns it to every device.
func (a *App) seedPortalProfile() error {
	payloads := []profiles.PayloadInput{{
		Type: "com.apple.webClip.managed", DisplayName: "Company Portal",
		Values: map[string]any{"Label": "Company Portal", "URL": "{{device.portal_url}}", "IsRemovable": false, "FullScreen": false},
	}}
	res, err := profiles.Build(profiles.Meta{Name: "Company Portal", Identifier: portalProfileIdentifier,
		Description:  "Home Screen shortcut to the self-service portal: device status, compliance, and available apps.",
		Organization: a.MDM.Setting(mdm.SettingOrgName)}, payloads)
	if err != nil {
		return err
	}
	raw, _ := api.MarshalPayloads(res.Payloads)
	p := &store.Profile{Name: "Company Portal", Identifier: portalProfileIdentifier, Description: "Self-service portal web clip (built in).",
		Source: "builder", Payloads: raw, Raw: res.XML, Scope: "System", PayloadTypes: res.PayloadTypes}
	if err := a.Store.CreateProfile(p); err != nil {
		return err
	}
	return a.Store.UpsertAssignment(&store.Assignment{ItemType: "profile", ItemID: p.ID, GroupID: a.Store.AllDevicesGroupID(), Intent: "install"})
}
