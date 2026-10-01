package server

import (
	"context"
	"fmt"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"github.com/yeungalan/orchard-mdm/internal/vpp"
)

type job struct {
	name     string
	interval func() time.Duration
	run      func(ctx context.Context)
	last     time.Time
}

func (a *App) jobs() []*job {
	minutes := func(key string, def int) func() time.Duration {
		return func() time.Duration {
			n := a.MDM.SettingInt(key)
			if n <= 0 {
				n = def
			}
			return time.Duration(n) * time.Minute
		}
	}
	fixed := func(d time.Duration) func() time.Duration { return func() time.Duration { return d } }
	return []*job{
		{name: "repush", interval: fixed(time.Minute), run: a.jobRepush},
		{name: "lost-mode-location", interval: fixed(time.Minute), run: a.jobLostModeLocation},
		{name: "reconcile", interval: fixed(5 * time.Minute), run: func(ctx context.Context) { a.Reconciler.ReconcileAll(ctx) }},
		{name: "compliance", interval: fixed(15 * time.Minute), run: func(context.Context) { a.Compliance.EvaluateAll() }},
		{name: "telemetry", interval: fixed(5 * time.Minute), run: a.jobTelemetry},
		{name: "inventory", interval: fixed(15 * time.Minute), run: a.jobInventory},
		{name: "ade-sync", interval: minutes(mdm.SettingADESyncMinutes, 30), run: func(ctx context.Context) { a.DEP.SyncAll(ctx) }},
		{name: "vpp-sync", interval: fixed(6 * time.Hour), run: a.jobVPPSync},
		{name: "maintenance", interval: fixed(6 * time.Hour), run: a.jobMaintenance},
	}
}

func (a *App) runScheduler(ctx context.Context) {
	jobs := a.jobs()
	// stagger the first runs so start-up stays quiet
	start := time.Now()
	for i, j := range jobs {
		j.last = start.Add(-j.interval()).Add(time.Duration(i*7+20) * time.Second)
	}
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			for _, j := range jobs {
				if now.Sub(j.last) < j.interval() {
					continue
				}
				j.last = now
				func() {
					defer func() {
						if r := recover(); r != nil {
							a.Log.Error("job panic", "job", j.name, "panic", r)
						}
					}()
					jctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
					defer cancel()
					t0 := time.Now()
					j.run(jctx)
					a.Log.Debug("job finished", "job", j.name, "dur", time.Since(t0).Round(time.Millisecond))
				}()
			}
		}
	}
}

// jobRepush re-notifies devices that still have undelivered commands.
func (a *App) jobRepush(ctx context.Context) {
	ids, err := a.Store.DevicesWithPendingCommands(store.Now() - 15*60)
	if err != nil || len(ids) == 0 {
		return
	}
	if len(ids) > 2000 {
		ids = ids[:2000]
	}
	a.MDM.PushNow(ctx, ids)
}

// jobLostModeLocation polls the location of devices in Lost Mode.
func (a *App) jobLostModeLocation(context.Context) {
	every := int64(a.MDM.SettingInt(mdm.SettingLostModeLocMinutes)) * 60
	if every <= 0 {
		return
	}
	devs, _, err := a.Store.ListDevices(store.DeviceFilter{Status: "enrolled"})
	if err != nil {
		return
	}
	var pushed []string
	for _, d := range devs {
		if !d.LostMode || store.Now()-d.LocationAt < every || a.Store.HasPendingCommand(d.UDID, "DeviceLocation", "") {
			continue
		}
		if _, err := a.MDM.Enqueue(d.UDID, map[string]any{"RequestType": "DeviceLocation"}, mdm.Meta{Source: "system", Ref: "lostmode-location"}); err == nil {
			pushed = append(pushed, d.UDID)
		}
	}
	a.MDM.Push(pushed...)
}

// jobTelemetry samples battery, storage and network state.
func (a *App) jobTelemetry(context.Context) {
	every := int64(a.MDM.SettingInt(mdm.SettingTelemetryMinutes)) * 60
	if every <= 0 {
		return
	}
	devs, err := a.Store.EnrolledDevices()
	if err != nil {
		return
	}
	var pushed []string
	for _, d := range devs {
		if store.Now()-d.TelemetryAt < every || !d.HasPushToken {
			continue
		}
		if a.MDM.QueueTelemetry(d.UDID) {
			pushed = append(pushed, d.UDID)
		}
		if len(pushed) >= 1000 {
			break
		}
	}
	a.MDM.Push(pushed...)
}

// jobInventory refreshes full inventory periodically.
func (a *App) jobInventory(context.Context) {
	every := int64(a.MDM.SettingInt(mdm.SettingInventoryHours)) * 3600
	if every <= 0 {
		return
	}
	devs, err := a.Store.EnrolledDevices()
	if err != nil {
		return
	}
	var pushed []string
	for _, d := range devs {
		if store.Now()-d.LastInventory < every || !d.HasPushToken {
			continue
		}
		if a.MDM.QueueInventory(d.UDID, "scheduled") > 0 {
			pushed = append(pushed, d.UDID)
		}
		if len(pushed) >= 500 {
			break
		}
	}
	a.MDM.Push(pushed...)
}

func (a *App) jobVPPSync(ctx context.Context) {
	tokens, err := a.Store.ListVPPTokens()
	if err != nil {
		return
	}
	for _, t := range tokens {
		if _, err := vpp.SyncToken(ctx, a.Store, a.Cfg.VPPURL, a.ITunes, t); err != nil {
			a.Log.Warn("vpp sync failed", "token", t.Name, "err", err)
		}
	}
}

// jobMaintenance prunes old data and warns about expiring certificates.
func (a *App) jobMaintenance(context.Context) {
	a.Store.CleanupExpired()
	a.Store.PruneAccountEnrollments(7 * 86400)
	day := int64(86400)
	if n := a.MDM.SettingInt(mdm.SettingCommandExpiryDays); n > 0 {
		if c, _ := a.Store.ExpireCommands(int64(n) * day); c > 0 {
			a.Log.Info("expired stale commands", "count", c)
		}
	}
	if n := a.MDM.SettingInt(mdm.SettingCommandRetention); n > 0 {
		_, _ = a.Store.PruneCommands(int64(n) * day)
		a.Store.PruneEvents(int64(n) * day)
	}
	if n := a.MDM.SettingInt(mdm.SettingTelemetryRetention); n > 0 {
		a.Store.PruneTelemetry(int64(n) * day)
	}
	info := a.APNs.CertInfo()
	if info.Configured && info.Error == "" && info.DaysLeft <= 30 {
		msg := fmt.Sprintf("The APNs push certificate expires in %d days (%s). Renew it with the same Apple ID at identity.apple.com.", info.DaysLeft, info.NotAfter.Format(time.DateOnly))
		_ = a.Store.InsertEvent(&store.Event{Type: "apns.cert_expiring", Level: "warn", Message: msg})
		a.Bus.Publish(events.PushCertExpiring, "", map[string]any{"days_left": info.DaysLeft, "not_after": info.NotAfter})
		a.Log.Warn(msg)
	}
}
