package devicesim

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/smallstep/pkcs7"
	"howett.net/plist"
)

// Authenticator performs the web-authentication step of account-driven
// enrollment (what ASWebAuthenticationSession shows the user) and returns the
// access token from the apple-remotemanagement-user-login callback.
type Authenticator func(client *http.Client, authURL string) (string, error)

var wwwAuthRE = regexp.MustCompile(`method="([^"]+)",\s*url="([^"]+)"`)

// AccountEnroll performs account-driven enrollment ("Sign In to Work or
// School Account"): service discovery against server, the unauthenticated
// enrollment request, web authentication, the authorized request and finally
// the enrollment profile installation.
func (d *Device) AccountEnroll(server, userIdentifier string, auth Authenticator) error {
	family := "iPhone"
	if strings.HasPrefix(d.ProductName, "iPad") {
		family = "iPad"
	}
	q := url.Values{"user-identifier": {userIdentifier}, "model-family": {family}}
	resp, err := d.HTTP.Get(strings.TrimRight(server, "/") + "/.well-known/com.apple.remotemanagement?" + q.Encode())
	if err != nil {
		return err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("service discovery: %s", resp.Status)
	}
	var disc struct {
		Servers []struct{ Version, BaseURL string }
	}
	if err := json.Unmarshal(body, &disc); err != nil || len(disc.Servers) == 0 || disc.Servers[0].BaseURL == "" {
		return fmt.Errorf("service discovery: malformed response %s", body)
	}
	srv := disc.Servers[0]
	if srv.Version != "mdm-byod" && srv.Version != "mdm-adde" {
		return fmt.Errorf("service discovery: unknown version %q", srv.Version)
	}
	reqBody, err := d.signedMachineInfo()
	if err != nil {
		return err
	}
	resp, err = d.postEnroll(srv.BaseURL, reqBody, "")
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("first enrollment attempt: expected 401, got %s", resp.Status)
	}
	m := wwwAuthRE.FindStringSubmatch(resp.Header.Get("WWW-Authenticate"))
	if !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer ") || m == nil || m[1] != "apple-as-web" {
		return fmt.Errorf("first enrollment attempt: bad WWW-Authenticate %q", resp.Header.Get("WWW-Authenticate"))
	}
	authURL, _ := url.Parse(m[2])
	aq := authURL.Query()
	aq.Set("user-identifier", userIdentifier)
	authURL.RawQuery = aq.Encode()
	token, err := auth(d.webClient(), authURL.String())
	if err != nil {
		return fmt.Errorf("web authentication: %w", err)
	}
	resp, err = d.postEnroll(srv.BaseURL, reqBody, token)
	if err != nil {
		return err
	}
	prof, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/x-apple-aspen-config" {
		return fmt.Errorf("second enrollment attempt: %s %s", resp.Status, prof)
	}
	if err := d.Enroll(prof); err != nil {
		return err
	}
	want := "BYOD"
	if srv.Version == "mdm-adde" {
		want = "ADDE"
	}
	if d.EnrollmentMode != want || !strings.EqualFold(d.ManagedAppleID, userIdentifier) {
		return fmt.Errorf("profile has EnrollmentMode %q for %q, expected %q for %q", d.EnrollmentMode, d.ManagedAppleID, want, userIdentifier)
	}
	return nil
}

// signedMachineInfo builds the CMS-signed LANGUAGE/PRODUCT/VERSION body.
func (d *Device) signedMachineInfo() ([]byte, error) {
	info, _ := plist.Marshal(map[string]string{"LANGUAGE": "en-US", "PRODUCT": d.ProductName, "VERSION": d.BuildVersion}, plist.XMLFormat)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "Apple iPhone Device CA (simulated)"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert, _ := x509.ParseCertificate(der)
	sd, err := pkcs7.NewSignedData(info)
	if err != nil {
		return nil, err
	}
	if err := sd.AddSigner(cert, key, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, err
	}
	return sd.Finish()
}

func (d *Device) postEnroll(baseURL string, body []byte, token string) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, baseURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/pkcs7-signature")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return d.HTTP.Do(req)
}

// webClient behaves like the authentication web view: it keeps cookies,
// follows ordinary redirects and stops at the callback scheme.
func (d *Device) webClient() *http.Client {
	c := *d.HTTP
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme == "apple-remotemanagement-user-login" {
			return http.ErrUseLastResponse
		}
		if len(via) > 10 {
			return errors.New("too many redirects")
		}
		return nil
	}
	return &c
}

// TokenFromCallback extracts the access token from a completed authentication response.
func TokenFromCallback(resp *http.Response) (string, error) {
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || loc.Scheme != "apple-remotemanagement-user-login" || loc.Host != "authentication-results" {
		return "", fmt.Errorf("authentication did not complete (%s, Location %q)", resp.Status, resp.Header.Get("Location"))
	}
	if resp.StatusCode != http.StatusPermanentRedirect {
		return "", fmt.Errorf("authentication result must be a 308 redirect, got %s", resp.Status)
	}
	tok := loc.Query().Get("access-token")
	if tok == "" {
		return "", errors.New("callback has no access-token")
	}
	return tok, nil
}

// CodeAuth signs in with an enrollment code on the server's sign-in page.
func CodeAuth(code string) Authenticator {
	return func(client *http.Client, authURL string) (string, error) {
		resp, err := client.Get(authURL)
		if err != nil {
			return "", err
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 || !bytes.Contains(page, []byte(`name="code"`)) {
			return "", fmt.Errorf("sign-in page: %s", resp.Status)
		}
		u, _ := url.Parse(authURL)
		form := url.Values{"code": {code}, "user-identifier": {u.Query().Get("user-identifier")}}
		resp, err = client.PostForm(u.Scheme+"://"+u.Host+u.Path, form)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		return TokenFromCallback(resp)
	}
}

// LinkAuth follows the first link whose href contains match (for example the
// single sign-on button) and expects the redirect chain to end at the callback.
func LinkAuth(match string) Authenticator {
	return func(client *http.Client, authURL string) (string, error) {
		resp, err := client.Get(authURL)
		if err != nil {
			return "", err
		}
		page, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		m := regexp.MustCompile(`href="([^"]*` + regexp.QuoteMeta(match) + `[^"]*)"`).FindSubmatch(page)
		if m == nil {
			return "", fmt.Errorf("no link containing %q on the sign-in page", match)
		}
		u, _ := url.Parse(authURL)
		next, _ := u.Parse(strings.ReplaceAll(string(m[1]), "&amp;", "&"))
		resp, err = client.Get(next.String())
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		return TokenFromCallback(resp)
	}
}
