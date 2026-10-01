package apns

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/smallstep/pkcs7"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
	"howett.net/plist"
	pkcs12 "software.sslmate.com/src/go-pkcs12"
)

const (
	pendingKeyName = "apns_pending"
	previousName   = "apns_previous"
	mdmcertKeyName = "mdmcert_encrypt"

	// MDMCertURL is the community mdmcert.download signing service.
	MDMCertURL = "https://mdmcert.download/api/v1/signrequest"
	// mdmcertAPIKey is the public API key used by open source MDM servers
	// (MicroMDM, NanoMDM, Commandment) for mdmcert.download.
	mdmcertAPIKey = "f847aea2ba06b41264d587b229e2712c89b1490a1208b7ff1aafab5bb40d47bc"
)

// Fallback Apple CA locations used when a vendor certificate carries no AIA URL.
var appleCAFallbacks = []string{
	"https://www.apple.com/certificateauthority/AppleWWDRCAG3.cer",
	"https://www.apple.com/appleca/AppleIncRootCertificate.cer",
}

// CSRRequest describes the subject of a new push CSR.
type CSRRequest struct {
	Email   string `json:"email"`
	Org     string `json:"org"`
	Country string `json:"country"`
	CN      string `json:"cn"`
}

// GenerateCSR creates a new push key and CSR and keeps the key until the
// signed certificate is uploaded.
func (m *Manager) GenerateCSR(r CSRRequest) ([]byte, error) {
	key, err := pki.GenerateKey(2048)
	if err != nil {
		return nil, err
	}
	if r.CN == "" {
		r.CN = "Orchard MDM Push"
	}
	csr, err := pki.NewCSR(key, r.CN, r.Email, r.Org, r.Country)
	if err != nil {
		return nil, err
	}
	keyPEM, err := pki.KeyPEM(key)
	if err != nil {
		return nil, err
	}
	meta, _ := json.Marshal(map[string]string{"csr": string(csr), "email": r.Email})
	if err := m.store.PutKeyPair(&store.KeyPair{Name: pendingKeyName, KeyPEM: keyPEM, Meta: string(meta)}); err != nil {
		return nil, err
	}
	return csr, nil
}

// PendingCSR returns the pending CSR PEM, if any.
func (m *Manager) PendingCSR() ([]byte, error) {
	kp, err := m.store.GetKeyPair(pendingKeyName)
	if err != nil {
		return nil, errors.New("no pending CSR; generate one first")
	}
	var meta struct {
		CSR string `json:"csr"`
	}
	if err := json.Unmarshal([]byte(kp.Meta), &meta); err != nil || meta.CSR == "" {
		return nil, errors.New("pending CSR is unreadable; generate a new one")
	}
	return []byte(meta.CSR), nil
}

type pushCertRequest struct {
	PushCertRequestCSR       string
	PushCertCertificateChain string
	PushCertSignature        string
}

// VendorSign signs the pending push CSR with an MDM vendor certificate (from
// the Apple Developer Program) and returns the base64 request file to upload
// at https://identity.apple.com.
func (m *Manager) VendorSign(ctx context.Context, vendorCert *x509.Certificate, vendorKey crypto.Signer, extraChain []*x509.Certificate) ([]byte, error) {
	csrPEM, err := m.PendingCSR()
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return nil, errors.New("invalid pending CSR")
	}
	return BuildVendorRequest(ctx, block.Bytes, vendorCert, vendorKey, extraChain)
}

// BuildVendorRequest produces the Apple push certificate request for a CSR.
func BuildVendorRequest(ctx context.Context, csrDER []byte, vendorCert *x509.Certificate, vendorKey crypto.Signer, extraChain []*x509.Certificate) ([]byte, error) {
	if !pki.KeyMatches(vendorCert, vendorKey) {
		return nil, errors.New("vendor private key does not match the vendor certificate")
	}
	rsaKey, ok := vendorKey.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("vendor key must be RSA")
	}
	sum := sha256.Sum256(csrDER)
	sig, err := rsa.SignPKCS1v15(rand.Reader, rsaKey, crypto.SHA256, sum[:])
	if err != nil {
		return nil, err
	}
	chain := []*x509.Certificate{vendorCert}
	chain = append(chain, extraChain...)
	if len(extraChain) == 0 {
		chain = append(chain, fetchIssuerChain(ctx, vendorCert)...)
	}
	var pemChain strings.Builder
	for _, c := range chain {
		pemChain.WriteString(pki.CertPEM(c))
	}
	req := pushCertRequest{
		PushCertRequestCSR:       base64.StdEncoding.EncodeToString(csrDER),
		PushCertCertificateChain: pemChain.String(),
		PushCertSignature:        base64.StdEncoding.EncodeToString(sig),
	}
	xml, err := plist.MarshalIndent(req, plist.XMLFormat, "\t")
	if err != nil {
		return nil, err
	}
	return []byte(base64.StdEncoding.EncodeToString(xml)), nil
}

// fetchIssuerChain follows Authority Information Access URLs up to the root.
func fetchIssuerChain(ctx context.Context, leaf *x509.Certificate) []*x509.Certificate {
	var out []*x509.Certificate
	cur := leaf
	client := &http.Client{Timeout: 15 * time.Second}
	for i := 0; i < 4; i++ {
		if bytes.Equal(cur.RawIssuer, cur.RawSubject) {
			break
		}
		urls := cur.IssuingCertificateURL
		if len(urls) == 0 && i < len(appleCAFallbacks) {
			urls = []string{appleCAFallbacks[i]}
		}
		var next *x509.Certificate
		for _, u := range urls {
			if c := fetchCert(ctx, client, u); c != nil {
				next = c
				break
			}
		}
		if next == nil {
			break
		}
		out = append(out, next)
		cur = next
	}
	return out
}

func fetchCert(ctx context.Context, client *http.Client, url string) *x509.Certificate {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil
	}
	if c, err := pki.ParseCertPEM(b); err == nil {
		return c
	}
	if p7, err := pkcs7.Parse(b); err == nil && len(p7.Certificates) > 0 {
		return p7.Certificates[0]
	}
	return nil
}

// ParseVendorP12 decodes a vendor certificate bundle (.p12).
func ParseVendorP12(data []byte, password string) (*x509.Certificate, crypto.Signer, []*x509.Certificate, error) {
	key, cert, chain, err := pkcs12.DecodeChain(data, password)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("decode p12: %w", err)
	}
	signer, ok := key.(crypto.Signer)
	if !ok {
		return nil, nil, nil, errors.New("p12 key is not a signing key")
	}
	return cert, signer, chain, nil
}

// MDMCertEncryptionCert returns (creating if needed) the certificate that
// mdmcert.download encrypts its response to.
func (m *Manager) MDMCertEncryptionCert() (*x509.Certificate, crypto.Signer, error) {
	if kp, err := m.store.GetKeyPair(mdmcertKeyName); err == nil {
		c, err1 := pki.ParseCertPEM([]byte(kp.CertPEM))
		k, err2 := pki.ParseKeyPEM([]byte(kp.KeyPEM))
		if err1 == nil && err2 == nil && time.Now().Before(c.NotAfter) {
			return c, k, nil
		}
	}
	c, k, err := pki.SelfSigned("Orchard MDM mdmcert.download", 365)
	if err != nil {
		return nil, nil, err
	}
	kpem, _ := pki.KeyPEM(k)
	if err := m.store.PutKeyPair(&store.KeyPair{Name: mdmcertKeyName, CertPEM: pki.CertPEM(c), KeyPEM: kpem}); err != nil {
		return nil, nil, err
	}
	return c, k, nil
}

// RequestMDMCertDownload submits the pending CSR to mdmcert.download. The
// signed request is emailed to the given address, encrypted to our key.
func (m *Manager) RequestMDMCertDownload(ctx context.Context, email, endpoint string) error {
	if endpoint == "" {
		endpoint = MDMCertURL
	}
	csrPEM, err := m.PendingCSR()
	if err != nil {
		return err
	}
	encCert, _, err := m.MDMCertEncryptionCert()
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]string{
		"csr":     base64.StdEncoding.EncodeToString(csrPEM),
		"email":   email,
		"key":     mdmcertAPIKey,
		"encrypt": base64.StdEncoding.EncodeToString([]byte(pki.CertPEM(encCert))),
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "orchard-mdm/1.0")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("mdmcert.download: %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	var r struct {
		Result string `json:"result"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal(b, &r) == nil && r.Result != "" && r.Result != "success" {
		return fmt.Errorf("mdmcert.download: %s %s", r.Result, r.Reason)
	}
	return nil
}

// DecryptMDMCertResponse decrypts the (hex encoded) file emailed by
// mdmcert.download and returns the request file to upload to Apple.
func (m *Manager) DecryptMDMCertResponse(data []byte) ([]byte, error) {
	cert, key, err := m.MDMCertEncryptionCert()
	if err != nil {
		return nil, err
	}
	raw := bytes.TrimSpace(data)
	if dec, err := hex.DecodeString(string(raw)); err == nil {
		raw = dec
	}
	p7, err := pkcs7.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse encrypted response: %w", err)
	}
	out, err := p7.Decrypt(cert, key)
	if err != nil {
		return nil, fmt.Errorf("decrypt response (was it requested from this server?): %w", err)
	}
	return out, nil
}

// UploadResult describes an installed push certificate.
type UploadResult struct {
	Topic        string    `json:"topic"`
	NotAfter     time.Time `json:"not_after"`
	TopicChanged bool      `json:"topic_changed"`
}

// InstallCertificate stores a push certificate. data may be the PEM/DER
// certificate returned by Apple (matched against the pending or current key)
// or a PKCS#12 bundle when password is non-empty or the data is not a cert.
func (m *Manager) InstallCertificate(data []byte, password, appleID string, allowTopicChange bool) (*UploadResult, error) {
	var cert *x509.Certificate
	var key crypto.Signer
	if c, err := pki.ParseCertPEM(data); err == nil {
		cert = c
		// prefer an inline key (PEM bundle), then the pending CSR key, then the current key
		if k, err := pki.ParseKeyPEM(data); err == nil && pki.KeyMatches(c, k) {
			key = k
		}
		for _, name := range []string{pendingKeyName, KeyPairName} {
			if key != nil {
				break
			}
			if kp, err := m.store.GetKeyPair(name); err == nil && kp.KeyPEM != "" {
				if k, err := pki.ParseKeyPEM([]byte(kp.KeyPEM)); err == nil && pki.KeyMatches(c, k) {
					key = k
				}
			}
		}
		if key == nil {
			return nil, errors.New("this certificate does not match the pending CSR key; upload the certificate generated from this server's CSR, or a .p12 bundle including the key")
		}
	} else {
		c, k, _, err := ParseVendorP12(data, password)
		if err != nil {
			return nil, err
		}
		cert, key = c, k
	}
	topic, err := pki.TopicFromCert(cert)
	if err != nil {
		return nil, err
	}
	if time.Now().After(cert.NotAfter) {
		return nil, fmt.Errorf("certificate expired on %s", cert.NotAfter.Format("2006-01-02"))
	}
	res := &UploadResult{Topic: topic, NotAfter: cert.NotAfter}
	if prev, err := m.store.GetKeyPair(KeyPairName); err == nil && prev.CertPEM != "" {
		if pc, err := pki.ParseCertPEM([]byte(prev.CertPEM)); err == nil {
			if pt, _ := pki.TopicFromCert(pc); pt != "" && pt != topic {
				res.TopicChanged = true
				if !allowTopicChange {
					return res, fmt.Errorf("the new certificate has topic %q but the current one is %q; devices enrolled with the old topic will stop receiving pushes and must re-enroll. Renew the existing certificate with the same Apple ID instead, or confirm the replacement", topic, pt)
				}
			}
		}
		prev.Name = previousName
		_ = m.store.PutKeyPair(prev)
	}
	keyPEM, err := pki.KeyPEM(key)
	if err != nil {
		return nil, err
	}
	meta, _ := json.Marshal(map[string]string{"apple_id": appleID})
	if err := m.store.PutKeyPair(&store.KeyPair{Name: KeyPairName, CertPEM: pki.CertPEM(cert), KeyPEM: keyPEM, Meta: string(meta)}); err != nil {
		return nil, err
	}
	_ = m.store.DeleteKeyPair(pendingKeyName)
	m.mu.Lock()
	m.client = nil
	m.mu.Unlock()
	return res, nil
}
