// Package pki contains certificate authority and certificate helpers.
package pki

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/smallstep/pkcs7"
)

func init() {
	// iOS SCEP clients and Apple's token encryption both handle AES; DES is obsolete.
	pkcs7.ContentEncryptionAlgorithm = pkcs7.EncryptionAlgorithmAES128CBC
}

// GenerateKey creates an RSA private key.
func GenerateKey(bits int) (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, bits)
}

// RandomSerial returns a random 128-bit certificate serial number.
func RandomSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 127)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic(err)
	}
	return n.Add(n, big.NewInt(1))
}

func subjectKeyID(pub crypto.PublicKey) []byte {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil
	}
	sum := sha1.Sum(der)
	return sum[:]
}

// NewCA creates a self-signed certificate authority.
func NewCA(commonName, org string, years int) (*x509.Certificate, *rsa.PrivateKey, error) {
	key, err := GenerateKey(2048)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().Add(-time.Hour)
	tmpl := &x509.Certificate{
		SerialNumber:          RandomSerial(),
		Subject:               pkix.Name{CommonName: commonName, Organization: []string{org}},
		NotBefore:             now,
		NotAfter:              now.AddDate(years, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		SubjectKeyId:          subjectKeyID(key.Public()),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

// SelfSigned creates a self-signed leaf certificate (used for ADE token
// decryption and mdmcert.download encryption).
func SelfSigned(commonName string, days int) (*x509.Certificate, *rsa.PrivateKey, error) {
	key, err := GenerateKey(2048)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now().Add(-time.Hour)
	tmpl := &x509.Certificate{
		SerialNumber:          RandomSerial(),
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             now,
		NotAfter:              now.AddDate(0, 0, days),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageDataEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

// SignCSR issues a client-auth certificate for the CSR.
func SignCSR(ca *x509.Certificate, caKey crypto.Signer, csr *x509.CertificateRequest, validity time.Duration) (*x509.Certificate, error) {
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("csr signature: %w", err)
	}
	now := time.Now().Add(-10 * time.Minute)
	notAfter := now.Add(validity)
	if notAfter.After(ca.NotAfter) {
		notAfter = ca.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber:   RandomSerial(),
		Subject:        csr.Subject,
		NotBefore:      now,
		NotAfter:       notAfter,
		KeyUsage:       x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:    []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		SubjectKeyId:   subjectKeyID(csr.PublicKey),
		AuthorityKeyId: ca.SubjectKeyId,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, csr.PublicKey, caKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(der)
}

// IssueClientCert issues a client certificate for a fresh key (used by tests
// and the device simulator).
func IssueClientCert(ca *x509.Certificate, caKey crypto.Signer, cn string, validity time.Duration) (*x509.Certificate, *rsa.PrivateKey, error) {
	key, err := GenerateKey(2048)
	if err != nil {
		return nil, nil, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: cn}}, key)
	if err != nil {
		return nil, nil, err
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, nil, err
	}
	cert, err := SignCSR(ca, caKey, csr, validity)
	return cert, key, err
}

// NewCSR builds a PEM CSR (used for the APNs push certificate request).
func NewCSR(key crypto.Signer, cn, email, org, country string) ([]byte, error) {
	subj := pkix.Name{CommonName: cn}
	if org != "" {
		subj.Organization = []string{org}
	}
	if country != "" {
		subj.Country = []string{country}
	}
	tmpl := &x509.CertificateRequest{Subject: subj, SignatureAlgorithm: x509.SHA256WithRSA}
	if email != "" {
		tmpl.EmailAddresses = []string{email}
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, tmpl, key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

// CertPEM encodes a certificate as PEM.
func CertPEM(c *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw}))
}

// KeyPEM encodes a private key as PEM.
func KeyPEM(key crypto.PrivateKey) (string, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})), nil
	default:
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return "", err
		}
		return string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), nil
	}
}

// ParseCertsPEM parses every certificate in PEM data (DER is accepted too).
func ParseCertsPEM(data []byte) ([]*x509.Certificate, error) {
	var out []*x509.Certificate
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		if c, err := x509.ParseCertificate(bytes.TrimSpace(data)); err == nil {
			return []*x509.Certificate{c}, nil
		}
		return nil, errors.New("no certificate found")
	}
	return out, nil
}

// ParseCertPEM parses the first certificate in PEM (or DER) data.
func ParseCertPEM(data []byte) (*x509.Certificate, error) {
	certs, err := ParseCertsPEM(data)
	if err != nil {
		return nil, err
	}
	return certs[0], nil
}

// ParseKeyPEM parses a PEM private key (PKCS#1, PKCS#8 or EC).
func ParseKeyPEM(data []byte) (crypto.Signer, error) {
	rest := data
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, errors.New("no private key found")
		}
		switch block.Type {
		case "RSA PRIVATE KEY":
			return x509.ParsePKCS1PrivateKey(block.Bytes)
		case "EC PRIVATE KEY":
			return x509.ParseECPrivateKey(block.Bytes)
		case "PRIVATE KEY":
			k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if err != nil {
				return nil, err
			}
			switch kk := k.(type) {
			case *rsa.PrivateKey:
				return kk, nil
			case *ecdsa.PrivateKey:
				return kk, nil
			default:
				if s, ok := k.(crypto.Signer); ok {
					return s, nil
				}
				return nil, errors.New("unsupported key type")
			}
		}
	}
}

// Fingerprint returns the hex SHA-256 of a certificate.
func Fingerprint(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}

// KeyMatches reports whether a private key belongs to a certificate.
func KeyMatches(c *x509.Certificate, key crypto.Signer) bool {
	type eq interface{ Equal(crypto.PublicKey) bool }
	pub, ok := key.Public().(eq)
	return ok && pub.Equal(c.PublicKey)
}

// oidUID is the LDAP "uid" attribute; Apple puts the push topic there.
var oidUID = asn1.ObjectIdentifier{0, 9, 2342, 19200300, 100, 1, 1}

// TopicFromCert extracts the APNs topic (UID attribute) from a push certificate.
func TopicFromCert(c *x509.Certificate) (string, error) {
	for _, n := range c.Subject.Names {
		if n.Type.Equal(oidUID) {
			if s, ok := n.Value.(string); ok && s != "" {
				return s, nil
			}
		}
	}
	return "", errors.New("certificate has no UID (topic) attribute; is this an MDM push certificate?")
}

// SubjectString renders a certificate subject in a compact form.
func SubjectString(n pkix.Name) string {
	return n.String()
}

// SignProfile wraps a configuration profile in a CMS SignedData envelope.
func SignProfile(data []byte, cert *x509.Certificate, key crypto.PrivateKey, chain []*x509.Certificate) ([]byte, error) {
	sd, err := pkcs7.NewSignedData(data)
	if err != nil {
		return nil, err
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSignerChain(cert, key, chain, pkcs7.SignerInfoConfig{}); err != nil {
		return nil, err
	}
	return sd.Finish()
}

// UnwrapSigned returns the content of a CMS signed message, or the input
// unchanged when it is not signed.
func UnwrapSigned(data []byte) []byte {
	trimmed := bytes.TrimSpace(data)
	if bytes.HasPrefix(trimmed, []byte("<?xml")) || bytes.HasPrefix(trimmed, []byte("<plist")) || bytes.HasPrefix(trimmed, []byte("bplist")) {
		return data
	}
	p7, err := pkcs7.Parse(data)
	if err != nil || len(p7.Content) == 0 {
		return data
	}
	return p7.Content
}

// VerifyChain checks that leaf was issued by ca, ignoring expiry (callers
// decide what to do with expired identities).
func VerifyChain(leaf, ca *x509.Certificate) error {
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	at := time.Now()
	if at.After(leaf.NotAfter) {
		at = leaf.NotAfter.Add(-time.Minute)
	}
	if at.Before(leaf.NotBefore) {
		at = leaf.NotBefore.Add(time.Minute)
	}
	_, err := leaf.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny}})
	return err
}

// RandomToken returns a URL-safe random hex token.
func RandomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// NewUUID returns a random RFC 4122 v4 UUID in upper case (Apple style).
func NewUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return strings.ToUpper(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]))
}

// NewTestPushCert creates a self-signed certificate that looks like an MDM
// push certificate (UID = topic). It is only useful for tests and local demos;
// Apple will not accept it.
func NewTestPushCert(topic string) (certPEM, keyPEM string, err error) {
	key, err := GenerateKey(2048)
	if err != nil {
		return "", "", err
	}
	now := time.Now().Add(-time.Hour)
	tmpl := &x509.Certificate{
		SerialNumber: RandomSerial(),
		Subject:      pkix.Name{CommonName: "APSP:" + topic, ExtraNames: []pkix.AttributeTypeAndValue{{Type: oidUID, Value: topic}}},
		NotBefore:    now,
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, key.Public(), key)
	if err != nil {
		return "", "", err
	}
	c, _ := x509.ParseCertificate(der)
	kp, _ := KeyPEM(key)
	return CertPEM(c), kp, nil
}
