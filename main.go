package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/octoeverywhere/connect-extension/internal/connect"
	"github.com/octoeverywhere/connect-extension/internal/detection"
	"github.com/octoeverywhere/connect-extension/internal/gadget"
	"github.com/octoeverywhere/connect-extension/internal/storage"
	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wire"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

//go:embed module.yaml
var manifestYAML []byte

//go:embed openapi.json
var openAPI []byte

var name = manifestScalar(manifestYAML, "name")
var release = extension.Version(manifestScalar(manifestYAML, "version"))

type Settings struct {
	ConnectURL string `json:"connect_url"`
}

type application struct {
	mu      sync.RWMutex
	manager *detection.Manager
	records *storage.Files
}

func manifestScalar(document []byte, key string) string {
	for _, line := range strings.Split(string(document), "\n") {
		if value, ok := strings.CutPrefix(line, key+":"); ok {
			if i := strings.Index(value, " #"); i >= 0 {
				value = value[:i]
			}
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "OctoEverywhere stopped:", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	line, err := (wire.Handshake{Protocol: wire.ProtocolMajor, MinMinor: wire.MinMinor, MaxMinor: wire.MaxMinor, Address: wire.StdioAddress}).Line()
	if err != nil {
		return err
	}
	if _, err = os.Stdout.WriteString(line); err != nil {
		return err
	}
	methods := wirerpc.Methods{}
	conn, err := wirerpc.NewConn(stdio{}, wirerpc.Options{Role: wirerpc.RoleChild, Methods: methods})
	if err != nil {
		return err
	}
	api, err := gadget.NewClient(gadget.Options{})
	if err != nil {
		return err
	}
	app := &application{}
	var worker sync.WaitGroup
	maps.Copy(methods, wirerpc.Methods{
		wirerpc.MethodInfo: func(context.Context, wirerpc.Call) (any, error) {
			return routesapi.Describe(wirerpc.Info{Name: name, Version: release, Contract: wirerpc.Contract, Serves: []wirerpc.Method{wirerpc.MethodConfigure}}, openAPI)
		},
		wirerpc.MethodConfigure: wirerpc.ConfigureGranted(func(_ context.Context, s Settings, grants wirerpc.Grants) error {
			if s.ConnectURL == "" {
				s.ConnectURL = "http://127.0.0.1:8080"
			}
			checked, err := connect.NewCameraClient(s.ConnectURL, "configuration-validation")
			if err != nil {
				return err
			}
			checked.Close()
			app.mu.Lock()
			defer app.mu.Unlock()
			if app.manager != nil {
				return errors.New("extension was already configured")
			}
			records, err := storage.NewFiles(grants.Data)
			if err != nil {
				return err
			}
			app.records = records
			app.manager = detection.New(records, connect.New(conn), api, func(url, token string) (detection.CameraSource, error) { return connect.NewCameraClient(url, token) }, s.ConnectURL)
			worker.Add(1)
			go func() { defer worker.Done(); app.manager.Run(ctx) }()
			fmt.Fprintln(os.Stderr, "OctoEverywhere configured with private persistent storage")
			return nil
		}),
		wirerpc.MethodShutdown: func(context.Context, wirerpc.Call) (any, error) { stop(); return nil, nil },
		routesapi.MethodServe:  routesapi.Serve(app.routes()),
	})
	fmt.Fprintf(os.Stderr, "OctoEverywhere %s started\n", release)
	err = conn.Serve(ctx)
	stop()
	worker.Wait()
	if app.records != nil {
		if closeErr := app.records.Close(); closeErr != nil {
			fmt.Fprintln(os.Stderr, "OctoEverywhere could not close its persistent storage")
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (a *application) routes() http.Handler {
	mux := http.NewServeMux()
	handle := func(pattern string, write bool, fn func(*detection.Manager, http.ResponseWriter, *http.Request, string)) {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			principal := routesapi.PrincipalOf(r)
			if principal == "" {
				problem(w, 401, "AUTH_REQUIRED", "Sign in to Connect to use OctoEverywhere.")
				return
			}
			a.mu.RLock()
			m := a.manager
			a.mu.RUnlock()
			if m == nil {
				problem(w, 503, "NOT_READY", "OctoEverywhere is starting. Try again shortly.")
				return
			}
			if write && !m.CanManage(r.Context(), principal) {
				problem(w, 403, "ACCESS_DENIED", "You do not have permission to configure this extension.")
				return
			}
			fn(m, w, r, principal)
		})
	}
	handle("GET /status", false, func(m *detection.Manager, w http.ResponseWriter, r *http.Request, p string) {
		status, err := m.Status(r.Context(), p)
		if err != nil {
			problem(w, 503, "CONNECT_UNAVAILABLE", err.Error())
			return
		}
		answer(w, 200, status)
	})
	handle("POST /settings/gadget", true, func(m *detection.Manager, w http.ResponseWriter, r *http.Request, _ string) {
		var args struct {
			Key string `json:"api_key"`
		}
		if !readBody(w, r, &args) {
			return
		}
		if err := m.SaveKey(r.Context(), args.Key); err != nil {
			safeError(w, err)
			return
		}
		answer(w, 200, map[string]bool{"ok": true})
	})
	handle("POST /settings/connect", true, func(m *detection.Manager, w http.ResponseWriter, r *http.Request, _ string) {
		var args struct {
			URL   string `json:"url"`
			Token string `json:"token"`
		}
		if !readBody(w, r, &args) {
			return
		}
		if err := m.SaveCameraAccess(r.Context(), args.URL, args.Token); err != nil {
			safeError(w, err)
			return
		}
		answer(w, 200, map[string]bool{"ok": true})
	})
	handle("PUT /printers/{id}", true, func(m *detection.Manager, w http.ResponseWriter, r *http.Request, p string) {
		var args detection.PrinterSettings
		if !readBody(w, r, &args) {
			return
		}
		if err := m.ConfigurePrinter(r.Context(), p, r.PathValue("id"), args); err != nil {
			safeError(w, err)
			return
		}
		answer(w, 200, map[string]bool{"ok": true})
	})
	handle("POST /printers/{id}/test-camera", true, func(m *detection.Manager, w http.ResponseWriter, r *http.Request, p string) {
		if err := m.TestCamera(r.Context(), p, r.PathValue("id")); err != nil {
			safeError(w, err)
			return
		}
		answer(w, 200, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		problem(w, 404, "ROUTE_NOT_FOUND", "This OctoEverywhere route does not exist.")
	})
	return mux
}

func readBody(w http.ResponseWriter, r *http.Request, out any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		problem(w, 400, "INVALID_REQUEST", "The request is invalid or too large.")
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		problem(w, 400, "INVALID_REQUEST", "Send one JSON object.")
		return false
	}
	return true
}

func safeError(w http.ResponseWriter, err error) {
	var apiErr *gadget.APIError
	if errors.As(err, &apiErr) {
		problem(w, 422, apiErr.Code, apiErr.Message)
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		problem(w, 503, "REQUEST_TIMEOUT", "The request did not finish. Check the connection and try again.")
		return
	}
	// Our adapters only return locally generated credential-free messages. Store
	// and SDK errors are never forwarded because they can contain internal data.
	message := err.Error()
	if strings.Contains(message, "wirerpc:") {
		message = "Connect could not save the change. Try again shortly."
	}
	problem(w, 422, "REQUEST_FAILED", message)
}
func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func problem(w http.ResponseWriter, status int, code, detail string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code, "detail": detail})
}

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }
