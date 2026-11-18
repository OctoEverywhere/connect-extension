package detection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/octoeverywhere/connect-extension/internal/connect"
	"github.com/octoeverywhere/connect-extension/internal/gadget"
	"github.com/octoeverywhere/connect-extension/internal/storage"
)

type memoryStore struct {
	mu      sync.Mutex
	data    map[string][]byte
	fail    bool
	failKey string
}

func (s *memoryStore) Load(_ context.Context, k string, out any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.data[k]
	if !ok {
		return storage.ErrNotFound
	}
	return json.Unmarshal(v, out)
}
func (s *memoryStore) Save(_ context.Context, k string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail || s.failKey == k {
		return errors.New("storage unavailable")
	}
	b, err := json.Marshal(v)
	if err == nil {
		s.data[k] = b
	}
	return err
}
func (s *memoryStore) Keys(_ context.Context, prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []string
	for key := range s.data {
		if strings.HasPrefix(key, prefix) {
			result = append(result, key)
		}
	}
	return result, nil
}
func (s *memoryStore) failWrites() { s.mu.Lock(); s.fail = true; s.mu.Unlock() }

type testFleet struct {
	mu              sync.Mutex
	printers        map[string]connect.Printer
	pauses          []string
	visible         map[string]bool
	denyBackground  bool
	backgroundError error
}

func (f *testFleet) List(_ context.Context, principal string) ([]connect.Printer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := []connect.Printer{}
	for id, p := range f.printers {
		if principal == "" || f.visible == nil || f.visible[id] {
			result = append(result, p)
		}
	}
	return result, nil
}
func (f *testFleet) Get(_ context.Context, principal, id string) (connect.Printer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.printers[id]
	if !ok || (principal != "" && f.visible != nil && !f.visible[id]) {
		return connect.Printer{}, errors.New("denied")
	}
	return p, nil
}
func (f *testFleet) CanOperate(_ context.Context, principal, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if principal == "" {
		return !f.denyBackground, f.backgroundError
	}
	return true, nil
}
func (f *testFleet) CanManage(context.Context, string) (bool, error) { return true, nil }
func (f *testFleet) Pause(_ context.Context, id, job string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pauses = append(f.pauses, id+"/"+job)
	return nil
}
func (f *testFleet) set(p connect.Printer) { f.mu.Lock(); f.printers[p.ID] = p; f.mu.Unlock() }
func (f *testFleet) pauseCount() int       { f.mu.Lock(); defer f.mu.Unlock(); return len(f.pauses) }

type testGadget struct {
	mu                          sync.Mutex
	creates, processes, cancels int
	contexts                    []string
	result                      gadget.Result
	processErr                  error
	createErr                   error
	hook                        func(context.Context) (gadget.Result, error)
}

func (g *testGadget) Create(context.Context, string, int, int) (gadget.Context, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.creates++
	if g.createErr != nil {
		return gadget.Context{}, g.createErr
	}
	return gadget.Context{ID: fmt.Sprintf("secret-context-%d", g.creates), ProcessURL: "https://secret.example/process", FallbackURL: "https://secret.example/fallback"}, nil
}
func (g *testGadget) Process(ctx context.Context, c gadget.Context, _ []byte) (gadget.Result, error) {
	g.mu.Lock()
	g.processes++
	g.contexts = append(g.contexts, c.ID)
	result, err, hook := g.result, g.processErr, g.hook
	g.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return result, err
}
func (g *testGadget) Cancel(context.Context, gadget.Context) error {
	g.mu.Lock()
	g.cancels++
	g.mu.Unlock()
	return nil
}
func (g *testGadget) counts() (int, int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.creates, g.processes
}

type testCamera struct {
	mu        sync.Mutex
	snapshots int
	cameras   []connect.Camera
	image     []byte
	now       func() time.Time
}

func (c *testCamera) Cameras(_ context.Context, id string) ([]connect.Camera, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []connect.Camera{}
	for _, camera := range c.cameras {
		if camera.DeviceID == id {
			out = append(out, camera)
		}
	}
	return out, nil
}
func (c *testCamera) Snapshot(context.Context, string, time.Time) (connect.Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.snapshots++
	return connect.Snapshot{JPEG: c.image, ID: fmt.Sprintf("frame-%d", c.snapshots), CapturedAt: c.now().Add(time.Second)}, nil
}

type fixture struct {
	manager *Manager
	store   *memoryStore
	fleet   *testFleet
	api     *testGadget
	camera  *testCamera
	clock   atomic.Int64
	printer connect.Printer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	f.clock.Store(1_700_000_000)
	now := func() time.Time { return time.Unix(f.clock.Load(), 0).UTC() }
	f.printer = connect.Printer{ID: "p1", Name: "Test printer", JobID: "job1", JobActive: true, Connected: true, Printing: true, State: "PRINTING", Progress: 10, ProgressKnown: true, DurationKnown: true, DurationSeconds: 600, CanPause: true}
	f.store = &memoryStore{data: map[string][]byte{}}
	f.fleet = &testFleet{printers: map[string]connect.Printer{"p1": f.printer}}
	f.api = &testGadget{result: gadget.Result{Quality: 4, Minimum: 20 * time.Second, Recommended: 30 * time.Second}}
	var frame bytes.Buffer
	if err := jpeg.Encode(&frame, image.NewGray(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	f.camera = &testCamera{now: now, image: frame.Bytes(), cameras: []connect.Camera{{Name: "bed", DeviceID: "p1", Snapshot: true, Serving: true}}}
	f.manager = New(f.store, f.fleet, f.api, func(string, string) (CameraSource, error) { return f.camera, nil }, "http://localhost:8080")
	f.manager.now = now
	f.manager.ready = true
	f.manager.credentials = Credentials{Schema: schema, APIKey: "ACCOUNT-SECRET", ConnectToken: "CAMERA-SECRET", ConnectURL: "http://localhost:8080"}
	settings := defaults()
	settings.Enabled = true
	settings.CameraID = "bed"
	f.manager.records["p1"] = &Record{Schema: schema, PrinterID: "p1", Settings: settings, Session: Session{JobID: "job1", ArmedAt: now().Add(-time.Minute), Interval: 20 * time.Second}, Generation: 1}
	f.manager.runtime["p1"] = &runtimeState{printer: f.printer}
	t.Cleanup(func() { f.manager.workers.Wait() })
	return f
}
func (f *fixture) task() Record {
	f.manager.mu.Lock()
	defer f.manager.mu.Unlock()
	return *f.manager.records["p1"]
}
func (f *fixture) inspect() {
	f.manager.inspect(context.Background(), f.printer, f.task(), f.manager.credentials)
}

func TestNoInferenceDuringWarmupPausedOfflineOrDisabled(t *testing.T) {
	for _, state := range []string{"warmup", "paused", "offline", "disabled"} {
		t.Run(state, func(t *testing.T) {
			f := newFixture(t)
			p := f.printer
			switch state {
			case "warmup":
				f.manager.records["p1"].Session.ArmedAt = time.Time{}
				p.DurationSeconds = 10
			case "paused":
				p.Printing = false
				p.Paused = true
			case "offline":
				p.Connected = false
				p.Printing = false
			case "disabled":
				f.manager.records["p1"].Settings.Enabled = false
			}
			f.fleet.set(p)
			f.manager.reconcile(context.Background())
			f.clock.Add(30)
			f.manager.reconcile(context.Background())
			f.manager.workers.Wait()
			if creates, processes := f.api.counts(); creates != 0 || processes != 0 {
				t.Fatalf("%s submitted inference: creates=%d process=%d", state, creates, processes)
			}
			if f.fleet.pauseCount() != 0 {
				t.Fatal("paused without inference")
			}
		})
	}
}

func TestRestartRestoresExistingPrintContext(t *testing.T) {
	f := newFixture(t)
	r := f.manager.records["p1"]
	r.Session.Context = gadget.Context{ID: "persisted-context", ProcessURL: "secret-url", FallbackURL: "secret-fallback"}
	if err := f.store.Save(context.Background(), recordKey("p1"), r); err != nil {
		t.Fatal(err)
	}
	if err := f.store.Save(context.Background(), "credentials/v1", f.manager.credentials); err != nil {
		t.Fatal(err)
	}
	restarted := New(f.store, f.fleet, f.api, func(string, string) (CameraSource, error) { return f.camera, nil }, "http://localhost:8080")
	restarted.now = f.manager.now
	if err := restarted.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted.runtime["p1"] = &runtimeState{printer: f.printer}
	restarted.inspect(context.Background(), f.printer, *restarted.records["p1"], restarted.credentials)
	if creates, processes := f.api.counts(); creates != 0 || processes != 1 {
		t.Fatalf("restart recreated context: %d/%d", creates, processes)
	}
	if f.api.contexts[0] != "persisted-context" {
		t.Fatal("did not reuse persisted context")
	}
}

func TestUncertainInspectionPreservesLongerPacingAcrossRestart(t *testing.T) {
	for _, interval := range []time.Duration{20 * time.Second, 10 * time.Minute} {
		t.Run(interval.String(), func(t *testing.T) {
			f := newFixture(t)
			f.manager.records["p1"].Session.Interval = interval
			f.api.hook = func(context.Context) (gadget.Result, error) {
				// This is the last durable record a restart sees if the request
				// completes on the server but its response never reaches us.
				var saved Record
				if err := f.store.Load(context.Background(), recordKey("p1"), &saved); err != nil {
					t.Fatal(err)
				}
				want := f.manager.now().Add(max(interval, 2*time.Minute))
				if !saved.Session.Next.Equal(want) || saved.Session.LastFrame == "" {
					t.Fatalf("uncertain request was not durably paced: next=%v, want=%v", saved.Session.Next, want)
				}
				return gadget.Result{}, errors.New("response lost")
			}
			f.inspect()
		})
	}
}

func TestKeyReplacementCannotPersistBeforeOldContextsAreCleared(t *testing.T) {
	for _, failedKey := range []string{recordKey("p1"), "credentials/v1", ""} {
		t.Run("fail="+failedKey, func(t *testing.T) {
			f := newFixture(t)
			f.inspect()
			if err := f.store.Save(context.Background(), "credentials/v1", f.manager.credentials); err != nil {
				t.Fatal(err)
			}
			f.store.failKey = failedKey
			err := f.manager.SaveKey(context.Background(), "replacement-account-key")
			if (err != nil) != (failedKey != "") {
				t.Fatalf("key replacement result: %v", err)
			}
			var credentials Credentials
			if err := f.store.Load(context.Background(), "credentials/v1", &credentials); err != nil {
				t.Fatal(err)
			}
			var printer Record
			if err := f.store.Load(context.Background(), recordKey("p1"), &printer); err != nil {
				t.Fatal(err)
			}
			if failedKey != "" && credentials.APIKey != "ACCOUNT-SECRET" {
				t.Fatal("failed context invalidation committed the replacement account key")
			}
			if credentials.APIKey == "replacement-account-key" && printer.Session.Context.ID != "" {
				t.Fatal("restart could use an old account's context with a replacement key")
			}
			if f.manager.credentials.APIKey != credentials.APIKey {
				t.Fatal("memory and durable account keys disagree")
			}
		})
	}
}

func TestTerminalQuotaStopsWholeAccount(t *testing.T) {
	f := newFixture(t)
	f.api.processErr = &gadget.APIError{Code: "OE_FREE_USAGE_LIMIT_REACHED", Message: "Quota exhausted", TerminalScope: gadget.ScopeAccount}
	f.inspect()
	_, before := f.api.counts()
	p2 := f.printer
	p2.ID = "p2"
	p2.JobID = "job2"
	f.fleet.set(p2)
	r2 := *f.manager.records["p1"]
	r2.PrinterID = "p2"
	r2.Session = Session{JobID: "job2", ArmedAt: f.manager.now().Add(-time.Minute)}
	f.manager.records["p2"] = &r2
	f.clock.Add(600)
	f.manager.reconcile(context.Background())
	f.manager.workers.Wait()
	_, after := f.api.counts()
	if after != before {
		t.Fatal("continued inference after terminal quota")
	}
	if f.manager.runtime["p1"].state != "quota" || f.manager.runtime["p2"].state != "quota" {
		t.Fatal("quota not shown for whole account")
	}
}

func TestTerminalQuotaSurvivesRestartUntilKeyValidated(t *testing.T) {
	f := newFixture(t)
	f.api.processErr = &gadget.APIError{Code: "OE_FREE_USAGE_LIMIT_REACHED", Message: "Quota exhausted", TerminalScope: gadget.ScopeAccount}
	f.inspect()
	beforeCreates, beforeProcesses := f.api.counts()
	restarted := New(f.store, f.fleet, f.api, func(string, string) (CameraSource, error) { return f.camera, nil }, "http://localhost:8080")
	restarted.now = f.manager.now
	t.Cleanup(func() { restarted.workers.Wait() })
	if err := restarted.load(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(600)
	restarted.reconcile(context.Background())
	restarted.workers.Wait()
	if creates, processes := f.api.counts(); creates != beforeCreates || processes != beforeProcesses {
		t.Fatal("restarting retried a terminal account failure")
	}
	if restarted.runtime["p1"].state != "quota" || restarted.accountProblem == "" {
		t.Fatal("restart did not restore the account quota state")
	}
	status, err := restarted.Status(context.Background(), "user:viewer")
	if err != nil || !strings.Contains(status.Error, "OE_FREE_USAGE_LIMIT_REACHED") {
		t.Fatalf("quota missing from status: %+v, %v", status, err)
	}
	// Invalid replacements and failed saves must not release the account block.
	f.api.createErr = errors.New("invalid key")
	if err := restarted.SaveKey(context.Background(), "invalid-key"); err == nil || restarted.accountProblem == "" {
		t.Fatal("failed validation released the account block")
	}
	f.api.createErr = nil
	f.store.failWrites()
	if err := restarted.SaveKey(context.Background(), "replacement-key"); err == nil || restarted.accountProblem == "" {
		t.Fatal("failed persistence released the account block")
	}
	f.store.mu.Lock()
	f.store.fail = false
	f.store.mu.Unlock()
	// Revalidating the same key is an explicit retry after quota is resolved.
	if err := restarted.SaveKey(context.Background(), restarted.credentials.APIKey); err != nil {
		t.Fatal(err)
	}
	if restarted.accountProblem != "" || restarted.credentials.AccountProblem != "" {
		t.Fatal("successful key validation did not clear the account block")
	}
	var saved Credentials
	if err := f.store.Load(context.Background(), "credentials/v1", &saved); err != nil || saved.AccountProblem != "" {
		t.Fatalf("account recovery was not durable: %+v, %v", saved, err)
	}
}

func TestTerminalAccountPersistenceFailureStopsUntilDurable(t *testing.T) {
	f := newFixture(t)
	f.api.hook = func(context.Context) (gadget.Result, error) {
		f.store.mu.Lock()
		f.store.failKey = "credentials/v1"
		f.store.mu.Unlock()
		return gadget.Result{}, &gadget.APIError{Code: "OE_FREE_USAGE_LIMIT_REACHED", Message: "Quota exhausted", TerminalScope: gadget.ScopeAccount}
	}
	f.inspect()
	if f.manager.problem == "" || !f.manager.credentialsDirty || f.manager.accountProblem == "" {
		t.Fatal("failed terminal-state write did not stop monitoring")
	}
	f.clock.Add(600)
	f.manager.reconcile(context.Background())
	f.manager.workers.Wait()
	if f.manager.problem == "" || !f.manager.credentialsDirty {
		t.Fatal("successful printer record writes cleared an unsaved account failure")
	}
	if _, processes := f.api.counts(); processes != 1 {
		t.Fatal("continued inference with unsaved account state")
	}
	f.store.mu.Lock()
	f.store.failKey = ""
	f.store.mu.Unlock()
	f.manager.reconcile(context.Background())
	f.manager.workers.Wait()
	if f.manager.problem != "" || f.manager.credentialsDirty || f.manager.runtime["p1"].state != "quota" {
		t.Fatal("storage recovery did not persist the terminal account state")
	}
	var saved Credentials
	if err := f.store.Load(context.Background(), "credentials/v1", &saved); err != nil || saved.AccountProblem == "" {
		t.Fatalf("terminal account state was not saved on recovery: %+v, %v", saved, err)
	}
}

func TestChangedConnectOriginInvalidatesCameraTokenBeforeUse(t *testing.T) {
	for _, failSave := range []bool{false, true} {
		t.Run(fmt.Sprintf("save_failure=%t", failSave), func(t *testing.T) {
			f := newFixture(t)
			if err := f.store.Save(context.Background(), "credentials/v1", f.manager.credentials); err != nil {
				t.Fatal(err)
			}
			if err := f.store.Save(context.Background(), recordKey("p1"), f.manager.records["p1"]); err != nil {
				t.Fatal(err)
			}
			if failSave {
				f.store.failWrites()
			}
			var cameraCalls atomic.Int64
			newOrigin := "http://different-connect.local:8080"
			restarted := New(f.store, f.fleet, f.api, func(string, string) (CameraSource, error) {
				cameraCalls.Add(1)
				return f.camera, nil
			}, newOrigin)
			restarted.now = f.manager.now
			t.Cleanup(func() { restarted.workers.Wait() })
			err := restarted.load(context.Background())
			if failSave {
				if err == nil || restarted.ready {
					t.Fatal("load succeeded without durably invalidating the old-origin token")
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				restarted.reconcile(context.Background())
				restarted.workers.Wait()
				if restarted.runtime["p1"].state != "no_camera" {
					t.Fatal("origin change did not require new camera access")
				}
				var saved Credentials
				if err := f.store.Load(context.Background(), "credentials/v1", &saved); err != nil || saved.ConnectToken != "" || saved.ConnectURL != newOrigin {
					t.Fatalf("origin invalidation was not persisted: %+v, %v", saved, err)
				}
				if saved.APIKey != "ACCOUNT-SECRET" {
					t.Fatal("origin change removed the unrelated Gadget key")
				}
			}
			status, err := restarted.Status(context.Background(), "user:viewer")
			if err != nil || status.CameraConfigured || cameraCalls.Load() != 0 {
				t.Fatalf("old-origin token reached a camera client: configured=%t calls=%d err=%v", status.CameraConfigured, cameraCalls.Load(), err)
			}
		})
	}
}

func TestTerminalRequestStopsUntilSettingsOrCameraAccessSaved(t *testing.T) {
	for _, scope := range []gadget.Scope{gadget.ScopeRequest, gadget.ScopeContext} {
		for _, recovery := range []string{"printer settings", "camera access"} {
			t.Run(string(scope)+"/"+recovery, func(t *testing.T) {
				f := newFixture(t)
				f.api.processErr = &gadget.APIError{Code: "OE_BAD_ARGS", Message: "Request rejected", TerminalScope: scope}
				if scope == gadget.ScopeContext {
					f.api.processErr = &gadget.APIError{Code: "OE_CONTEXT_CANCELLED", Message: "Context cancelled", TerminalScope: scope}
				}
				f.inspect()
				if f.manager.records["p1"].Session.Blocked == "" || f.manager.records["p1"].Session.BlockedScope != scope {
					t.Fatal("non-retryable failure did not block the print")
				}
				if scope == gadget.ScopeRequest && !strings.Contains(f.manager.runtime["p1"].detail, "save the printer settings") {
					t.Fatal("request failure did not explain how to resume")
				}
				f.clock.Add(600)
				f.manager.reconcile(context.Background())
				f.manager.workers.Wait()
				if _, processes := f.api.counts(); processes != 1 {
					t.Fatal("non-retryable failure automatically repeated inference")
				}
				var err error
				if recovery == "printer settings" {
					err = f.manager.ConfigurePrinter(context.Background(), "user:operator", "p1", f.manager.records["p1"].Settings)
				} else {
					err = f.manager.SaveCameraAccess(context.Background(), f.manager.configuredURL, "updated-camera-token")
				}
				if err != nil {
					t.Fatal(err)
				}
				var saved Record
				if err := f.store.Load(context.Background(), recordKey("p1"), &saved); err != nil {
					t.Fatal(err)
				}
				if (saved.Session.Blocked == "") != (scope == gadget.ScopeRequest) {
					t.Fatalf("recovery cleared the wrong durable block: %+v", saved.Session)
				}
				f.api.processErr = nil
				f.manager.reconcile(context.Background())
				f.manager.workers.Wait()
				want := 1
				if scope == gadget.ScopeRequest {
					want = 2
				}
				if _, processes := f.api.counts(); processes != want {
					t.Fatalf("inference calls after recovery=%d, want=%d", processes, want)
				}
			})
		}
	}
}

func TestOldJobResponseNeverPausesReplacement(t *testing.T) {
	f := newFixture(t)
	f.manager.records["p1"].Settings.AutoPause = true
	f.api.hook = func(context.Context) (gadget.Result, error) {
		p := f.printer
		p.JobID = "new-job"
		f.fleet.set(p)
		return gadget.Result{Warning: true, Pause: true, Quality: 1, Minimum: 20 * time.Second, Recommended: 30 * time.Second}, nil
	}
	f.inspect()
	if f.fleet.pauseCount() != 0 {
		t.Fatal("old job paused replacement")
	}
	if f.manager.records["p1"].Session.Result != nil {
		t.Fatal("old job result was committed")
	}
}

func TestAutoPauseRequiresOptInAndIsRequestedOnce(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			f := newFixture(t)
			f.manager.records["p1"].Settings.AutoPause = enabled
			f.api.result = gadget.Result{Pause: true, Warning: true, Quality: 1, Minimum: 20 * time.Second, Recommended: 30 * time.Second}
			f.inspect()
			f.clock.Add(30)
			f.inspect()
			want := 0
			if enabled {
				want = 1
			}
			if got := f.fleet.pauseCount(); got != want {
				t.Fatalf("pause count=%d want=%d", got, want)
			}
		})
	}
	if defaults().AutoPause {
		t.Fatal("automatic pause is enabled by default")
	}
}

func TestAutoPauseRequiresModulePermissionToEnable(t *testing.T) {
	for _, checkError := range []bool{false, true} {
		t.Run(fmt.Sprintf("permission_error=%t", checkError), func(t *testing.T) {
			f := newFixture(t)
			if checkError {
				f.fleet.backgroundError = errors.New("permission check unavailable")
			} else {
				f.fleet.denyBackground = true
			}
			settings := f.manager.records["p1"].Settings
			settings.AutoPause = true
			err := f.manager.ConfigurePrinter(context.Background(), "user:operator", "p1", settings)
			if err == nil || !strings.Contains(err.Error(), "sink:octoeverywhere") {
				t.Fatalf("operator user enabled auto-pause without a module grant: %v", err)
			}
			if f.manager.records["p1"].Settings.AutoPause {
				t.Fatal("rejected configuration enabled automatic pause")
			}
		})
	}
}

func TestAutoPausePermissionRevokedDuringInferencePreventsCommand(t *testing.T) {
	f := newFixture(t)
	f.manager.records["p1"].Settings.AutoPause = true
	f.api.hook = func(context.Context) (gadget.Result, error) {
		f.fleet.mu.Lock()
		f.fleet.denyBackground = true
		f.fleet.mu.Unlock()
		return gadget.Result{Pause: true, Warning: true, Quality: 1, Minimum: 20 * time.Second, Recommended: 30 * time.Second}, nil
	}
	f.inspect()
	if f.fleet.pauseCount() != 0 || f.manager.records["p1"].Session.PauseRequested {
		t.Fatal("revoked module grant issued or consumed the automatic pause request")
	}
	if !strings.Contains(f.manager.runtime["p1"].detail, "sink:octoeverywhere") {
		t.Fatal("missing actionable module permission error")
	}
	f.fleet.mu.Lock()
	f.fleet.denyBackground = false
	f.fleet.mu.Unlock()
	f.api.hook = nil
	f.api.result.Pause = true
	f.clock.Add(30)
	f.inspect()
	if f.fleet.pauseCount() != 1 {
		t.Fatal("restoring the module grant did not allow a later pause suggestion")
	}
}

func TestDisableDoesNotRequireCameraOrBackgroundPauseAccess(t *testing.T) {
	f := newFixture(t)
	f.manager.records["p1"].Settings.AutoPause = true
	f.fleet.denyBackground = true
	f.printer.CanPause = false
	f.fleet.set(f.printer)
	var cameraCalls atomic.Int64
	f.manager.cameras = func(string, string) (CameraSource, error) {
		cameraCalls.Add(1)
		return nil, connect.ErrCameraUnauthorized
	}
	settings := f.manager.records["p1"].Settings
	settings.Enabled = false
	if err := f.manager.ConfigurePrinter(context.Background(), "user:operator", "p1", settings); err != nil {
		t.Fatalf("could not disable monitoring after dependencies became unavailable: %v", err)
	}
	if f.manager.records["p1"].Settings.Enabled || cameraCalls.Load() != 0 {
		t.Fatal("disabling monitoring contacted its unavailable camera")
	}
	var saved Record
	if err := f.store.Load(context.Background(), recordKey("p1"), &saved); err != nil || saved.Settings.Enabled {
		t.Fatalf("disabled setting was not durable: %+v, %v", saved.Settings, err)
	}
}

func TestCameraCompatibilityIsCheckedBeforeGadgetCalls(t *testing.T) {
	for _, frame := range []string{"valid", "invalid JPEG", "oversized JPEG"} {
		t.Run(frame, func(t *testing.T) {
			f := newFixture(t)
			switch frame {
			case "invalid JPEG":
				f.camera.image = []byte("not a JPEG")
			case "oversized JPEG":
				f.camera.image = append(f.camera.image, make([]byte, gadget.MaxImageBytes+1-len(f.camera.image))...)
			}
			err := f.manager.TestCamera(context.Background(), "user:operator", "p1")
			if (err == nil) != (frame == "valid") {
				t.Fatalf("camera compatibility result: %v", err)
			}
			if creates, processes := f.api.counts(); creates != 0 || processes != 0 {
				t.Fatal("camera test called Gadget")
			}
			if frame != "valid" {
				f.inspect()
				if creates, processes := f.api.counts(); creates != 0 || processes != 0 {
					t.Fatal("incompatible image reached Gadget before validation")
				}
				if f.manager.records["p1"].Session.BlockedScope != gadget.ScopeRequest {
					t.Fatal("incompatible image did not stop the session with a recoverable request error")
				}
			}
		})
	}
}

func TestCameraOwnershipAndStatusDoNotExposeSecrets(t *testing.T) {
	f := newFixture(t)
	settings := f.manager.records["p1"].Settings
	settings.CameraID = "somebody-elses-camera"
	if err := f.manager.ConfigurePrinter(context.Background(), "user:operator", "p1", settings); err == nil {
		t.Fatal("accepted another printer's camera")
	}
	f.manager.records["p1"].Session.Context = gadget.Context{ID: "CONTEXT-SECRET", ProcessURL: "PROCESS-SECRET", FallbackURL: "FALLBACK-SECRET"}
	status, err := f.manager.Status(context.Background(), "user:viewer")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ACCOUNT-SECRET", "CAMERA-SECRET", "CONTEXT-SECRET", "PROCESS-SECRET", "FALLBACK-SECRET"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("status leaked %s", secret)
		}
	}
	if f.manager.records["p1"].Settings.CameraID != "bed" {
		t.Fatal("invalid configuration mutated settings")
	}
}

func TestStorageFailurePreventsInferenceAndPause(t *testing.T) {
	f := newFixture(t)
	f.manager.records["p1"].Settings.AutoPause = true
	f.api.result.Pause = true
	f.store.failWrites()
	f.manager.reconcile(context.Background())
	f.manager.workers.Wait()
	if creates, processes := f.api.counts(); creates != 0 || processes != 0 {
		t.Fatalf("acted without durable state %d/%d", creates, processes)
	}
	if f.fleet.pauseCount() != 0 || f.manager.problem == "" {
		t.Fatal("persistence failure was not fail-closed")
	}
}

func TestDisableWhileInferencePendingDiscardsResponse(t *testing.T) {
	f := newFixture(t)
	f.manager.records["p1"].Settings.AutoPause = true
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	f.api.hook = func(context.Context) (gadget.Result, error) {
		close(started)
		<-release
		return gadget.Result{Pause: true, Warning: true, Quality: 1, Minimum: 20 * time.Second, Recommended: 30 * time.Second}, nil
	}
	go func() { defer close(done); f.inspect() }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("inspection did not start")
	}
	settings := f.manager.records["p1"].Settings
	settings.Enabled = false
	if err := f.manager.ConfigurePrinter(context.Background(), "user:operator", "p1", settings); err != nil {
		close(release)
		<-done
		t.Fatal(err)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("inspection did not finish")
	}
	if f.fleet.pauseCount() != 0 {
		t.Fatal("late result paused disabled monitoring")
	}
	if f.manager.records["p1"].Session.Result != nil {
		t.Fatal("late result was committed")
	}
}
