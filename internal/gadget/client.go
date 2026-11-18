// Package gadget implements the public Gadget HTTP API. It never retries a POST:
// callers own print lifecycle, inspection pacing, and action deduplication.
package gadget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL    = "https://gadget-pv1-oeapi.octoeverywhere.com"
	MaxImageBytes     = 6 * 1024 * 1024
	maxImageDimension = 8192
	maxImagePixels    = 20_000_000
	maxResponseBytes  = 1024 * 1024
	requestTimeout    = 45 * time.Second
)

// Service is the boundary between the print monitor and the HTTP implementation.
type Service interface {
	Create(context.Context, string, int, int) (Context, error)
	Process(context.Context, Context, []byte) (Result, error)
	Cancel(context.Context, Context) error
}

// Context belongs to one print. Its ID and URLs are bearer credentials; never log
// them or expose them in a frontend status response.
type Context struct {
	ID          string     `json:"id"`
	ProcessURL  string     `json:"process_url"`
	FallbackURL string     `json:"fallback_url"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

type Result struct {
	Quality          int
	Warning          bool
	Pause            bool
	Score            int
	FasterInspection bool
	Minimum          time.Duration
	Recommended      time.Duration
}

type Scope string

const (
	ScopeNone    Scope = ""
	ScopeAccount Scope = "account"
	ScopeContext Scope = "context"
	ScopeRequest Scope = "request"
)

// APIError deliberately contains locally generated messages, never response
// bodies, credentials, or URLs. RetryAfter is a lower bound, not permission to
// retry terminal errors. UseFallback applies to a later, freshly captured image.
type APIError struct {
	Code          string
	Message       string
	Status        int
	RetryAfter    time.Duration
	Retryable     bool
	UseFallback   bool
	TerminalScope Scope
}

func (e *APIError) Error() string { return e.Code + ": " + e.Message }

// Options allows transport injection without weakening production URL policy.
// AllowLoopbackHTTP is only for local tests: it permits the exact loopback
// BaseURL origin, never arbitrary HTTP destinations or other local ports.
type Options struct {
	HTTPClient        *http.Client
	BaseURL           string
	AllowLoopbackHTTP bool
}

type Client struct {
	http           *http.Client
	baseURL        string
	loopbackOrigin string
}

func NewClient(options Options) (*Client, error) {
	base := options.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	u, err := url.Parse(base)
	if err != nil || u == nil || (u.Path != "" && u.Path != "/") || !cleanURL(u, base) {
		return nil, requestError("Invalid Gadget API base URL.")
	}
	local := ""
	if options.AllowLoopbackHTTP && (u.Scheme == "http" || u.Scheme == "https") && isLoopback(u.Hostname()) {
		local = u.Scheme + "://" + u.Host
	} else if !productionOrigin(u) {
		return nil, requestError("Gadget API destinations must use HTTPS on OctoEverywhere hosts.")
	}
	httpClient := http.Client{}
	if options.HTTPClient != nil {
		httpClient = *options.HTTPClient
	}
	// The supplied transport may support the extension host's network capability.
	// Copy the client so redirect and timeout policy cannot mutate the caller's client.
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if httpClient.Timeout <= 0 || httpClient.Timeout > requestTimeout {
		httpClient.Timeout = requestTimeout
	}
	return &Client{http: &httpClient, baseURL: u.Scheme + "://" + u.Host, loopbackOrigin: local}, nil
}

func (c *Client) Create(ctx context.Context, key string, warningConfidence, pauseConfidence int) (Context, error) {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return Context{}, requestError("A valid Gadget API key is required.")
	}
	if warningConfidence < 1 || warningConfidence > 5 || pauseConfidence < 1 || pauseConfidence > 5 {
		return Context{}, requestError("Confidence levels must be integers from 1 to 5.")
	}
	body, _ := json.Marshal(struct {
		Warning int `json:"WarningConfidenceLevel"`
		Pause   int `json:"PauseConfidenceLevel"`
	}{warningConfidence, pauseConfidence})
	data, err := c.post(ctx, c.baseURL+"/api/gadget/v1/createcontext", key, "application/json", bytes.NewReader(body), false)
	if err != nil {
		return Context{}, err
	}
	var response struct {
		ID          string     `json:"ContextId"`
		ProcessURL  string     `json:"ProcessRequestUrl"`
		FallbackURL string     `json:"FallbackProcessRequestUrl"`
		ExpiresAt   *time.Time `json:"ExpiresAtUtc"`
	}
	if decode(data, &response) != nil {
		return Context{}, responseError(0)
	}
	result := Context{ID: response.ID, ProcessURL: response.ProcessURL, FallbackURL: response.FallbackURL, ExpiresAt: response.ExpiresAt}
	if !validID(result.ID) || !c.validProcessURL(result.ProcessURL, result.ID) || !c.validProcessURL(result.FallbackURL, result.ID) {
		return Context{}, responseError(0)
	}
	return result, nil
}

func (c *Client) Process(ctx context.Context, printContext Context, image []byte) (Result, error) {
	if !validID(printContext.ID) || !c.validProcessURL(printContext.ProcessURL, printContext.ID) || !c.validProcessURL(printContext.FallbackURL, printContext.ID) {
		return Result{}, requestError("Invalid Gadget processing context.")
	}
	if err := validateJPEG(image); err != nil {
		return Result{}, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", `form-data; name="image"; filename="snapshot.jpg"`)
	header.Set("Content-Type", "image/jpeg")
	part, err := writer.CreatePart(header)
	if err != nil {
		return Result{}, requestError("Could not prepare the camera image.")
	}
	if _, err = part.Write(image); err != nil {
		return Result{}, requestError("Could not prepare the camera image.")
	}
	if err = writer.Close(); err != nil {
		return Result{}, requestError("Could not prepare the camera image.")
	}
	// Process is authorized by its secret URL. Do not forward the account key.
	data, err := c.post(ctx, printContext.ProcessURL, "", writer.FormDataContentType(), &body, true)
	if err != nil {
		return Result{}, err
	}
	return parseResult(data)
}

func (c *Client) Cancel(ctx context.Context, printContext Context) error {
	if !validID(printContext.ID) {
		return requestError("Invalid Gadget context ID.")
	}
	data, err := c.post(ctx, c.baseURL+"/api/gadget/v1/cancelcontext/"+printContext.ID, "", "", nil, false)
	if err != nil {
		return err
	}
	var response struct {
		ID        string `json:"ContextId"`
		Cancelled *bool  `json:"IsCancelled"`
	}
	if decode(data, &response) != nil || response.ID != printContext.ID || response.Cancelled == nil || !*response.Cancelled {
		return responseError(0)
	}
	return nil
}

func (c *Client) post(ctx context.Context, target, key, contentType string, body io.Reader, processing bool) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, body)
	if err != nil {
		return nil, requestError("Could not prepare the Gadget request.")
	}
	if key != "" {
		req.Header.Set("X-API-Key", key)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &APIError{Code: "OE_NETWORK_ERROR", Message: "Could not reach Gadget. A later inspection can retry.", Retryable: true, UseFallback: processing}
	}
	defer response.Body.Close()
	data, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if readErr != nil || len(data) > maxResponseBytes {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &APIError{Code: "OE_RESPONSE_ERROR", Message: "Gadget returned an unreadable response.", Status: response.StatusCode, Retryable: true, UseFallback: processing}
	}
	if response.StatusCode != http.StatusOK {
		return nil, classifyError(response.StatusCode, data, retryAfter(response.Header.Get("Retry-After"), time.Now()), processing)
	}
	return data, nil
}

func parseResult(data []byte) (Result, error) {
	// Validate pacing first, independently from the assessment. An invalid flag
	// must not cause a caller to forget a valid, longer server minimum.
	var fields map[string]json.RawMessage
	if decode(data, &fields) != nil || fields == nil {
		return Result{}, responseError(0)
	}
	var pacing map[string]json.RawMessage
	if decode(fields["NextProcessIntervalSec"], &pacing) != nil {
		return Result{}, responseError(0)
	}
	var minimumValue, recommendedValue *int64
	if decode(pacing["Minimum"], &minimumValue) != nil {
		return Result{}, responseError(0)
	}
	minimum, ok := durationSeconds(minimumValue)
	if !ok {
		return Result{}, responseError(0)
	}
	if decode(pacing["Recommended"], &recommendedValue) != nil {
		return Result{}, responseError(minimum)
	}
	recommended, ok := durationSeconds(recommendedValue)
	if !ok || recommended < minimum {
		return Result{}, responseError(minimum)
	}
	var response struct {
		Quality *int            `json:"PrintQuality"`
		Warning *bool           `json:"WarningSuggested"`
		Pause   *bool           `json:"PauseSuggested"`
		Score   *int            `json:"Score"`
		Faster  json.RawMessage `json:"FasterInspectionSuggested"`
	}
	if decode(data, &response) != nil {
		return Result{}, responseError(recommended)
	}
	if response.Quality == nil || *response.Quality < 1 || *response.Quality > 10 || response.Score == nil || *response.Score < 0 || *response.Score > 100 || response.Warning == nil || response.Pause == nil {
		return Result{}, responseError(recommended)
	}
	faster := false
	if len(response.Faster) > 0 && (bytes.Equal(bytes.TrimSpace(response.Faster), []byte("null")) || json.Unmarshal(response.Faster, &faster) != nil) {
		return Result{}, responseError(recommended)
	}
	return Result{Quality: *response.Quality, Warning: *response.Warning, Pause: *response.Pause, Score: *response.Score, FasterInspection: faster, Minimum: minimum, Recommended: recommended}, nil
}

func durationSeconds(value *int64) (time.Duration, bool) {
	if value == nil || *value < 1 || *value > int64((1<<63-1)/time.Second) {
		return 0, false
	}
	return time.Duration(*value) * time.Second, true
}

// ValidateImage checks Gadget's JPEG format and size limits without uploading
// the image or creating a context. It uses the same checks as Process.
func ValidateImage(image []byte) error { return validateJPEG(image) }

func validateJPEG(image []byte) error {
	if len(image) == 0 || len(image) > MaxImageBytes {
		return requestError("Camera JPEG must be nonempty and no larger than 6 MiB.")
	}
	// Decode only metadata: decoding a full image adds substantial CPU and memory
	// to the local extension. Gadget still validates the compressed image payload.
	config, err := jpeg.DecodeConfig(bytes.NewReader(image))
	if err != nil || config.Width < 1 || config.Height < 1 {
		return requestError("Camera snapshot must be a valid JPEG.")
	}
	if config.Width > maxImageDimension || config.Height > maxImageDimension || int64(config.Width)*int64(config.Height) > maxImagePixels {
		return requestError("Camera JPEG exceeds Gadget's image dimension limits.")
	}
	return nil
}

func (c *Client) validProcessURL(value, id string) bool {
	u, err := url.Parse(value)
	if err != nil || !cleanURL(u, value) || u.Path != "/api/gadget/v1/process/"+id || u.RawPath != "" {
		return false
	}
	return productionOrigin(u) || (c.loopbackOrigin != "" && u.Scheme+"://"+u.Host == c.loopbackOrigin)
}

func cleanURL(u *url.URL, raw string) bool {
	return u != nil && u.Host != "" && u.User == nil && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && u.Opaque == "" && !strings.ContainsAny(raw, " \t\r\n#")
}

func productionOrigin(u *url.URL) bool {
	host := strings.ToLower(u.Hostname())
	return u.Scheme == "https" && (u.Port() == "" || u.Port() == "443") && (host == "octoeverywhere.com" || strings.HasSuffix(host, ".octoeverywhere.com"))
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validID(id string) bool {
	if len(id) == 0 || len(id) > 256 {
		return false
	}
	for _, char := range id {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-') {
			return false
		}
	}
	return true
}

func decode(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func requestError(message string) *APIError {
	return &APIError{Code: "OE_CLIENT_ERROR", Message: message, TerminalScope: ScopeRequest}
}
func responseError(delay time.Duration) *APIError {
	return &APIError{Code: "OE_RESPONSE_ERROR", Message: "Gadget returned an invalid response.", Retryable: true, RetryAfter: delay}
}

func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		if duration, ok := durationSeconds(&seconds); ok {
			return duration
		}
		return time.Duration(1<<63 - 1)
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

func classifyError(status int, data []byte, retry time.Duration, processing bool) *APIError {
	var wire struct {
		Code string `json:"ErrorType"`
	}
	_ = decode(data, &wire)
	result := &APIError{Code: wire.Code, Status: status}
	switch wire.Code {
	case "OE_INVALID_API_KEY", "OE_API_KEY_DISABLED", "OE_API_KEY_BLOCKED_PAYMENT_FAILED", "OE_API_KEY_IP_RESTRICTED", "OE_FREE_USAGE_LIMIT_REACHED":
		result.Message = accountMessage(wire.Code)
		result.TerminalScope = ScopeAccount
	case "OE_CONTEXT_NOT_FOUND", "OE_CONTEXT_EXPIRED", "OE_CONTEXT_CANCELLED":
		result.Message = "This Gadget print context is unavailable. Monitoring has stopped."
		result.TerminalScope = ScopeContext
	case "OE_BAD_ARGS", "OE_ARGS_PARSE_FAILED":
		result.Message = "Gadget rejected the request. Check the extension configuration and camera image."
		result.TerminalScope = ScopeRequest
	case "OE_CONTEXT_RATE_LIMITED", "OE_BACKEND_THROTTLED", "OE_IMAGE_DECODE_FAILED":
		result.Message = "Gadget could not inspect this image. Wait before sending a fresh snapshot."
		result.Retryable = true
	case "OE_INTERNAL_ERROR":
		result.Message = "Gadget could not complete the inspection. A later inspection can retry."
		result.Retryable = true
		result.UseFallback = processing
	default:
		// Do not echo an unknown ErrorType: it can contain account credentials or
		// an entire response body. Preserve only the numeric HTTP status.
		result.Code = "OE_HTTP_ERROR"
		result.Message = "Gadget returned an unexpected HTTP response."
		result.Retryable = status == 408 || status == 429 || status >= 500
		result.UseFallback = processing && (status == 408 || status >= 500)
		if !result.Retryable {
			result.TerminalScope = ScopeRequest
		}
	}
	if result.Retryable {
		result.RetryAfter = retry
	}
	return result
}

func accountMessage(code string) string {
	switch code {
	case "OE_FREE_USAGE_LIMIT_REACHED":
		return "The Gadget free allowance is exhausted. After renewal or changing billing settings, save your Gadget API key again to resume monitoring."
	case "OE_API_KEY_IP_RESTRICTED":
		return "This public IP is reserved for another Gadget key. Use the existing key or contact support."
	case "OE_API_KEY_BLOCKED_PAYMENT_FAILED":
		return "Gadget account billing needs attention."
	case "OE_API_KEY_DISABLED":
		return "The Gadget API key is disabled."
	default:
		return "The Gadget API key or owning account is unavailable."
	}
}
