package gadget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testID = "print_context_123"
const validResult = `{"NextProcessIntervalSec":{"Minimum":5,"Recommended":20},"PrintQuality":8,"WarningSuggested":false,"PauseSuggested":false,"Score":12}`

func newTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(Options{BaseURL: server.URL, HTTPClient: server.Client(), AllowLoopbackHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func testContext(server *httptest.Server) Context {
	return Context{ID: testID, ProcessURL: server.URL + "/api/gadget/v1/process/" + testID, FallbackURL: server.URL + "/api/gadget/v1/process/" + testID}
}

func testJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var data bytes.Buffer
	if err := jpeg.Encode(&data, image.NewGray(image.Rect(0, 0, width, height)), nil); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func requireAPIError(t *testing.T, err error) *APIError {
	t.Helper()
	var apiError *APIError
	if !errors.As(err, &apiError) {
		t.Fatalf("expected APIError, got %v", err)
	}
	return apiError
}

func TestCompleteWireFlow(t *testing.T) {
	jpegBytes := testJPEG(t, 32, 24)
	requests := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/gadget/v1/createcontext":
			if r.Header.Get("X-API-Key") != "private-key" {
				t.Error("missing creation key")
			}
			var body map[string]int
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["WarningConfidenceLevel"] != 2 || body["PauseConfidenceLevel"] != 4 {
				t.Errorf("confidence body: %#v", body)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ContextId": testID, "ProcessRequestUrl": server.URL + "/api/gadget/v1/process/" + testID, "FallbackProcessRequestUrl": server.URL + "/api/gadget/v1/process/" + testID, "ExpiresAtUtc": "2026-11-18T12:00:00Z"})
		case "/api/gadget/v1/process/" + testID:
			if r.Header.Get("X-API-Key") != "" {
				t.Error("process must not send the account key")
			}
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Error(err)
				return
			}
			if part.Header.Get("Content-Type") != "image/jpeg" || part.FileName() != "snapshot.jpg" {
				t.Error("invalid image part headers")
			}
			body, _ := io.ReadAll(part)
			if !bytes.Equal(body, jpegBytes) {
				t.Error("image bytes changed")
			}
			if _, err := reader.NextPart(); err != io.EOF {
				t.Error("expected exactly one file")
			}
			_, _ = io.WriteString(w, validResult)
		case "/api/gadget/v1/cancelcontext/" + testID:
			if r.Header.Get("X-API-Key") != "" {
				t.Error("cancel must not send account key")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"ContextId": testID, "IsCancelled": true})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newTestClient(t, server)
	printContext, err := client.Create(context.Background(), " private-key ", 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	if printContext.ID != testID || printContext.ExpiresAt == nil {
		t.Fatal("context fields missing")
	}
	result, err := client.Process(context.Background(), printContext, jpegBytes)
	if err != nil {
		t.Fatal(err)
	}
	if result.Quality != 8 || result.Score != 12 || result.Warning || result.Pause || result.FasterInspection || result.Minimum != 5*time.Second || result.Recommended != 20*time.Second {
		t.Fatalf("unexpected result: %+v", result)
	}
	if err := client.Cancel(context.Background(), printContext); err != nil {
		t.Fatal(err)
	}
	if requests != 3 {
		t.Fatalf("expected 3 requests, got %d", requests)
	}
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		code                string
		status              int
		retryable, fallback bool
		scope               Scope
	}{
		{"OE_INVALID_API_KEY", 401, false, false, ScopeAccount},
		{"OE_API_KEY_DISABLED", 403, false, false, ScopeAccount},
		{"OE_API_KEY_BLOCKED_PAYMENT_FAILED", 403, false, false, ScopeAccount},
		{"OE_API_KEY_IP_RESTRICTED", 403, false, false, ScopeAccount},
		{"OE_FREE_USAGE_LIMIT_REACHED", 429, false, false, ScopeAccount},
		{"OE_CONTEXT_RATE_LIMITED", 429, true, false, ScopeNone},
		{"OE_BACKEND_THROTTLED", 503, true, false, ScopeNone},
		{"OE_IMAGE_DECODE_FAILED", 400, true, false, ScopeNone},
		{"OE_BAD_ARGS", 400, false, false, ScopeRequest},
		{"OE_ARGS_PARSE_FAILED", 400, false, false, ScopeRequest},
		{"OE_CONTEXT_NOT_FOUND", 404, false, false, ScopeContext},
		{"OE_CONTEXT_EXPIRED", 410, false, false, ScopeContext},
		{"OE_CONTEXT_CANCELLED", 410, false, false, ScopeContext},
		{"OE_INTERNAL_ERROR", 500, true, true, ScopeNone},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Retry-After", "37")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(map[string]string{"ErrorType": tc.code, "ErrorDetails": "SECRET-KEY https://secret-context"})
			}))
			defer server.Close()
			_, err := newTestClient(t, server).Process(context.Background(), testContext(server), testJPEG(t, 4, 4))
			apiError := requireAPIError(t, err)
			if apiError.Code != tc.code || apiError.Status != tc.status || apiError.Retryable != tc.retryable || apiError.UseFallback != tc.fallback || apiError.TerminalScope != tc.scope {
				t.Fatalf("unexpected error: %+v", apiError)
			}
			if apiError.Retryable && apiError.RetryAfter != 37*time.Second {
				t.Error("retry delay lost")
			}
			if !apiError.Retryable && apiError.RetryAfter != 0 {
				t.Error("terminal error advertises retry")
			}
			if strings.Contains(apiError.Error(), "SECRET") || strings.Contains(apiError.Error(), "secret-context") {
				t.Error("secret in error")
			}
			if calls != 1 {
				t.Fatal("client retried request")
			}
		})
	}
}

func TestResultValidationPreservesPacing(t *testing.T) {
	cases := []struct {
		name, body string
		delay      time.Duration
	}{
		{"invalid-json", "<html>unavailable</html>", 0},
		{"trailing-json", validResult + `{}`, 0},
		{"missing-interval", `{"PrintQuality":9}`, 0},
		{"zero-minimum", strings.Replace(validResult, `"Minimum":5`, `"Minimum":0`, 1), 0},
		{"recommended-below-minimum", strings.Replace(validResult, `"Recommended":20`, `"Recommended":4`, 1), 5 * time.Second},
		{"missing-recommended", strings.Replace(validResult, `,"Recommended":20`, ``, 1), 5 * time.Second},
		{"wrong-recommended-type", strings.Replace(validResult, `"Recommended":20`, `"Recommended":"20"`, 1), 5 * time.Second},
		{"overflow-minimum", strings.Replace(validResult, `"Minimum":5`, `"Minimum":9223372036854775807`, 1), 0},
		{"bad-quality", strings.Replace(validResult, `"PrintQuality":8`, `"PrintQuality":11`, 1), 20 * time.Second},
		{"wrong-quality-type", strings.Replace(validResult, `"PrintQuality":8`, `"PrintQuality":"8"`, 1), 20 * time.Second},
		{"bad-score", strings.Replace(validResult, `"Score":12`, `"Score":-1`, 1), 20 * time.Second},
		{"null-warning", strings.Replace(validResult, `"WarningSuggested":false`, `"WarningSuggested":null`, 1), 20 * time.Second},
		{"string-pause", strings.Replace(validResult, `"PauseSuggested":false`, `"PauseSuggested":"false"`, 1), 20 * time.Second},
		{"null-faster", strings.TrimSuffix(validResult, "}") + `,"FasterInspectionSuggested":null}`, 20 * time.Second},
		{"string-faster", strings.TrimSuffix(validResult, "}") + `,"FasterInspectionSuggested":"true"}`, 20 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseResult([]byte(tc.body))
			apiError := requireAPIError(t, err)
			if !apiError.Retryable || apiError.RetryAfter != tc.delay {
				t.Fatalf("unexpected pacing: %+v, want %s", apiError, tc.delay)
			}
		})
	}
	result, err := parseResult([]byte(strings.TrimSuffix(validResult, "}") + `,"FasterInspectionSuggested":true,"FutureField":"ignored"}`))
	if err != nil || !result.FasterInspection {
		t.Fatalf("valid optional hint: %+v %v", result, err)
	}
}

func TestOriginAndContextValidation(t *testing.T) {
	client, err := NewClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	good := "https://gadget-eu.octoeverywhere.com/api/gadget/v1/process/" + testID
	if !client.validProcessURL(good, testID) {
		t.Fatal("valid regional URL rejected")
	}
	for _, bad := range []string{
		"http://gadget-eu.octoeverywhere.com/api/gadget/v1/process/" + testID,
		"https://octoeverywhere.com.evil.test/api/gadget/v1/process/" + testID,
		"https://evil.test/api/gadget/v1/process/" + testID,
		"https://user:password@octoeverywhere.com/api/gadget/v1/process/" + testID,
		"https://octoeverywhere.com:8443/api/gadget/v1/process/" + testID,
		good + "?secret=value", good + "#fragment", good + "?", good + "/extra",
		strings.Replace(good, testID, "another", 1),
		strings.Replace(good, "process/", "process/%70", 1),
		"https://127.0.0.1/api/gadget/v1/process/" + testID,
	} {
		if client.validProcessURL(bad, testID) {
			t.Errorf("accepted invalid processing URL: %s", bad)
		}
	}
	for _, base := range []string{"http://octoeverywhere.com", "https://evil.test", "https://octoeverywhere.com/path", "https://octoeverywhere.com?x=1", "https://octoeverywhere.com:8443", "http://127.0.0.1:1234"} {
		if _, err := NewClient(Options{BaseURL: base}); err == nil {
			t.Errorf("accepted unsafe base: %s", base)
		}
	}
	if _, err := NewClient(Options{BaseURL: "http://example.test", AllowLoopbackHTTP: true}); err == nil {
		t.Error("loopback exception allowed public host")
	}
	local, err := NewClient(Options{BaseURL: "http://127.0.0.1:1234", AllowLoopbackHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	if local.validProcessURL("http://127.0.0.1:5678/api/gadget/v1/process/"+testID, testID) {
		t.Error("loopback exception allowed different port")
	}
}

func TestRedirectsNeverForwardSecrets(t *testing.T) {
	redirected := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected++; w.WriteHeader(200) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	original := &http.Client{Timeout: time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return nil }}
	client, err := NewClient(Options{BaseURL: server.URL, HTTPClient: original, AllowLoopbackHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Create(context.Background(), "private-key", 3, 3)
	apiError := requireAPIError(t, err)
	if apiError.Status != 307 || redirected != 0 {
		t.Fatal("redirect followed")
	}
	if original.Timeout != time.Minute || original.CheckRedirect == nil {
		t.Fatal("caller client mutated")
	}
	if client.http.Timeout != requestTimeout {
		t.Fatal("timeout not bounded")
	}
}

func TestImageValidation(t *testing.T) {
	if err := validateJPEG(testJPEG(t, 640, 480)); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, []byte("not a jpeg"), bytes.Repeat([]byte{'x'}, MaxImageBytes+1), testJPEG(t, 8193, 1), testJPEG(t, 5001, 4000)} {
		if err := validateJPEG(data); err == nil {
			t.Fatal("accepted invalid image")
		}
	}
}

func TestCreateAndCancelResponseValidation(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"ContextId":"secret","ProcessRequestUrl":"https://evil.test","FallbackProcessRequestUrl":"https://evil.test"}`, `{"ContextId":"../escape"}`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, body) }))
		client := newTestClient(t, server)
		if _, err := client.Create(context.Background(), "key", 3, 3); err == nil {
			t.Error("invalid create response accepted")
		}
		if err := client.Cancel(context.Background(), testContext(server)); err == nil {
			t.Error("invalid cancel response accepted")
		}
		server.Close()
	}
}

func TestCancellationNetworkAndResponseBounds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", maxResponseBytes+1))
	}))
	client := newTestClient(t, server)
	printContext := testContext(server)
	_, err := client.Process(context.Background(), printContext, testJPEG(t, 4, 4))
	apiError := requireAPIError(t, err)
	if !apiError.Retryable || !apiError.UseFallback {
		t.Fatal("oversized response not rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Process(ctx, printContext, testJPEG(t, 4, 4))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	server.Close()
	_, err = client.Process(context.Background(), printContext, testJPEG(t, 4, 4))
	apiError = requireAPIError(t, err)
	if !apiError.Retryable || !apiError.UseFallback || strings.Contains(apiError.Error(), server.URL) {
		t.Fatalf("bad network error: %v", apiError)
	}
}

func TestRetryAfterAndUnknownErrors(t *testing.T) {
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{{"12", 12 * time.Second}, {"-1", 0}, {"garbage", 0}, {now.Add(17 * time.Second).Format(http.TimeFormat), 17 * time.Second}, {now.Add(-time.Second).Format(http.TimeFormat), 0}} {
		if got := retryAfter(tc.value, now); got != tc.want {
			t.Errorf("retryAfter(%q)=%s want %s", tc.value, got, tc.want)
		}
	}
	err := classifyError(502, []byte(`{"ErrorType":"my-secret-key","ErrorDetails":"secret"}`), 10*time.Second, true)
	if err.Code != "OE_HTTP_ERROR" || !err.Retryable || !err.UseFallback || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe unknown error: %v", err)
	}
	creationError := classifyError(500, []byte(`{"ErrorType":"OE_INTERNAL_ERROR"}`), 0, false)
	if creationError.UseFallback {
		t.Error("create cannot switch processing URL")
	}
}
