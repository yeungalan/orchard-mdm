// Package apps handles the app catalog: App Store metadata lookups and
// enterprise (in-house) .ipa packages.
package apps

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"howett.net/plist"
)

// StoreApp is App Store metadata.
type StoreApp struct {
	TrackID     int64   `json:"trackId"`
	TrackName   string  `json:"trackName"`
	BundleID    string  `json:"bundleId"`
	Version     string  `json:"version"`
	ArtworkURL  string  `json:"artworkUrl512"`
	Artwork100  string  `json:"artworkUrl100"`
	SellerName  string  `json:"sellerName"`
	Description string  `json:"description"`
	Price       float64 `json:"price"`
	Kind        string  `json:"kind"`
	Genre       string  `json:"primaryGenreName"`
	MinOS       string  `json:"minimumOsVersion"`
}

// Icon returns the best icon URL.
func (a StoreApp) Icon() string {
	if a.ArtworkURL != "" {
		return a.ArtworkURL
	}
	return a.Artwork100
}

// ITunes queries the public iTunes Search API.
type ITunes struct {
	BaseURL string
	Country string
	HTTP    *http.Client
}

// NewITunes creates a client.
func NewITunes(base string) *ITunes {
	if base == "" {
		base = "https://itunes.apple.com"
	}
	return &ITunes{BaseURL: strings.TrimRight(base, "/"), Country: "us", HTTP: &http.Client{Timeout: 15 * time.Second}}
}

func (it *ITunes) get(ctx context.Context, endpoint string, q url.Values) ([]StoreApp, error) {
	if q.Get("country") == "" {
		q.Set("country", it.Country)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, it.BaseURL+endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := it.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("itunes: %s", resp.Status)
	}
	var out struct {
		Results []StoreApp `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, err
	}
	var apps []StoreApp
	for _, a := range out.Results {
		if a.TrackID != 0 {
			apps = append(apps, a)
		}
	}
	return apps, nil
}

// Search finds iOS apps by name.
func (it *ITunes) Search(ctx context.Context, term, country string, limit int) ([]StoreApp, error) {
	if limit <= 0 || limit > 50 {
		limit = 25
	}
	q := url.Values{"term": {term}, "entity": {"software"}, "limit": {fmt.Sprint(limit)}}
	if country != "" {
		q.Set("country", country)
	}
	return it.get(ctx, "/search", q)
}

// Lookup fetches metadata for App Store IDs.
func (it *ITunes) Lookup(ctx context.Context, ids []string, country string) ([]StoreApp, error) {
	q := url.Values{"id": {strings.Join(ids, ",")}, "entity": {"software"}}
	if country != "" {
		q.Set("country", country)
	}
	return it.get(ctx, "/lookup", q)
}

// LookupBundle fetches metadata by bundle identifier.
func (it *ITunes) LookupBundle(ctx context.Context, bundleID, country string) (*StoreApp, error) {
	q := url.Values{"bundleId": {bundleID}, "entity": {"software"}}
	if country != "" {
		q.Set("country", country)
	}
	res, err := it.get(ctx, "/lookup", q)
	if err != nil {
		return nil, err
	}
	if len(res) == 0 {
		return nil, errors.New("app not found in the App Store")
	}
	return &res[0], nil
}

var storeURLID = regexp.MustCompile(`id(\d{6,})`)

// ParseStoreID accepts a numeric ID or an apps.apple.com URL.
func ParseStoreID(s string) string {
	s = strings.TrimSpace(s)
	if m := storeURLID.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return s
}

// IPAInfo is metadata extracted from an .ipa.
type IPAInfo struct {
	BundleID    string `json:"bundle_id"`
	Version     string `json:"version"`
	BuildNumber string `json:"build"`
	Name        string `json:"name"`
	MinOS       string `json:"min_os"`
}

var appInfoRE = regexp.MustCompile(`^Payload/[^/]+\.app/Info\.plist$`)

// ParseIPA reads Info.plist from an .ipa file.
func ParseIPA(r io.ReaderAt, size int64) (*IPAInfo, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("not a valid .ipa (zip): %w", err)
	}
	for _, f := range zr.File {
		if !appInfoRE.MatchString(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(rc, 4<<20))
		rc.Close()
		if err != nil {
			return nil, err
		}
		var info map[string]any
		if _, err := plist.Unmarshal(data, &info); err != nil {
			return nil, fmt.Errorf("Info.plist: %w", err)
		}
		str := func(k string) string { s, _ := info[k].(string); return s }
		out := &IPAInfo{
			BundleID: str("CFBundleIdentifier"), Version: str("CFBundleShortVersionString"), BuildNumber: str("CFBundleVersion"),
			Name: str("CFBundleDisplayName"), MinOS: str("MinimumOSVersion"),
		}
		if out.Name == "" {
			out.Name = str("CFBundleName")
		}
		if out.Name == "" {
			out.Name = strings.TrimSuffix(path.Base(path.Dir(f.Name)), ".app")
		}
		if out.Version == "" {
			out.Version = out.BuildNumber
		}
		if out.BundleID == "" {
			return nil, errors.New("Info.plist has no CFBundleIdentifier")
		}
		return out, nil
	}
	return nil, errors.New("no Payload/*.app/Info.plist found in the .ipa")
}

// Manifest builds the installation manifest for an enterprise app.
func Manifest(ipaURL, bundleID, version, title string) ([]byte, error) {
	m := map[string]any{
		"items": []any{map[string]any{
			"assets": []any{map[string]any{"kind": "software-package", "url": ipaURL}},
			"metadata": map[string]any{
				"bundle-identifier": bundleID,
				"bundle-version":    version,
				"kind":              "software",
				"title":             title,
			},
		}},
	}
	return plist.MarshalIndent(m, plist.XMLFormat, "\t")
}
