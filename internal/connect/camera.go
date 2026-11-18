package connect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const MaxSnapshotBytes = 8 << 20
const MaxSnapshotAge = 2 * time.Minute

var (
	ErrCameraUnauthorized = errors.New("Connect camera credential expired, was revoked, or lacks access")
	ErrCameraUnavailable  = errors.New("camera is unavailable")
	ErrNoNewFrame         = errors.New("camera has no new frame")
	ErrStaleFrame         = errors.New("camera frame is stale or its capture time is unavailable")
	ErrInvalidFrame       = errors.New("camera response is not a valid bounded JPEG")
)

type Camera struct {
	Name          string `json:"name"`
	DeviceID      string `json:"device_id"`
	Serving       bool   `json:"serving"`
	Snapshot      bool   `json:"snapshot"`
	MinIntervalMS int64  `json:"min_interval_ms"`
}

type Snapshot struct {
	JPEG       []byte
	CapturedAt time.Time
	ID         string
}

// CameraClient only sends its credential to the configured Connect origin.
// The operator chooses that origin; camera responses can never supply a URL.
type CameraClient struct {
	base  *url.URL
	token string
	http  *http.Client
	now   func() time.Time
}

func NewCameraClient(baseURL, token string) (*CameraClient, error) {
	return newCameraClient(baseURL, token, false)
}

// allowTestPort is used only by in-package tests with httptest's ephemeral port.
// The exported constructor always enforces the marketplace egress declaration.
func newCameraClient(baseURL, token string, allowTestPort bool) (*CameraClient, error) {
	base, err := url.Parse(baseURL)
	if err != nil || base == nil || base.Hostname() == "" ||
		(base.Scheme != "http" && base.Scheme != "https") || base.User != nil ||
		base.RawQuery != "" || base.Fragment != "" || base.Opaque != "" ||
		(base.Path != "" && base.Path != "/") {
		return nil, errors.New("Connect URL must be an HTTP(S) origin without credentials, a path, query, or fragment")
	}
	port := base.Port()
	if port == "" {
		port = "443"
		if base.Scheme == "http" {
			port = "80"
		}
	}
	if !allowTestPort && !slices.Contains([]string{"80", "443", "8080", "8443", "18480"}, port) {
		return nil, errors.New("Connect URL port must be 80, 443, 8080, 8443, or 18480, as declared by the extension")
	}
	if strings.TrimSpace(token) == "" || strings.ContainsAny(token, "\r\n") {
		return nil, errors.New("a Connect camera credential is required")
	}
	base.Path = ""
	// Connect commonly runs on a LAN or container network over HTTP. The
	// configured origin is explicit; redirects are refused so bearer credentials
	// cannot be forwarded to a camera, another server, or an auth redirect.
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       60 * time.Second,
		MaxIdleConns:          8, MaxIdleConnsPerHost: 4,
	}
	return &CameraClient{base: base, token: token, now: time.Now, http: &http.Client{
		Transport: transport, Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *CameraClient) Close() { c.http.CloseIdleConnections() }

func (c *CameraClient) Cameras(ctx context.Context, deviceID string) ([]Camera, error) {
	if deviceID == "" {
		return nil, errors.New("camera listing requires a printer id")
	}
	u := *c.base
	u.Path = "/api/v1/cameras"
	u.RawQuery = url.Values{"device": []string{deviceID}}.Encode()
	response, err := c.get(ctx, &u, "application/json", time.Time{})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if err := cameraStatus(response.StatusCode); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil {
		return nil, errors.New("camera listing could not be read")
	}
	if len(data) > 1<<20 {
		return nil, errors.New("camera listing is too large")
	}
	var listing struct {
		Items []Camera `json:"items"`
	}
	if json.Unmarshal(data, &listing) != nil {
		return nil, errors.New("camera listing is invalid")
	}
	result := make([]Camera, 0, len(listing.Items))
	for _, camera := range listing.Items {
		// Treat the API's filter as untrusted input too: never return another
		// printer's camera when a privileged background credential is in use.
		if camera.DeviceID == deviceID && validCameraName(camera.Name) {
			result = append(result, camera)
		}
	}
	return result, nil
}

// Snapshot never returns a cached or undated frame. notBefore is the latest
// permissible lower bound (job confirmation or the previous analyzed frame).
func (c *CameraClient) Snapshot(ctx context.Context, cameraName string, notBefore time.Time) (Snapshot, error) {
	if !validCameraName(cameraName) {
		return Snapshot{}, errors.New("invalid camera name")
	}
	u := *c.base
	u.Path = "/api/v1/cameras/" + cameraName + "/snapshot"
	response, err := c.get(ctx, &u, "image/jpeg", notBefore)
	if err != nil {
		return Snapshot{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified || response.StatusCode == http.StatusNoContent {
		return Snapshot{}, ErrNoNewFrame
	}
	if err := cameraStatus(response.StatusCode); err != nil {
		return Snapshot{}, err
	}
	captured, err := http.ParseTime(response.Header.Get("Last-Modified"))
	now := c.now()
	if err != nil || captured.After(now.Add(5*time.Second)) || now.Sub(captured) > MaxSnapshotAge {
		return Snapshot{}, ErrStaleFrame
	}
	if !notBefore.IsZero() && !captured.After(notBefore) {
		return Snapshot{}, ErrNoNewFrame
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "image/jpeg" || response.ContentLength > MaxSnapshotBytes {
		return Snapshot{}, ErrInvalidFrame
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxSnapshotBytes+1))
	if err != nil {
		return Snapshot{}, errors.New("camera frame could not be read")
	}
	if len(data) == 0 || len(data) > MaxSnapshotBytes {
		return Snapshot{}, ErrInvalidFrame
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width < 1 || config.Height < 1 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return Snapshot{}, ErrInvalidFrame
	}
	digest := sha256.Sum256(data)
	return Snapshot{JPEG: data, CapturedAt: captured, ID: hex.EncodeToString(digest[:])}, nil
}

func (c *CameraClient) get(ctx context.Context, u *url.URL, accept string, since time.Time) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, errors.New("camera request could not be created")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Accept", accept)
	if !since.IsZero() {
		request.Header.Set("If-Modified-Since", since.UTC().Format(http.TimeFormat))
	}
	response, err := c.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Do not return an arbitrary transport error, which may contain the
		// configured URL or attacker-controlled response text.
		return nil, errors.New("Connect camera request failed")
	}
	return response, nil
}

func cameraStatus(status int) error {
	switch status {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrCameraUnauthorized
	case http.StatusNotFound, http.StatusBadGateway, http.StatusServiceUnavailable:
		return ErrCameraUnavailable
	default:
		return fmt.Errorf("Connect camera request returned HTTP %d", status)
	}
}

func validCameraName(name string) bool {
	if name == "" || len(name) > 256 || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}
