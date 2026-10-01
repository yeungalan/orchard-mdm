package mdm

import (
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/smallstep/pkcs7"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

var errNoIdentity = errors.New("request carries no device identity (Mdm-Signature header or client certificate)")

// requestCert extracts the device identity certificate from a request: a TLS
// client certificate, the Mdm-Signature header (detached CMS signature of the
// body, sent because the enrollment profile sets SignMessage) or a header set
// by a TLS-terminating proxy.
func (s *Service) requestCert(r *http.Request, body []byte) (*x509.Certificate, error) {
	if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
		return r.TLS.PeerCertificates[0], nil
	}
	if sig := r.Header.Get("Mdm-Signature"); sig != "" {
		raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(sig), ""))
		if err != nil {
			return nil, fmt.Errorf("decode Mdm-Signature: %w", err)
		}
		p7, err := pkcs7.Parse(raw)
		if err != nil {
			return nil, fmt.Errorf("parse Mdm-Signature: %w", err)
		}
		p7.Content = body
		if err := p7.Verify(); err != nil {
			return nil, fmt.Errorf("verify Mdm-Signature: %w", err)
		}
		cert := p7.GetOnlySigner()
		if cert == nil {
			return nil, errors.New("Mdm-Signature has no signer certificate")
		}
		return cert, nil
	}
	if s.certHeader != "" {
		if v := r.Header.Get(s.certHeader); v != "" {
			pemText, err := url.QueryUnescape(v)
			if err != nil {
				return nil, err
			}
			return pki.ParseCertPEM([]byte(pemText))
		}
	}
	return nil, errNoIdentity
}

// verifyIdentity checks that cert was issued by our CA.
func (s *Service) verifyIdentity(cert *x509.Certificate) error {
	ca, _ := s.CA()
	if err := pki.VerifyChain(cert, ca); err != nil {
		return fmt.Errorf("identity not issued by this server: %w", err)
	}
	if issued, err := s.store.GetIssuedCertBySHA(pki.Fingerprint(cert)); err == nil && issued.Revoked {
		return errors.New("identity certificate has been revoked")
	}
	if time.Now().After(cert.NotAfter) {
		s.log.Warn("device identity certificate has expired; renew it from the device page", "subject", cert.Subject.CommonName, "expired", cert.NotAfter)
	}
	return nil
}

// authorizeDevice checks that cert belongs to the device record udid.
func (s *Service) authorizeDevice(d *store.Device, cert *x509.Certificate) error {
	fp := pki.Fingerprint(cert)
	if d.CertFingerprint == fp {
		return nil
	}
	// identity renewal: a certificate issued for this device via a renew challenge
	if issued, err := s.store.GetIssuedCertBySHA(fp); err == nil {
		if issued.DeviceID == d.UDID || (issued.Purpose == "enroll" && issued.Ref == "renew:"+d.UDID) {
			_ = s.store.UpdateDevice(d.UDID, map[string]any{"cert_fingerprint": fp, "cert_not_after": cert.NotAfter.Unix()})
			s.store.SetIssuedCertDevice(fp, d.UDID)
			s.event(d.UDID, "device.identity_renewed", "info", "Device identity certificate renewed", map[string]any{"expires": cert.NotAfter.Format(time.DateOnly)})
			return nil
		}
	}
	return errors.New("certificate does not match the enrolled device identity")
}
