// Package scepsrv is the SCEP endpoint that issues device identity
// certificates during enrollment.
package scepsrv

import (
	"crypto"
	"crypto/x509"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/smallstep/pkcs7"
	"github.com/smallstep/scep"
	"github.com/yeungalan/orchard-mdm/internal/pki"
	"github.com/yeungalan/orchard-mdm/internal/store"
)

// Handler serves /scep.
type Handler struct {
	Store    *store.Store
	CA       func() (*x509.Certificate, crypto.Signer)
	Validity func() time.Duration
	Log      *slog.Logger
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	op := r.URL.Query().Get("operation")
	switch op {
	case "GetCACaps":
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Renewal\nSHA-1\nSHA-256\nAES\nDES3\nSCEPStandard\nPOSTPKIOperation"))
	case "GetCACert":
		ca, _ := h.CA()
		w.Header().Set("Content-Type", "application/x-x509-ca-cert")
		_, _ = w.Write(ca.Raw)
	case "PKIOperation":
		var msg []byte
		var err error
		if r.Method == http.MethodPost {
			msg, err = io.ReadAll(io.LimitReader(r.Body, 1<<20))
		} else {
			raw := r.URL.Query().Get("message")
			msg, err = base64.StdEncoding.DecodeString(strings.ReplaceAll(raw, " ", "+"))
		}
		if err != nil {
			http.Error(w, "bad message", http.StatusBadRequest)
			return
		}
		h.pkiOperation(w, msg)
	default:
		http.Error(w, "unsupported SCEP operation", http.StatusBadRequest)
	}
}

func (h *Handler) pkiOperation(w http.ResponseWriter, data []byte) {
	ca, caKey := h.CA()
	msg, err := scep.ParsePKIMessage(data)
	if err != nil {
		h.Log.Warn("scep: parse", "err", err)
		http.Error(w, "invalid PKI message", http.StatusBadRequest)
		return
	}
	if err := msg.DecryptPKIEnvelope(ca, caKey); err != nil {
		h.Log.Warn("scep: decrypt", "err", err)
		http.Error(w, "cannot decrypt request", http.StatusBadRequest)
		return
	}
	fail := func(reason string) {
		h.Log.Warn("scep: request rejected", "reason", reason, "subject", msg.CSR.Subject.String())
		rep, err := msg.Fail(ca, caKey, scep.BadRequest)
		if err != nil {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-pki-message")
		_, _ = w.Write(rep.Raw)
	}
	purpose, ref := "", ""
	switch msg.MessageType {
	case scep.PKCSReq:
		p, rf, ok := h.Store.ConsumeChallenge(msg.ChallengePassword)
		if !ok {
			fail("invalid or expired challenge")
			return
		}
		purpose, ref = p, rf
	case scep.RenewalReq, scep.UpdateReq:
		// renewals must be signed by a still-valid identity we issued
		p7, err := pkcs7.Parse(data)
		signer := (*x509.Certificate)(nil)
		if err == nil {
			signer = p7.GetOnlySigner()
		}
		if signer == nil || pki.VerifyChain(signer, ca) != nil || time.Now().After(signer.NotAfter) {
			fail("renewal must be signed by a valid identity issued by this server")
			return
		}
		prev, err := h.Store.GetIssuedCertBySHA(pki.Fingerprint(signer))
		if err != nil || prev.Revoked {
			fail("renewal signer is unknown or revoked")
			return
		}
		purpose, ref = "enroll", prev.Ref
		if prev.DeviceID != "" {
			ref = "renew:" + prev.DeviceID
		}
	default:
		fail("unsupported message type")
		return
	}
	cert, err := pki.SignCSR(ca, caKey, msg.CSR, h.Validity())
	if err != nil {
		fail(err.Error())
		return
	}
	issued := &store.IssuedCert{
		Serial: cert.SerialNumber.Text(16), SHA256: pki.Fingerprint(cert), Subject: cert.Subject.String(),
		NotBefore: cert.NotBefore.Unix(), NotAfter: cert.NotAfter.Unix(), Purpose: purpose, Ref: ref,
	}
	if strings.HasPrefix(ref, "renew:") {
		issued.DeviceID = strings.TrimPrefix(ref, "renew:")
	}
	if err := h.Store.InsertIssuedCert(issued); err != nil {
		h.Log.Error("scep: record issued cert", "err", err)
	}
	rep, err := msg.Success(ca, caKey, cert)
	if err != nil {
		h.Log.Error("scep: build response", "err", err)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}
	h.Log.Info("scep: issued device identity", "subject", cert.Subject.String(), "ref", ref, "expires", cert.NotAfter.Format(time.DateOnly))
	w.Header().Set("Content-Type", "application/x-pki-message")
	_, _ = w.Write(rep.Raw)
}
