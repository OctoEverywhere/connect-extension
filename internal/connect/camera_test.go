package connect

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testJPEG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, image.NewRGBA(image.Rect(0, 0, 4, 4)), nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestCameraFreshnessAndIdentity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	frame := testJPEG(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("missing credential")
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Last-Modified", now.Format(http.TimeFormat))
		_, _ = w.Write(frame)
	}))
	defer server.Close()
	c, err := newCameraClient(server.URL, "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.now = func() time.Time { return now.Add(time.Second) }
	snapshot, err := c.Snapshot(context.Background(), "bed-cam", now.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(snapshot.JPEG, frame) || snapshot.ID == "" || !snapshot.CapturedAt.Equal(now) {
		t.Fatal("invalid snapshot")
	}
	if _, err = c.Snapshot(context.Background(), "bed-cam", now); !errors.Is(err, ErrNoNewFrame) {
		t.Fatalf("duplicate frame accepted: %v", err)
	}
	c.now = func() time.Time { return now.Add(MaxSnapshotAge + time.Second) }
	if _, err = c.Snapshot(context.Background(), "bed-cam", time.Time{}); !errors.Is(err, ErrStaleFrame) {
		t.Fatalf("stale frame accepted: %v", err)
	}
}

func TestCameraRejectsRedirectAndFiltersPrinter(t *testing.T) {
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/cameras" {
			if r.URL.Query().Get("device") != "mine" {
				t.Error("missing device filter")
			}
			_, _ = w.Write([]byte(`{"items":[{"name":"mine-cam","device_id":"mine","snapshot":true},{"name":"other-cam","device_id":"other","snapshot":true}]}`))
			return
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer server.Close()
	c, err := newCameraClient(server.URL, "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	cameras, err := c.Cameras(context.Background(), "mine")
	if err != nil {
		t.Fatal(err)
	}
	if len(cameras) != 1 || cameras[0].Name != "mine-cam" {
		t.Fatalf("camera visibility leaked: %+v", cameras)
	}
	if _, err = c.Snapshot(context.Background(), "mine-cam", time.Time{}); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("followed redirect")
	}
}

func TestCameraRefusesUnsafeInputsAndUnusableFrames(t *testing.T) {
	for _, origin := range []string{"http://localhost:9000", "https://localhost:12345"} {
		if _, err := NewCameraClient(origin, "secret"); err == nil {
			t.Fatalf("undeclared egress port accepted: %s", origin)
		}
	}
	for _, origin := range []string{"http://localhost", "https://localhost", "http://localhost:8080", "https://localhost:8443", "http://localhost:18480"} {
		client, err := NewCameraClient(origin, "secret")
		if err != nil {
			t.Fatalf("declared port refused: %v", err)
		}
		client.Close()
	}
	for _, origin := range []string{"file:///etc/passwd", "https://user:pass@example.com", "https://example.com/a", "https://example.com/?token=secret", "https://example.com/#x"} {
		if _, err := NewCameraClient(origin, "secret"); err == nil {
			t.Fatalf("accepted %s", origin)
		}
	}
	for _, name := range []string{"", "../other", "a/b", "a%2fb", "a?x=1", "a#frag", "a\\b"} {
		if validCameraName(name) {
			t.Fatalf("accepted camera %q", name)
		}
	}
	cases := []struct {
		name                        string
		status                      int
		contentType, modified, body string
		want                        error
	}{
		{"unauthorized", 401, "", "", "", ErrCameraUnauthorized},
		{"empty", 204, "", "", "", ErrNoNewFrame},
		{"not modified", 304, "", "", "", ErrNoNewFrame},
		{"missing timestamp", 200, "image/jpeg", "", "jpeg", ErrStaleFrame},
		{"bad jpeg", 200, "image/jpeg", time.Now().UTC().Format(http.TimeFormat), "not jpeg", ErrInvalidFrame},
		{"too large", 200, "image/jpeg", time.Now().UTC().Format(http.TimeFormat), strings.Repeat("x", MaxSnapshotBytes+1), ErrInvalidFrame},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.Header().Set("Last-Modified", test.modified)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			c, err := newCameraClient(server.URL, "secret", true)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			_, err = c.Snapshot(context.Background(), "bed-cam", time.Time{})
			if !errors.Is(err, test.want) {
				t.Fatalf("got %v, want %v", err, test.want)
			}
		})
	}
}
