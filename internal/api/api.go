// Package api implements the administration REST API used by the web
// console and automation (API keys).
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeungalan/orchard-mdm/internal/apns"
	"github.com/yeungalan/orchard-mdm/internal/apps"
	"github.com/yeungalan/orchard-mdm/internal/compliance"
	"github.com/yeungalan/orchard-mdm/internal/config"
	"github.com/yeungalan/orchard-mdm/internal/ddm"
	"github.com/yeungalan/orchard-mdm/internal/dep"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/reconcile"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"github.com/yeungalan/orchard-mdm/internal/webhook"
)

// Deps are the services the API needs.
type Deps struct {
	Cfg        *config.Config
	Log        *slog.Logger
	Store      *store.Store
	Bus        *events.Bus
	APNs       *apns.Manager
	MDM        *mdm.Service
	DDM        *ddm.Service
	DEP        *dep.Service
	Reconciler *reconcile.Reconciler
	Compliance *compliance.Engine
	Webhooks   *webhook.Dispatcher
	ITunes     *apps.ITunes
	Version    string
	Started    time.Time
	TLSMode    string
}

// API serves /api.
type API struct {
	Deps

	setupMu    sync.Mutex
	setupToken string

	loginMu    sync.Mutex
	loginFails map[string][]time.Time
}

// New creates the API.
func New(d Deps) *API {
	return &API{Deps: d, loginFails: map[string][]time.Time{}}
}

// Perm is a permission level.
type Perm int

// Permission levels, each including the ones before it.
const (
	PermRead   Perm = iota // view everything
	PermAct                // run device actions and commands
	PermManage             // change configuration (profiles, apps, groups, policies, enrollment)
	PermAdmin              // users, API keys, server settings, certificates
)

// Roles maps role names to permission levels.
var Roles = map[string]Perm{"readonly": PermRead, "helpdesk": PermAct, "operator": PermManage, "admin": PermAdmin}

// RoleNames in display order.
var RoleNames = []string{"admin", "operator", "helpdesk", "readonly"}

// Principal is the authenticated caller.
type Principal struct {
	Kind        string `json:"kind"` // session|apikey
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	CSRF        string `json:"-"`
	SessionHash string `json:"-"`
}

// Actor names the principal for audit records.
func (p *Principal) Actor() string {
	if p.Kind == "apikey" {
		return "apikey:" + p.Name
	}
	return p.Name
}

// Error is an API error with an HTTP status.
type Error struct {
	Status int
	Msg    string
}

func (e *Error) Error() string { return e.Msg }

func errf(status int, format string, args ...any) error {
	return &Error{Status: status, Msg: fmt.Sprintf(format, args...)}
}

func badRequest(format string, args ...any) error {
	return errf(http.StatusBadRequest, format, args...)
}

type handler func(w http.ResponseWriter, r *http.Request, p *Principal) error

func (a *API) writeError(w http.ResponseWriter, err error) {
	var ae *Error
	switch {
	case errors.As(err, &ae):
		writeJSON(w, ae.Status, map[string]string{"error": ae.Msg})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		msg := err.Error()
		if strings.Contains(msg, "UNIQUE constraint failed") {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "an item with the same name or identifier already exists"})
			return
		}
		a.Log.Error("api error", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func ok(w http.ResponseWriter, v any) error {
	writeJSON(w, http.StatusOK, v)
	return nil
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 64<<20))
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return badRequest("request body is empty")
		}
		return badRequest("invalid JSON: %v", err)
	}
	return nil
}

func pathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		return 0, badRequest("invalid %s", name)
	}
	return id, nil
}

func queryInt(r *http.Request, name string, def int) int {
	if v := r.URL.Query().Get(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func (a *API) audit(r *http.Request, p *Principal, action, target string, details any) {
	d := ""
	switch t := details.(type) {
	case nil:
	case string:
		d = t
	default:
		b, _ := json.Marshal(t)
		d = string(b)
	}
	actor := "system"
	if p != nil {
		actor = p.Actor()
	}
	_ = a.Store.InsertAudit(&store.AuditEntry{Actor: actor, Action: action, Target: target, Details: d, IP: mdm.ClientIP(r, a.Cfg.TrustProxy)})
}

func (a *API) handle(mux *http.ServeMux, pattern string, perm Perm, h handler) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		p, err := a.authenticate(r)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "authentication required"})
			return
		}
		if Roles[p.Role] < perm {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "your role (" + p.Role + ") does not allow this action"})
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && p.Kind == "session" && r.Header.Get("X-CSRF-Token") != p.CSRF {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "invalid or missing CSRF token"})
			return
		}
		if err := h(w, r, p); err != nil {
			a.writeError(w, err)
		}
	})
}

func (a *API) public(mux *http.ServeMux, pattern string, h func(w http.ResponseWriter, r *http.Request) error) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			a.writeError(w, err)
		}
	})
}

// Register adds every API route.
func (a *API) Register(mux *http.ServeMux) {
	// authentication
	a.public(mux, "GET /api/auth/status", a.authStatus)
	a.public(mux, "POST /api/auth/login", a.login)
	a.public(mux, "POST /api/auth/setup", a.setup)
	a.handle(mux, "POST /api/auth/logout", PermRead, a.logout)
	a.handle(mux, "GET /api/auth/me", PermRead, a.me)
	a.handle(mux, "POST /api/auth/password", PermRead, a.changePassword)

	// dashboard & system
	a.handle(mux, "GET /api/dashboard", PermRead, a.dashboard)
	a.handle(mux, "GET /api/system", PermRead, a.system)
	a.handle(mux, "GET /api/variables", PermRead, a.variables)
	a.handle(mux, "GET /api/events", PermRead, a.listEvents)
	a.handle(mux, "GET /api/audit", PermAdmin, a.listAudit)

	// devices
	a.handle(mux, "GET /api/devices", PermRead, a.listDevices)
	a.handle(mux, "GET /api/devices/export.csv", PermRead, a.exportDevices)
	a.handle(mux, "GET /api/devices/facets", PermRead, a.deviceFacets)
	a.handle(mux, "POST /api/devices/bulk", PermAct, a.bulkDevices)
	a.handle(mux, "GET /api/devices/{udid}", PermRead, a.getDevice)
	a.handle(mux, "PATCH /api/devices/{udid}", PermAct, a.patchDevice)
	a.handle(mux, "DELETE /api/devices/{udid}", PermManage, a.deleteDevice)
	a.handle(mux, "GET /api/devices/{udid}/apps", PermRead, a.deviceApps)
	a.handle(mux, "GET /api/devices/{udid}/profiles", PermRead, a.deviceProfiles)
	a.handle(mux, "GET /api/devices/{udid}/certificates", PermRead, a.deviceCertificates)
	a.handle(mux, "GET /api/devices/{udid}/commands", PermRead, a.deviceCommands)
	a.handle(mux, "GET /api/devices/{udid}/events", PermRead, a.deviceEvents)
	a.handle(mux, "GET /api/devices/{udid}/telemetry", PermRead, a.deviceTelemetry)
	a.handle(mux, "GET /api/devices/{udid}/locations", PermRead, a.deviceLocations)
	a.handle(mux, "GET /api/devices/{udid}/declarations", PermRead, a.deviceDeclarations)
	a.handle(mux, "GET /api/devices/{udid}/assignments", PermRead, a.deviceAssignments)
	a.handle(mux, "GET /api/devices/{udid}/compliance", PermRead, a.deviceCompliance)
	a.handle(mux, "POST /api/devices/{udid}/commands", PermAct, a.deviceSendCommand)
	a.handle(mux, "POST /api/devices/{udid}/actions/{action}", PermAct, a.deviceAction)
	a.handle(mux, "GET /api/devices/{udid}/secrets", PermManage, a.deviceSecrets)

	// commands
	a.handle(mux, "GET /api/command-catalog", PermRead, a.commandCatalog)
	a.handle(mux, "GET /api/commands", PermRead, a.listCommands)
	a.handle(mux, "GET /api/commands/{uuid}", PermRead, a.getCommand)
	a.handle(mux, "DELETE /api/commands/{uuid}", PermAct, a.cancelCommand)

	// groups
	a.handle(mux, "GET /api/groups", PermRead, a.listGroups)
	a.handle(mux, "POST /api/groups", PermManage, a.createGroup)
	a.handle(mux, "POST /api/groups/preview", PermRead, a.previewGroup)
	a.handle(mux, "GET /api/group-rule-fields", PermRead, a.groupRuleFields)
	a.handle(mux, "GET /api/groups/{id}", PermRead, a.getGroup)
	a.handle(mux, "PUT /api/groups/{id}", PermManage, a.updateGroup)
	a.handle(mux, "DELETE /api/groups/{id}", PermManage, a.deleteGroup)
	a.handle(mux, "GET /api/groups/{id}/members", PermRead, a.groupMembers)
	a.handle(mux, "POST /api/groups/{id}/members", PermManage, a.addGroupMembers)
	a.handle(mux, "DELETE /api/groups/{id}/members", PermManage, a.removeGroupMembers)

	// assignments
	a.handle(mux, "GET /api/assignments", PermRead, a.listAssignments)
	a.handle(mux, "POST /api/assignments", PermManage, a.createAssignment)
	a.handle(mux, "DELETE /api/assignments/{id}", PermManage, a.deleteAssignment)

	// profiles
	a.handle(mux, "GET /api/profile-schemas", PermRead, a.profileSchemas)
	a.handle(mux, "GET /api/profiles", PermRead, a.listProfiles)
	a.handle(mux, "POST /api/profiles", PermManage, a.createProfile)
	a.handle(mux, "POST /api/profiles/upload", PermManage, a.uploadProfile)
	a.handle(mux, "GET /api/profiles/{id}", PermRead, a.getProfile)
	a.handle(mux, "PUT /api/profiles/{id}", PermManage, a.updateProfile)
	a.handle(mux, "DELETE /api/profiles/{id}", PermManage, a.deleteProfile)
	a.handle(mux, "GET /api/profiles/{id}/download", PermRead, a.downloadProfile)
	a.handle(mux, "GET /api/profiles/{id}/status", PermRead, a.profileStatus)
	a.handle(mux, "POST /api/profiles/{id}/retry", PermManage, a.retryProfile)

	// apps
	a.handle(mux, "GET /api/apps", PermRead, a.listApps)
	a.handle(mux, "GET /api/apps/search", PermRead, a.searchApps)
	a.handle(mux, "POST /api/apps", PermManage, a.createApp)
	a.handle(mux, "POST /api/apps/enterprise", PermManage, a.uploadEnterpriseApp)
	a.handle(mux, "GET /api/apps/{id}", PermRead, a.getApp)
	a.handle(mux, "PUT /api/apps/{id}", PermManage, a.updateApp)
	a.handle(mux, "DELETE /api/apps/{id}", PermManage, a.deleteApp)
	a.handle(mux, "GET /api/apps/{id}/status", PermRead, a.appStatus)
	a.handle(mux, "POST /api/apps/{id}/retry", PermManage, a.retryApp)
	a.handle(mux, "POST /api/apps/{id}/install", PermAct, a.installAppOnDevices)

	// declarative management
	a.handle(mux, "GET /api/declaration-templates", PermRead, a.declarationTemplates)
	a.handle(mux, "GET /api/declarations", PermRead, a.listDeclarations)
	a.handle(mux, "POST /api/declarations", PermManage, a.createDeclaration)
	a.handle(mux, "GET /api/declarations/{id}", PermRead, a.getDeclaration)
	a.handle(mux, "PUT /api/declarations/{id}", PermManage, a.updateDeclaration)
	a.handle(mux, "DELETE /api/declarations/{id}", PermManage, a.deleteDeclaration)

	// compliance
	a.handle(mux, "GET /api/compliance/policies", PermRead, a.listPolicies)
	a.handle(mux, "POST /api/compliance/policies", PermManage, a.createPolicy)
	a.handle(mux, "GET /api/compliance/policies/{id}", PermRead, a.getPolicy)
	a.handle(mux, "PUT /api/compliance/policies/{id}", PermManage, a.updatePolicy)
	a.handle(mux, "DELETE /api/compliance/policies/{id}", PermManage, a.deletePolicy)
	a.handle(mux, "GET /api/compliance/summary", PermRead, a.complianceSummary)
	a.handle(mux, "POST /api/compliance/evaluate", PermManage, a.complianceEvaluate)

	// enrollment
	a.handle(mux, "GET /api/enrollment/info", PermRead, a.enrollmentInfo)
	a.handle(mux, "GET /api/enrollment/tokens", PermRead, a.listEnrollmentTokens)
	a.handle(mux, "POST /api/enrollment/tokens", PermManage, a.createEnrollmentToken)
	a.handle(mux, "PUT /api/enrollment/tokens/{id}", PermManage, a.updateEnrollmentToken)
	a.handle(mux, "DELETE /api/enrollment/tokens/{id}", PermManage, a.deleteEnrollmentToken)
	a.handle(mux, "GET /api/enrollment/profile", PermManage, a.downloadEnrollmentProfile)
	a.handle(mux, "GET /api/enrollment/account", PermRead, a.accountEnrollment)
	a.handle(mux, "PUT /api/enrollment/account", PermAdmin, a.putAccountEnrollment)

	// automated device enrollment
	a.handle(mux, "GET /api/ade/servers", PermRead, a.listADEServers)
	a.handle(mux, "POST /api/ade/servers", PermAdmin, a.createADEServer)
	a.handle(mux, "PUT /api/ade/servers/{id}", PermAdmin, a.updateADEServer)
	a.handle(mux, "DELETE /api/ade/servers/{id}", PermAdmin, a.deleteADEServer)
	a.handle(mux, "GET /api/ade/servers/{id}/publickey", PermAdmin, a.adePublicKey)
	a.handle(mux, "POST /api/ade/servers/{id}/token", PermAdmin, a.adeUploadToken)
	a.handle(mux, "POST /api/ade/servers/{id}/sync", PermManage, a.adeSync)
	a.handle(mux, "POST /api/ade/servers/{id}/service-discovery", PermAdmin, a.adeServiceDiscovery)
	a.handle(mux, "GET /api/ade/devices", PermRead, a.listADEDevices)
	a.handle(mux, "GET /api/ade/profiles", PermRead, a.listADEProfiles)
	a.handle(mux, "POST /api/ade/profiles", PermManage, a.createADEProfile)
	a.handle(mux, "PUT /api/ade/profiles/{id}", PermManage, a.updateADEProfile)
	a.handle(mux, "DELETE /api/ade/profiles/{id}", PermManage, a.deleteADEProfile)
	a.handle(mux, "POST /api/ade/assign", PermManage, a.adeAssign)
	a.handle(mux, "POST /api/ade/unassign", PermManage, a.adeUnassign)

	// apps and books
	a.handle(mux, "GET /api/vpp/tokens", PermRead, a.listVPPTokens)
	a.handle(mux, "POST /api/vpp/tokens", PermAdmin, a.createVPPToken)
	a.handle(mux, "DELETE /api/vpp/tokens/{id}", PermAdmin, a.deleteVPPToken)
	a.handle(mux, "POST /api/vpp/tokens/{id}/sync", PermManage, a.syncVPPToken)
	a.handle(mux, "GET /api/vpp/assets", PermRead, a.listVPPAssets)

	// APNs
	a.handle(mux, "GET /api/apns", PermRead, a.apnsStatus)
	a.handle(mux, "POST /api/apns/csr", PermAdmin, a.apnsCSR)
	a.handle(mux, "GET /api/apns/csr", PermAdmin, a.apnsDownloadCSR)
	a.handle(mux, "POST /api/apns/vendor-sign", PermAdmin, a.apnsVendorSign)
	a.handle(mux, "POST /api/apns/mdmcert", PermAdmin, a.apnsMDMCertRequest)
	a.handle(mux, "POST /api/apns/mdmcert/decrypt", PermAdmin, a.apnsMDMCertDecrypt)
	a.handle(mux, "POST /api/apns/certificate", PermAdmin, a.apnsUploadCert)
	a.handle(mux, "POST /api/apns/test", PermAct, a.apnsTest)

	// administration
	a.handle(mux, "GET /api/settings", PermRead, a.getSettings)
	a.handle(mux, "PUT /api/settings", PermAdmin, a.putSettings)
	a.handle(mux, "GET /api/users", PermAdmin, a.listUsers)
	a.handle(mux, "POST /api/users", PermAdmin, a.createUserHandler)
	a.handle(mux, "PUT /api/users/{id}", PermAdmin, a.updateUser)
	a.handle(mux, "DELETE /api/users/{id}", PermAdmin, a.deleteUser)
	a.handle(mux, "GET /api/apikeys", PermAdmin, a.listAPIKeys)
	a.handle(mux, "POST /api/apikeys", PermAdmin, a.createAPIKey)
	a.handle(mux, "DELETE /api/apikeys/{id}", PermAdmin, a.deleteAPIKey)
	a.handle(mux, "GET /api/webhooks", PermRead, a.listWebhooks)
	a.handle(mux, "POST /api/webhooks", PermAdmin, a.createWebhook)
	a.handle(mux, "PUT /api/webhooks/{id}", PermAdmin, a.updateWebhook)
	a.handle(mux, "DELETE /api/webhooks/{id}", PermAdmin, a.deleteWebhook)
	a.handle(mux, "POST /api/webhooks/{id}/test", PermAdmin, a.testWebhook)
	a.handle(mux, "GET /api/pki/ca", PermRead, a.downloadCA)
	a.handle(mux, "GET /api/pki/issued", PermRead, a.listIssuedCerts)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown API endpoint " + r.Method + " " + r.URL.Path})
	})
}
