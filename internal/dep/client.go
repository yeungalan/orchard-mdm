// Package dep integrates with Apple Business/School Manager Automated Device
// Enrollment (ADE, formerly DEP).
package dep

import (
	"bufio"
	"bytes"
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smallstep/pkcs7"
)

// Tokens are the OAuth credentials inside an ADE server token.
type Tokens struct {
	ConsumerKey       string `json:"consumer_key"`
	ConsumerSecret    string `json:"consumer_secret"`
	AccessToken       string `json:"access_token"`
	AccessSecret      string `json:"access_secret"`
	AccessTokenExpiry string `json:"access_token_expiry"`
}

// Expiry returns the token expiry time.
func (t Tokens) Expiry() time.Time {
	ts, _ := time.Parse(time.RFC3339, t.AccessTokenExpiry)
	return ts
}

// DecryptToken decrypts the .p7m server token downloaded from Apple Business
// Manager using the server's key pair.
func DecryptToken(data []byte, cert *x509.Certificate, key crypto.PrivateKey) (*Tokens, error) {
	der, err := smimeBody(data)
	if err != nil {
		return nil, err
	}
	p7, err := pkcs7.Parse(der)
	if err != nil {
		return nil, fmt.Errorf("parse token: %w", err)
	}
	plain, err := p7.Decrypt(cert, key)
	if err != nil {
		return nil, fmt.Errorf("decrypt token (was it generated for this server's public key?): %w", err)
	}
	text := string(plain)
	start := strings.Index(text, "-----BEGIN MESSAGE-----")
	end := strings.Index(text, "-----END MESSAGE-----")
	if start < 0 || end < start {
		return nil, errors.New("decrypted token has no message block")
	}
	var t Tokens
	if err := json.Unmarshal([]byte(strings.TrimSpace(text[start+len("-----BEGIN MESSAGE-----"):end])), &t); err != nil {
		return nil, fmt.Errorf("token JSON: %w", err)
	}
	if t.ConsumerKey == "" || t.AccessToken == "" {
		return nil, errors.New("token is missing OAuth credentials")
	}
	return &t, nil
}

// smimeBody extracts the DER envelope from an S/MIME message (or accepts raw DER/base64).
func smimeBody(data []byte) ([]byte, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == 0x30 {
		return trimmed, nil
	}
	body := trimmed
	if bytes.Contains(trimmed[:min(len(trimmed), 512)], []byte("Content-Type")) {
		tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(trimmed)))
		if _, err := tp.ReadMIMEHeader(); err != nil {
			return nil, fmt.Errorf("read S/MIME headers: %w", err)
		}
		rest, _ := io.ReadAll(tp.R)
		body = rest
	}
	clean := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, string(body))
	der, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return nil, errors.New("token is not a valid S/MIME (.p7m) file")
	}
	return der, nil
}

// Client calls the ADE API.
type Client struct {
	BaseURL string
	Tokens  Tokens
	HTTP    *http.Client

	mu      sync.Mutex
	session string
}

// NewClient creates an ADE API client.
func NewClient(base string, t Tokens) *Client {
	if base == "" {
		base = "https://mdmenrollment.apple.com"
	}
	return &Client{BaseURL: strings.TrimRight(base, "/"), Tokens: t, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func pctEncode(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// oauthHeader builds the OAuth 1.0a Authorization header for the session request.
func oauthHeader(method, rawURL string, t Tokens, nonce, timestamp string) string {
	params := map[string]string{
		"oauth_consumer_key":     t.ConsumerKey,
		"oauth_token":            t.AccessToken,
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        timestamp,
		"oauth_nonce":            nonce,
		"oauth_version":          "1.0",
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var pairs []string
	for _, k := range keys {
		pairs = append(pairs, pctEncode(k)+"="+pctEncode(params[k]))
	}
	base := method + "&" + pctEncode(rawURL) + "&" + pctEncode(strings.Join(pairs, "&"))
	mac := hmac.New(sha1.New, []byte(pctEncode(t.ConsumerSecret)+"&"+pctEncode(t.AccessSecret)))
	mac.Write([]byte(base))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return fmt.Sprintf(`OAuth realm="ADM", oauth_consumer_key="%s", oauth_token="%s", oauth_signature_method="HMAC-SHA1", oauth_signature="%s", oauth_timestamp="%s", oauth_nonce="%s", oauth_version="1.0"`,
		pctEncode(t.ConsumerKey), pctEncode(t.AccessToken), pctEncode(sig), timestamp, nonce)
}

func (c *Client) authenticate(ctx context.Context) error {
	u := c.BaseURL + "/session"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	nb := make([]byte, 12)
	_, _ = rand.Read(nb)
	req.Header.Set("Authorization", oauthHeader(http.MethodGet, u, c.Tokens, hex.EncodeToString(nb), strconv.FormatInt(time.Now().Unix(), 10)))
	req.Header.Set("User-Agent", "orchard-mdm/1.0")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("ade session: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out struct {
		AuthSessionToken string `json:"auth_session_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AuthSessionToken == "" {
		return fmt.Errorf("ade session: unexpected response %s", strings.TrimSpace(string(body)))
	}
	c.mu.Lock()
	c.session = out.AuthSessionToken
	c.mu.Unlock()
	return nil
}

// APIError is an ADE error response.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("ade: %d %s", e.Status, e.Body) }

// IsCursorError reports whether the error means the sync cursor is unusable.
func IsCursorError(err error) bool {
	var e *APIError
	if !errors.As(err, &e) {
		return false
	}
	return strings.Contains(e.Body, "CURSOR")
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		c.mu.Lock()
		sess := c.session
		c.mu.Unlock()
		if sess == "" {
			if err := c.authenticate(ctx); err != nil {
				return err
			}
			c.mu.Lock()
			sess = c.session
			c.mu.Unlock()
		}
		var rdr io.Reader
		if body != nil {
			b, err := json.Marshal(body)
			if err != nil {
				return err
			}
			rdr = bytes.NewReader(b)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
		if err != nil {
			return err
		}
		req.Header.Set("X-ADM-Auth-Session", sess)
		req.Header.Set("X-Server-Protocol-Version", "3")
		req.Header.Set("Content-Type", "application/json;charset=UTF8")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "orchard-mdm/1.0")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		resp.Body.Close()
		if s := resp.Header.Get("X-ADM-Auth-Session"); s != "" {
			c.mu.Lock()
			c.session = s
			c.mu.Unlock()
		}
		if resp.StatusCode == http.StatusUnauthorized || (resp.StatusCode == http.StatusForbidden && bytes.Contains(data, []byte("TOKEN_EXPIRED"))) {
			c.mu.Lock()
			c.session = ""
			c.mu.Unlock()
			if attempt == 0 {
				continue
			}
		}
		if resp.StatusCode >= 300 {
			return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(data))}
		}
		if out != nil && len(data) > 0 {
			return json.Unmarshal(data, out)
		}
		return nil
	}
	return errors.New("ade: authentication failed")
}

// Account is the ADE server account information.
type Account struct {
	ServerName    string `json:"server_name"`
	ServerUUID    string `json:"server_uuid"`
	AdminID       string `json:"admin_id"`
	FacilitatorID string `json:"facilitator_id"`
	OrgName       string `json:"org_name"`
	OrgEmail      string `json:"org_email"`
	OrgPhone      string `json:"org_phone"`
	OrgAddress    string `json:"org_address"`
	OrgID         string `json:"org_id"`
	OrgType       string `json:"org_type"`
}

// Account fetches account details.
func (c *Client) Account(ctx context.Context) (*Account, error) {
	var a Account
	return &a, c.do(ctx, http.MethodGet, "/account", nil, &a)
}

// Device is an ADE device record.
type Device struct {
	SerialNumber       string `json:"serial_number"`
	Model              string `json:"model"`
	Description        string `json:"description"`
	Color              string `json:"color"`
	AssetTag           string `json:"asset_tag"`
	ProfileStatus      string `json:"profile_status"`
	ProfileUUID        string `json:"profile_uuid"`
	ProfileAssignTime  string `json:"profile_assign_time"`
	ProfilePushTime    string `json:"profile_push_time"`
	DeviceAssignedDate string `json:"device_assigned_date"`
	DeviceAssignedBy   string `json:"device_assigned_by"`
	OS                 string `json:"os"`
	DeviceFamily       string `json:"device_family"`
	OpType             string `json:"op_type"`
	OpDate             string `json:"op_date"`
}

// DeviceList is a page of devices.
type DeviceList struct {
	Devices      []Device `json:"devices"`
	Cursor       string   `json:"cursor"`
	MoreToFollow bool     `json:"more_to_follow"`
	FetchedUntil string   `json:"fetched_until"`
}

// FetchDevices returns a page of all devices.
func (c *Client) FetchDevices(ctx context.Context, cursor string) (*DeviceList, error) {
	req := map[string]any{"limit": 1000}
	if cursor != "" {
		req["cursor"] = cursor
	}
	var out DeviceList
	return &out, c.do(ctx, http.MethodPost, "/server/devices", req, &out)
}

// SyncDevices returns changes since the cursor.
func (c *Client) SyncDevices(ctx context.Context, cursor string) (*DeviceList, error) {
	var out DeviceList
	return &out, c.do(ctx, http.MethodPost, "/devices/sync", map[string]any{"cursor": cursor, "limit": 1000}, &out)
}

// ProfileResponse is returned when defining or assigning profiles.
type ProfileResponse struct {
	ProfileUUID string            `json:"profile_uuid"`
	Devices     map[string]string `json:"devices"`
}

// DefineProfile uploads an enrollment profile.
func (c *Client) DefineProfile(ctx context.Context, profile map[string]any) (*ProfileResponse, error) {
	var out ProfileResponse
	return &out, c.do(ctx, http.MethodPost, "/profile", profile, &out)
}

// AssignProfile assigns a profile to serial numbers.
func (c *Client) AssignProfile(ctx context.Context, uuid string, serials []string) (*ProfileResponse, error) {
	var out ProfileResponse
	return &out, c.do(ctx, http.MethodPut, "/profile/devices", map[string]any{"profile_uuid": uuid, "devices": serials}, &out)
}

// RemoveProfile unassigns profiles from serial numbers.
func (c *Client) RemoveProfile(ctx context.Context, serials []string) (map[string]string, error) {
	var out struct {
		Devices map[string]string `json:"devices"`
	}
	return out.Devices, c.do(ctx, http.MethodDelete, "/profile/devices", map[string]any{"devices": serials}, &out)
}

// DeviceDetails fetches details for serial numbers.
func (c *Client) DeviceDetails(ctx context.Context, serials []string) (map[string]Device, error) {
	var out struct {
		Devices map[string]Device `json:"devices"`
	}
	return out.Devices, c.do(ctx, http.MethodPost, "/devices", map[string]any{"devices": serials}, &out)
}

// AssignServiceDiscovery registers the account-driven enrollment service
// discovery URL with Apple Business Manager. Devices (iOS 18.2+) whose
// organization domain doesn't host the well-known file are redirected there.
func (c *Client) AssignServiceDiscovery(ctx context.Context, discoveryURL string) error {
	return c.do(ctx, http.MethodPost, "/account-driven-enrollment/profile", map[string]string{"mdm_service_discovery_url": discoveryURL}, nil)
}
