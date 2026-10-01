package vpp

import (
	"context"
	"fmt"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/apps"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

// SyncToken refreshes the asset list of a stored token and enriches names
// from the App Store.
func SyncToken(ctx context.Context, st *store.Store, baseURL string, it *apps.ITunes, t *store.VPPToken) (int, error) {
	c := New(baseURL, t.Token)
	assets, err := c.Assets(ctx)
	t.LastSync = time.Now().Unix()
	if err != nil {
		t.LastError = err.Error()
		_ = st.UpdateVPPToken(t)
		return 0, err
	}
	t.LastError = ""
	if cfg, err := c.GetClientConfig(ctx); err == nil && cfg.LocationName != "" {
		t.LocationName = cfg.LocationName
	}
	_ = st.UpdateVPPToken(t)

	prev, _ := st.ListVPPAssets(t.ID)
	names := map[string][2]string{}
	for _, a := range prev {
		if a.Name != "" {
			names[a.AdamID] = [2]string{a.Name, a.IconURL}
		}
	}
	var missing []string
	out := make([]*store.VPPAsset, 0, len(assets))
	for _, a := range assets {
		rec := &store.VPPAsset{VPPTokenID: t.ID, AdamID: a.AdamID, PricingParam: a.PricingParam, ProductType: a.ProductType,
			AvailableCount: a.AvailableCount, AssignedCount: a.AssignedCount, TotalCount: a.TotalCount, RetiredCount: a.RetiredCount,
			DeviceAssignable: a.DeviceAssignable, Revocable: a.Revocable}
		if n, ok := names[a.AdamID]; ok {
			rec.Name, rec.IconURL = n[0], n[1]
		} else if a.ProductType == "App" || a.ProductType == "" {
			missing = append(missing, a.AdamID)
		}
		out = append(out, rec)
	}
	if err := st.ReplaceVPPAssets(t.ID, out); err != nil {
		return 0, err
	}
	if it != nil {
		for i := 0; i < len(missing); i += 100 {
			end := min(i+100, len(missing))
			res, err := it.Lookup(ctx, missing[i:end], "")
			if err != nil {
				break
			}
			for _, app := range res {
				st.UpdateVPPAssetName(fmt.Sprint(app.TrackID), app.TrackName, app.Icon())
			}
		}
	}
	return len(out), nil
}
