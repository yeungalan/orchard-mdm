// Package config loads Orchard MDM runtime configuration from flags and
// environment variables (ORCHARD_*).
package config

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the process configuration.
type Config struct {
	Listen         string
	DataDir        string
	PublicURL      string
	TLSCert        string
	TLSKey         string
	ACMEDomains    []string
	ACMEEmail      string
	HTTPRedirect   string
	TrustProxy     bool
	ClientCertHdr  string
	LogLevel       string
	LogJSON        bool
	APNsURL        string
	DEPURL         string
	VPPURL         string
	ITunesURL      string
	AdminUser      string
	AdminPassword  string
	DisableSchedul bool
}

func env(key, def string) string {
	if v, ok := os.LookupEnv("ORCHARD_" + key); ok {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v, ok := os.LookupEnv("ORCHARD_" + key)
	if !ok {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// Load parses flags (args excludes the program name).
func Load(args []string) (*Config, error) {
	c := &Config{}
	fs := flag.NewFlagSet("orchard", flag.ContinueOnError)
	var acme string
	fs.StringVar(&c.Listen, "listen", env("LISTEN", ""), "listen address (default :8443 with TLS, :8080 without) [ORCHARD_LISTEN]")
	fs.StringVar(&c.DataDir, "data", env("DATA", "./data"), "data directory for the database and uploads [ORCHARD_DATA]")
	fs.StringVar(&c.PublicURL, "url", env("URL", ""), "public https base URL devices use, e.g. https://mdm.example.com [ORCHARD_URL]")
	fs.StringVar(&c.TLSCert, "tls-cert", env("TLS_CERT", ""), "TLS certificate (PEM, full chain) [ORCHARD_TLS_CERT]")
	fs.StringVar(&c.TLSKey, "tls-key", env("TLS_KEY", ""), "TLS private key (PEM) [ORCHARD_TLS_KEY]")
	fs.StringVar(&acme, "acme-domains", env("ACME_DOMAINS", ""), "comma separated domains to obtain Let's Encrypt certificates for [ORCHARD_ACME_DOMAINS]")
	fs.StringVar(&c.ACMEEmail, "acme-email", env("ACME_EMAIL", ""), "contact email for Let's Encrypt [ORCHARD_ACME_EMAIL]")
	fs.StringVar(&c.HTTPRedirect, "http-redirect", env("HTTP_REDIRECT", ""), "optional plain HTTP listener that redirects to HTTPS (and answers ACME challenges), e.g. :80 [ORCHARD_HTTP_REDIRECT]")
	fs.BoolVar(&c.TrustProxy, "trust-proxy", envBool("TRUST_PROXY", false), "trust X-Forwarded-For/Proto headers from a reverse proxy [ORCHARD_TRUST_PROXY]")
	fs.StringVar(&c.ClientCertHdr, "client-cert-header", env("CLIENT_CERT_HEADER", ""), "header carrying the URL-escaped PEM client certificate from a TLS-terminating proxy (optional) [ORCHARD_CLIENT_CERT_HEADER]")
	fs.StringVar(&c.LogLevel, "log-level", env("LOG_LEVEL", "info"), "debug|info|warn|error [ORCHARD_LOG_LEVEL]")
	fs.BoolVar(&c.LogJSON, "log-json", envBool("LOG_JSON", false), "log in JSON format [ORCHARD_LOG_JSON]")
	fs.StringVar(&c.APNsURL, "apns-url", env("APNS_URL", "https://api.push.apple.com"), "APNs endpoint [ORCHARD_APNS_URL]")
	fs.StringVar(&c.DEPURL, "dep-url", env("DEP_URL", "https://mdmenrollment.apple.com"), "Apple device enrollment (ADE) API endpoint [ORCHARD_DEP_URL]")
	fs.StringVar(&c.VPPURL, "vpp-url", env("VPP_URL", "https://vpp.itunes.apple.com/mdm/v2"), "Apps and Books API endpoint [ORCHARD_VPP_URL]")
	fs.StringVar(&c.ITunesURL, "itunes-url", env("ITUNES_URL", "https://itunes.apple.com"), "iTunes search/lookup endpoint [ORCHARD_ITUNES_URL]")
	fs.StringVar(&c.AdminUser, "admin-user", env("ADMIN_USER", ""), "create this admin user on first start (with -admin-password) [ORCHARD_ADMIN_USER]")
	fs.StringVar(&c.AdminPassword, "admin-password", env("ADMIN_PASSWORD", ""), "password for -admin-user [ORCHARD_ADMIN_PASSWORD]")
	fs.BoolVar(&c.DisableSchedul, "no-scheduler", envBool("NO_SCHEDULER", false), "disable background jobs (debugging) [ORCHARD_NO_SCHEDULER]")
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	for _, d := range strings.Split(acme, ",") {
		if d = strings.TrimSpace(d); d != "" {
			c.ACMEDomains = append(c.ACMEDomains, d)
		}
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return nil, fmt.Errorf("-tls-cert and -tls-key must be given together")
	}
	if c.Listen == "" {
		if c.TLSEnabled() {
			c.Listen = ":8443"
		} else {
			c.Listen = ":8080"
		}
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	if c.PublicURL == "" && len(c.ACMEDomains) > 0 {
		c.PublicURL = "https://" + c.ACMEDomains[0]
	}
	abs, err := filepath.Abs(c.DataDir)
	if err != nil {
		return nil, err
	}
	c.DataDir = abs
	return c, nil
}

// TLSEnabled reports whether Orchard terminates TLS itself.
func (c *Config) TLSEnabled() bool { return c.TLSCert != "" || len(c.ACMEDomains) > 0 }
