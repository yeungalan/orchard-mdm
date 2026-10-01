package dep

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/smallstep/pkcs7"
	"github.com/yeungalan/orchard-mdm/internal/events"
	"github.com/yeungalan/orchard-mdm/internal/mdm"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"howett.net/plist"
)

// Service manages ADE servers, devices and profiles.
type Service struct {
	store   *store.Store
	mdm     *mdm.Service
	baseURL string
	log     *slog.Logger
	bus     *events.Bus
	mu      sync.Mutex // serialises syncs

	// NewClient can be replaced in tests.
	NewClient func(base string, t Tokens) *Client
}

// New creates the ADE service.
func New(s *store.Store, m *mdm.Service, baseURL string, log *slog.Logger) *Service {
	return &Service{store: s, mdm: m, baseURL: baseURL, log: log, bus: m.Bus(), NewClient: NewClient}
}

// CreateServer creates a new ADE server with a fresh key pair whose public
// certificate is uploaded to Apple Business Manager.
func (s *Service) CreateServer(name string) (*store.DEPServer, error) {
	if strings.TrimSpace(name) == "" {
		name = "Apple Business Manager"
	}
	cert, key, err := pki.SelfSigned("Orchard MDM ADE "+name, 3650)
	if err != nil {
		return nil, err
	}
	kp, _ := pki.KeyPEM(key)
	srv := &store.DEPServer{Name: name, CertPEM: pki.CertPEM(cert), KeyPEM: kp}
	if err := s.store.CreateDEPServer(srv); err != nil {
		return nil, err
	}
	return s.store.GetDEPServer(srv.ID)
}

func (s *Service) client(srv *store.DEPServer) (*Client, error) {
	if srv.ConsumerKey == "" || srv.AccessToken == "" {
		return nil, errors.New("no server token uploaded yet")
	}
	return s.NewClient(s.baseURL, Tokens{ConsumerKey: srv.ConsumerKey, ConsumerSecret: srv.ConsumerSecret, AccessToken: srv.AccessToken, AccessSecret: srv.AccessSecret}), nil
}

// UploadToken decrypts and stores a server token, then reads the account.
func (s *Service) UploadToken(ctx context.Context, id int64, data []byte) (*store.DEPServer, error) {
	srv, err := s.store.GetDEPServer(id)
	if err != nil {
		return nil, err
	}
	cert, err := pki.ParseCertPEM([]byte(srv.CertPEM))
	if err != nil {
		return nil, err
	}
	key, err := pki.ParseKeyPEM([]byte(srv.KeyPEM))
	if err != nil {
		return nil, err
	}
	t, err := DecryptToken(data, cert, key)
	if err != nil {
		return nil, err
	}
	srv.ConsumerKey, srv.ConsumerSecret, srv.AccessToken, srv.AccessSecret = t.ConsumerKey, t.ConsumerSecret, t.AccessToken, t.AccessSecret
	srv.AccessTokenExpiry = t.Expiry().Unix()
	srv.Cursor, srv.LastError = "", ""
	c, _ := s.client(srv)
	if acct, err := c.Account(ctx); err == nil {
		srv.ServerName, srv.ServerUUID, srv.AdminID = acct.ServerName, acct.ServerUUID, acct.AdminID
		srv.OrgName, srv.OrgEmail, srv.OrgPhone, srv.OrgAddress, srv.OrgID = acct.OrgName, acct.OrgEmail, acct.OrgPhone, acct.OrgAddress, acct.OrgID
	} else {
		srv.LastError = "token stored, but reading the account failed: " + err.Error()
	}
	if err := s.store.UpdateDEPServer(srv); err != nil {
		return nil, err
	}
	return s.store.GetDEPServer(id)
}

// SyncResult summarises a sync.
type SyncResult struct {
	Added    int `json:"added"`
	Modified int `json:"modified"`
	Removed  int `json:"removed"`
	Assigned int `json:"assigned"`
}

// SyncAll syncs every server with a token.
func (s *Service) SyncAll(ctx context.Context) {
	servers, err := s.store.ListDEPServers()
	if err != nil {
		return
	}
	for _, srv := range servers {
		if !srv.HasToken {
			continue
		}
		if _, err := s.Sync(ctx, srv.ID); err != nil {
			s.log.Warn("ade sync failed", "server", srv.Name, "err", err)
		}
	}
}

// Sync fetches device changes for one server.
func (s *Service) Sync(ctx context.Context, id int64) (*SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	srv, err := s.store.GetDEPServer(id)
	if err != nil {
		return nil, err
	}
	c, err := s.client(srv)
	if err != nil {
		return nil, err
	}
	res := &SyncResult{}
	var newSerials []string
	apply := func(list *DeviceList, full bool) {
		for _, d := range list.Devices {
			if d.SerialNumber == "" {
				continue
			}
			prev, prevErr := s.store.GetDEPDevice(d.SerialNumber)
			if strings.EqualFold(d.OpType, "deleted") {
				_ = s.store.DeleteDEPDevice(d.SerialNumber)
				res.Removed++
				s.bus.Publish(events.ADEDeviceRemoved, "", map[string]any{"serial_number": d.SerialNumber})
				continue
			}
			rec := &store.DEPDevice{SerialNumber: d.SerialNumber, DEPServerID: srv.ID, Model: d.Model, Description: d.Description, Color: d.Color, OS: d.OS,
				DeviceFamily: d.DeviceFamily, AssetTag: d.AssetTag, ProfileStatus: d.ProfileStatus, ProfileUUID: d.ProfileUUID, ProfileAssignTime: d.ProfileAssignTime,
				ProfilePushTime: d.ProfilePushTime, DeviceAssignedDate: d.DeviceAssignedDate, DeviceAssignedBy: d.DeviceAssignedBy, OpType: d.OpType, OpDate: d.OpDate}
			if prevErr == nil {
				rec.AssignedProfileID = prev.AssignedProfileID
				if !full && d.ProfileStatus == "" {
					rec.ProfileStatus, rec.ProfileUUID = prev.ProfileStatus, prev.ProfileUUID
				}
				res.Modified++
			} else {
				res.Added++
				s.bus.Publish(events.ADEDeviceAdded, "", map[string]any{"serial_number": d.SerialNumber, "model": d.Model, "description": d.Description})
			}
			if rec.ProfileUUID != "" && rec.AssignedProfileID == 0 {
				rec.AssignedProfileID = s.store.DEPProfileIDByUUID(rec.ProfileUUID)
			}
			_ = s.store.UpsertDEPDevice(rec)
			if (rec.ProfileUUID == "" || rec.ProfileStatus == "empty") && prevErr != nil {
				newSerials = append(newSerials, d.SerialNumber)
			}
		}
	}
	cursor := srv.Cursor
	fetch := func() error {
		cursor = ""
		for i := 0; i < 1000; i++ {
			list, err := c.FetchDevices(ctx, cursor)
			if err != nil {
				return err
			}
			apply(list, true)
			cursor = list.Cursor
			if !list.MoreToFollow {
				return nil
			}
		}
		return nil
	}
	if cursor == "" {
		err = fetch()
	} else {
		for i := 0; i < 1000; i++ {
			list, e := c.SyncDevices(ctx, cursor)
			if e != nil {
				if IsCursorError(e) {
					err = fetch()
				} else {
					err = e
				}
				break
			}
			apply(list, false)
			if list.Cursor != "" {
				cursor = list.Cursor
			}
			if !list.MoreToFollow {
				break
			}
		}
	}
	srv.LastSync = store.Now()
	srv.Cursor = cursor
	if err != nil {
		srv.LastError = err.Error()
	} else {
		srv.LastError = ""
	}
	_ = s.store.UpdateDEPServer(srv)
	if err != nil {
		return res, err
	}
	if srv.DefaultProfileID > 0 && len(newSerials) > 0 {
		n, aerr := s.assignLocked(ctx, srv.DefaultProfileID, newSerials)
		res.Assigned = n
		if aerr != nil {
			s.log.Warn("ade auto-assign failed", "err", aerr)
		}
	}
	s.log.Info("ade sync", "server", srv.Name, "added", res.Added, "modified", res.Modified, "removed", res.Removed, "assigned", res.Assigned)
	return res, nil
}

// ProfileJSON returns the ADE profile document sent to Apple.
func (s *Service) ProfileJSON(p *store.DEPProfile) (map[string]any, error) {
	base := s.mdm.PublicURL()
	if base == "" {
		return nil, mdm.ErrNoPublicURL
	}
	out := map[string]any{
		"profile_name":            p.Name,
		"url":                     base + "/mdm/ade/enroll",
		"allow_pairing":           true,
		"is_supervised":           true,
		"is_mandatory":            true,
		"is_mdm_removable":        false,
		"await_device_configured": false,
		"skip_setup_items":        []string{},
	}
	for k, v := range p.Config {
		switch k {
		case "url", "devices":
			continue
		}
		out[k] = v
	}
	if out["profile_name"] == "" {
		out["profile_name"] = p.Name
	}
	if org := s.mdm.Setting(mdm.SettingOrgName); out["department"] == nil && org != "" {
		out["department"] = org
	}
	if anchors, ok := out["anchor_certs"].([]any); ok && len(anchors) == 0 {
		delete(out, "anchor_certs")
	}
	return out, nil
}

func (s *Service) ensureUploaded(ctx context.Context, c *Client, srv *store.DEPServer, p *store.DEPProfile) (string, error) {
	doc, err := s.ProfileJSON(p)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(doc)
	sum := sha256.Sum256(b)
	hash := hex.EncodeToString(sum[:])
	if uuid, prevHash := s.store.GetDEPProfileUpload(p.ID, srv.ID); uuid != "" && prevHash == hash {
		return uuid, nil
	}
	resp, err := c.DefineProfile(ctx, doc)
	if err != nil {
		return "", err
	}
	if resp.ProfileUUID == "" {
		return "", errors.New("Apple did not return a profile UUID")
	}
	if err := s.store.SetDEPProfileUpload(p.ID, srv.ID, resp.ProfileUUID, hash); err != nil {
		return "", err
	}
	return resp.ProfileUUID, nil
}

// AssignProfile assigns an ADE profile to serial numbers (across servers).
func (s *Service) AssignProfile(ctx context.Context, profileID int64, serials []string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.assignLocked(ctx, profileID, serials)
}

func (s *Service) assignLocked(ctx context.Context, profileID int64, serials []string) (int, error) {
	p, err := s.store.GetDEPProfile(profileID)
	if err != nil {
		return 0, err
	}
	byServer := map[int64][]string{}
	for _, sn := range serials {
		d, err := s.store.GetDEPDevice(sn)
		if err != nil {
			continue
		}
		byServer[d.DEPServerID] = append(byServer[d.DEPServerID], sn)
	}
	if len(byServer) == 0 {
		return 0, errors.New("none of the serial numbers are assigned to an ADE server here")
	}
	assigned := 0
	var errs []string
	ids := make([]int64, 0, len(byServer))
	for id := range byServer {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, sid := range ids {
		srv, err := s.store.GetDEPServer(sid)
		if err != nil {
			continue
		}
		c, err := s.client(srv)
		if err != nil {
			errs = append(errs, srv.Name+": "+err.Error())
			continue
		}
		uuid, err := s.ensureUploaded(ctx, c, srv, p)
		if err != nil {
			errs = append(errs, srv.Name+": "+err.Error())
			continue
		}
		resp, err := c.AssignProfile(ctx, uuid, byServer[sid])
		if err != nil {
			errs = append(errs, srv.Name+": "+err.Error())
			continue
		}
		for sn, status := range resp.Devices {
			if status == "SUCCESS" {
				assigned++
				if d, err := s.store.GetDEPDevice(sn); err == nil {
					d.AssignedProfileID, d.ProfileUUID, d.ProfileStatus = p.ID, uuid, "assigned"
					d.ProfileAssignTime = time.Now().UTC().Format(time.RFC3339)
					_ = s.store.UpsertDEPDevice(d)
				}
			} else {
				errs = append(errs, sn+": "+status)
			}
		}
	}
	if len(errs) > 0 {
		return assigned, fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return assigned, nil
}

// UnassignProfile removes profile assignments.
func (s *Service) UnassignProfile(ctx context.Context, serials []string) error {
	byServer := map[int64][]string{}
	for _, sn := range serials {
		if d, err := s.store.GetDEPDevice(sn); err == nil {
			byServer[d.DEPServerID] = append(byServer[d.DEPServerID], sn)
		}
	}
	for sid, list := range byServer {
		srv, err := s.store.GetDEPServer(sid)
		if err != nil {
			continue
		}
		c, err := s.client(srv)
		if err != nil {
			return err
		}
		if _, err := c.RemoveProfile(ctx, list); err != nil {
			return err
		}
		for _, sn := range list {
			if d, err := s.store.GetDEPDevice(sn); err == nil {
				d.AssignedProfileID, d.ProfileUUID, d.ProfileStatus = 0, "", "empty"
				_ = s.store.UpsertDEPDevice(d)
			}
		}
	}
	return nil
}

// EnrollHandler serves the ADE enrollment URL. Devices POST a (CMS signed)
// plist with their identifiers and receive the enrollment profile.
func (s *Service) EnrollHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	content := body
	if p7, err := pkcs7.Parse(body); err == nil && len(p7.Content) > 0 {
		content = p7.Content
	}
	var info map[string]any
	if _, err := plist.Unmarshal(content, &info); err != nil {
		http.Error(w, "invalid device information", http.StatusBadRequest)
		return
	}
	serial, _ := info["SERIAL"].(string)
	udid, _ := info["UDID"].(string)
	if serial == "" {
		http.Error(w, "missing serial number", http.StatusBadRequest)
		return
	}
	if _, err := s.store.GetDEPDevice(serial); err != nil && !s.mdm.SettingBool("ade_allow_unknown_serials") {
		s.log.Warn("ade enrollment from unknown serial", "serial", serial, "ip", mdm.ClientIP(r, false))
		http.Error(w, "this device is not assigned to this MDM server", http.StatusForbidden)
		return
	}
	prof, err := s.mdm.EnrollmentProfile(mdm.EnrollmentOptions{Ref: "ade", ChallengeTTL: time.Hour})
	if err != nil {
		s.log.Error("ade enrollment profile", "err", err)
		http.Error(w, "enrollment is not available: "+err.Error(), http.StatusServiceUnavailable)
		return
	}
	_ = s.store.InsertEvent(&store.Event{DeviceID: udid, Type: "ade.enroll", Message: "Automated Device Enrollment started for " + serial})
	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	_, _ = w.Write(prof)
}

// AssignServiceDiscovery points Apple Business Manager's account-driven
// enrollment service discovery at this server.
func (s *Service) AssignServiceDiscovery(ctx context.Context, id int64) (string, error) {
	srv, err := s.store.GetDEPServer(id)
	if err != nil {
		return "", err
	}
	c, err := s.client(srv)
	if err != nil {
		return "", err
	}
	base := s.mdm.PublicURL()
	if base == "" {
		return "", mdm.ErrNoPublicURL
	}
	u := base + "/.well-known/com.apple.remotemanagement"
	if err := c.AssignServiceDiscovery(ctx, u); err != nil {
		return "", err
	}
	_ = s.store.SetSetting(fmt.Sprintf("ade_service_discovery_%d", id), u)
	return u, nil
}
