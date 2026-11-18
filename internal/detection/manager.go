package detection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/octoeverywhere/connect-extension/internal/connect"
	"github.com/octoeverywhere/connect-extension/internal/gadget"
	"github.com/octoeverywhere/connect-extension/internal/storage"
)

const schema = 1
const pollInterval = 5 * time.Second
const autoPausePermissionMessage = "Automatic pause requires Operator access for sink:octoeverywhere on this printer. Ask an administrator to grant it using Connect's grants API; see the extension README, then try again."

type runtimeState struct {
	state, detail string
	printer       connect.Printer
	cancel        context.CancelFunc
}

// Manager serializes durable transitions and runs at most three inspections in
// parallel. Each printer has one worker; no network call holds the fleet loop.
type Manager struct {
	mu               sync.Mutex
	store            storage.Store
	fleet            Fleet
	gadget           gadget.Service
	cameras          CameraFactory
	credentials      Credentials
	credentialsDirty bool
	records          map[string]*Record
	runtime          map[string]*runtimeState
	ready            bool
	problem          string
	accountProblem   string
	configuredURL    string
	wake             chan struct{}
	slots            chan struct{}
	workers          sync.WaitGroup
	now              func() time.Time
}

func New(store storage.Store, fleet Fleet, api gadget.Service, cameras CameraFactory, connectURL string) *Manager {
	return &Manager{store: store, fleet: fleet, gadget: api, cameras: cameras, configuredURL: strings.TrimRight(connectURL, "/"),
		records: map[string]*Record{}, runtime: map[string]*runtimeState{}, wake: make(chan struct{}, 1), slots: make(chan struct{}, 3), now: time.Now}
}

func recordKey(id string) string {
	hash := sha256.Sum256([]byte(id))
	return "printers/v1/" + hex.EncodeToString(hash[:])
}

func (m *Manager) load(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := Credentials{Schema: schema}
	if err := m.store.Load(ctx, "credentials/v1", &c); err != nil && !errors.Is(err, storage.ErrNotFound) {
		return err
	}
	if c.Schema != schema {
		return errors.New("unsupported settings version")
	}
	if c.ConnectURL != m.configuredURL {
		// A token belongs to the origin where it was granted. Persist invalidation
		// before making credentials available to status or monitoring requests.
		c.ConnectToken = ""
		c.ConnectURL = m.configuredURL
		if err := m.store.Save(ctx, "credentials/v1", c); err != nil {
			return errors.New("could not reset camera access after the Connect URL changed")
		}
	}
	keys, err := m.store.Keys(ctx, "printers/v1/")
	if err != nil {
		return err
	}
	records := make(map[string]*Record, len(keys))
	for _, key := range keys {
		var r Record
		if err = m.store.Load(ctx, key, &r); err != nil {
			return err
		}
		if r.Schema != schema || r.PrinterID == "" {
			return errors.New("unsupported printer record version")
		}
		records[r.PrinterID] = &r
	}
	m.credentials = c
	m.credentialsDirty = false
	m.accountProblem = c.AccountProblem
	m.records = records
	m.ready = true
	m.problem = ""
	return nil
}

func (m *Manager) Run(ctx context.Context) {
	defer m.workers.Wait()
	for {
		if err := m.load(ctx); err == nil {
			break
		}
		m.mu.Lock()
		m.problem = "Waiting for Connect storage. Monitoring is stopped until settings can be loaded."
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		m.reconcile(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-m.wake:
		}
	}
}

func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) getRecordLocked(id string) *Record {
	if r := m.records[id]; r != nil {
		return r
	}
	r := &Record{Schema: schema, PrinterID: id, Settings: defaults(), History: []HistoryEntry{}}
	m.records[id] = r
	return r
}

func (m *Manager) saveLocked(ctx context.Context, r *Record) error {
	if err := m.store.Save(ctx, recordKey(r.PrinterID), r); err != nil {
		m.problem = "Could not save monitoring state. Monitoring is stopped until storage recovers."
		return err
	}
	return nil
}

func (m *Manager) saveCredentialsLocked(ctx context.Context) error {
	if err := m.store.Save(ctx, "credentials/v1", m.credentials); err != nil {
		m.credentialsDirty = true
		m.problem = "Could not save account state. Monitoring is stopped until storage recovers."
		return err
	}
	m.credentialsDirty = false
	return nil
}

func (m *Manager) historyLocked(r *Record, p connect.Printer, kind, message string, quality int) {
	r.History = append(r.History, HistoryEntry{At: m.now().UTC(), PrinterID: p.ID, PrinterName: p.Name, JobName: p.FileName, Kind: kind, Message: message, Quality: quality})
	if len(r.History) > 40 {
		r.History = append([]HistoryEntry(nil), r.History[len(r.History)-40:]...)
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	printers, err := m.fleet.List(readCtx, "")
	cancel()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err != nil {
		m.problem = "Connect printer state is unavailable. Monitoring is paused."
		for _, v := range m.runtime {
			if v.cancel != nil {
				v.cancel()
			}
		}
		return
	}
	// Successful persistence, not just a successful read, clears a storage fault.
	if m.credentialsDirty {
		if err := m.saveCredentialsLocked(ctx); err != nil {
			return
		}
	}
	if m.problem != "" {
		for _, r := range m.records {
			if err := m.saveLocked(ctx, r); err != nil {
				return
			}
		}
		m.problem = ""
	}
	seen := make(map[string]bool, len(printers))
	for _, p := range printers {
		seen[p.ID] = true
		r := m.getRecordLocked(p.ID)
		v := m.runtime[p.ID]
		if v == nil {
			v = &runtimeState{}
			m.runtime[p.ID] = v
		}
		v.printer = p
		changedJob := (p.JobActive && r.Session.JobID != p.JobID) || (p.JobTerminal && r.Session.JobID != "")
		if changedJob {
			if v.cancel != nil {
				v.cancel()
			}
			old := r.Session.Context
			r.Generation++
			r.Session = Session{JobID: p.JobID, Interval: 20 * time.Second}
			if err := m.saveLocked(ctx, r); err != nil {
				return
			}
			if old.ID != "" {
				m.cancelContext(ctx, old)
			}
		}
		switch {
		case !r.Settings.Enabled:
			v.state = "disabled"
			v.detail = "Monitoring is turned off for this printer."
		case m.credentials.APIKey == "":
			v.state = "setup"
			v.detail = "Add your Gadget API key to begin."
		case m.accountProblem != "":
			v.state = "error"
			v.detail = m.accountProblem
			if strings.Contains(m.accountProblem, "OE_FREE_USAGE_LIMIT_REACHED") {
				v.state = "quota"
			}
		case !p.Connected:
			v.state = "offline"
			v.detail = "Waiting for this printer to reconnect."
		case p.Paused:
			v.state = "paused"
			v.detail = "Inspections pause with the print."
			if r.Session.PauseRequested && !r.Session.PauseConfirmed {
				r.Session.PauseConfirmed = true
				m.historyLocked(r, p, "paused", "Printer pause confirmed.", 0)
				_ = m.saveLocked(ctx, r)
			}
		case !p.JobActive:
			v.state = "idle"
			v.detail = "Ready for the next print."
		case !p.Printing:
			v.state = "waiting_first_layer"
			v.detail = "Waiting for the printer to report active printing."
		case r.Session.Blocked != "":
			v.state = "error"
			v.detail = r.Session.Blocked
		case m.credentials.ConnectToken == "" || r.Settings.CameraID == "":
			v.state = "no_camera"
			v.detail = "Choose a camera and connect camera access."
		default:
			if r.Session.ArmedAt.IsZero() {
				ready, reason := r.Session.Warmup.Observe(p, m.now(), r.Settings.WarmupSeconds)
				if !ready {
					v.state = "waiting_first_layer"
					v.detail = reason
					break
				}
				r.Session.ArmedAt = m.now()
				m.historyLocked(r, p, "monitoring", reason, 0)
				if err := m.saveLocked(ctx, r); err != nil {
					return
				}
			}
			// Retain a recent inspection failure until the next attempt actually starts.
			if v.state != "error" && v.state != "no_camera" {
				v.state = "monitoring"
				v.detail = "Gadget is watching this print."
			}
			if v.cancel == nil && !m.now().Before(r.Session.Next) {
				select {
				case m.slots <- struct{}{}:
					workerCtx, stop := context.WithCancel(ctx)
					v.cancel = stop
					r.Session.Next = m.now().Add(max(r.Session.Interval, 20*time.Second))
					if err := m.saveLocked(ctx, r); err != nil {
						stop()
						v.cancel = nil
						<-m.slots
						return
					}
					task := *r
					credentials := m.credentials
					v.state = "checking"
					v.detail = "Checking a fresh camera image."
					m.workers.Add(1)
					go func() {
						defer m.workers.Done()
						defer func() {
							<-m.slots
							m.mu.Lock()
							if rv := m.runtime[p.ID]; rv != nil {
								rv.cancel = nil
							}
							m.mu.Unlock()
							m.Wake()
						}()
						m.inspect(workerCtx, p, task, credentials)
					}()
				default:
				}
			}
		}
		if (!r.Settings.Enabled || !p.Printing || m.accountProblem != "") && v.cancel != nil {
			v.cancel()
		}
		if !p.Printing {
			r.Session.Warmup.Observe(p, m.now(), r.Settings.WarmupSeconds)
		}
	}
	for id, v := range m.runtime {
		if !seen[id] {
			v.state = "offline"
			v.detail = "Printer is no longer available to the extension."
			if v.cancel != nil {
				v.cancel()
			}
		}
	}
}

func (m *Manager) cancelContext(ctx context.Context, c gadget.Context) {
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		bounded, stop := context.WithTimeout(ctx, 10*time.Second)
		defer stop()
		_ = m.gadget.Cancel(bounded, c)
	}()
}

func (m *Manager) current(ctx context.Context, id string, task Record) (connect.Printer, bool) {
	m.mu.Lock()
	r := m.records[id]
	ok := r != nil && r.Generation == task.Generation && r.Settings.Enabled && r.Session.JobID == task.Session.JobID && m.accountProblem == "" && m.problem == ""
	m.mu.Unlock()
	if !ok || ctx.Err() != nil {
		return connect.Printer{}, false
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	p, err := m.fleet.Get(ctx, "", id)
	return p, err == nil && p.Printing && p.JobID == task.Session.JobID
}

func (m *Manager) inspect(ctx context.Context, p connect.Printer, task Record, credentials Credentials) {
	// HTTP clients have their own deadlines; host RPC calls otherwise inherit
	// only the module lifetime and could occupy every inspection slot forever.
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cameras, err := m.cameras(m.configuredURL, credentials.ConnectToken)
	if err != nil {
		m.workerError(ctx, p, task, err)
		return
	}
	if closer, ok := cameras.(interface{ Close() }); ok {
		defer closer.Close()
	}
	available, err := cameras.Cameras(ctx, p.ID)
	if err != nil {
		m.workerError(ctx, p, task, err)
		return
	}
	found := false
	for _, c := range available {
		if c.Name == task.Settings.CameraID && c.Serving && c.Snapshot {
			found = true
			break
		}
	}
	if !found {
		m.workerError(ctx, p, task, connect.ErrCameraUnavailable)
		return
	}
	since := task.Session.ArmedAt
	if task.Session.LastFrameAt.After(since) {
		since = task.Session.LastFrameAt
	}
	snapshot, err := cameras.Snapshot(ctx, task.Settings.CameraID, since)
	if err != nil {
		m.workerError(ctx, p, task, err)
		return
	}
	if snapshot.ID == task.Session.LastFrame {
		m.workerError(ctx, p, task, connect.ErrNoNewFrame)
		return
	}
	if err := gadget.ValidateImage(snapshot.JPEG); err != nil {
		m.workerError(ctx, p, task, err)
		return
	}
	if _, ok := m.current(ctx, p.ID, task); !ok {
		return
	}
	if task.Session.Context.ID == "" {
		c, err := m.gadget.Create(ctx, credentials.APIKey, task.Settings.WarningConfidence, task.Settings.PauseConfidence)
		if err != nil {
			m.workerError(ctx, p, task, err)
			return
		}
		m.mu.Lock()
		r := m.records[p.ID]
		if r.Generation != task.Generation || !r.Settings.Enabled || ctx.Err() != nil {
			m.mu.Unlock()
			m.cancelContext(context.WithoutCancel(ctx), c)
			return
		}
		r.Session.Context = c
		task.Session.Context = c
		err = m.saveLocked(ctx, r)
		m.mu.Unlock()
		if err != nil {
			return
		}
	}
	if _, ok := m.current(ctx, p.ID, task); !ok {
		return
	}
	// Fence an uncertain POST durably before sending. After a crash, neither its
	// frame nor its attempt time is immediately replayed and charged twice.
	m.mu.Lock()
	r := m.records[p.ID]
	if r.Generation != task.Generation {
		m.mu.Unlock()
		return
	}
	r.Session.LastFrame = snapshot.ID
	r.Session.LastFrameAt = snapshot.CapturedAt
	r.Session.Next = m.now().Add(max(r.Session.Interval, 2*time.Minute))
	err = m.saveLocked(ctx, r)
	m.mu.Unlock()
	if err != nil {
		return
	}
	result, err := m.gadget.Process(ctx, task.Session.Context, snapshot.JPEG)
	if err != nil {
		m.workerError(ctx, p, task, err)
		return
	}
	current, ok := m.current(ctx, p.ID, task)
	if !ok {
		return
	}
	moduleCanPause := false
	if result.Pause && task.Settings.AutoPause && !task.Session.PauseRequested && current.CanPause {
		// Background commands run as the module, not the person who enabled
		// monitoring. Recheck its grant because permissions may have changed.
		allowed, err := m.fleet.CanOperate(ctx, "", p.ID)
		moduleCanPause = err == nil && allowed
	}
	m.mu.Lock()
	r = m.records[p.ID]
	if r.Generation != task.Generation {
		m.mu.Unlock()
		return
	}
	r.Session.Result = &result
	r.Session.ResultAt = m.now().UTC()
	r.Session.Interval = max(result.Minimum, result.Recommended)
	r.Session.Next = m.now().Add(r.Session.Interval)
	if result.Warning && !r.Session.WarningSent {
		r.Session.WarningSent = true
		m.historyLocked(r, p, "warning", "Gadget detected a possible print issue.", result.Quality)
	}
	if !result.Warning && !result.Pause {
		r.Session.WarningSent = false
	}
	shouldPause := result.Pause && r.Settings.AutoPause && !r.Session.PauseRequested && current.CanPause && moduleCanPause
	if shouldPause {
		r.Session.PauseRequested = true
		m.historyLocked(r, p, "pause_requested", "Gadget requested an automatic pause.", result.Quality)
	}
	v := m.runtime[p.ID]
	v.state = "monitoring"
	v.detail = "Gadget is watching this print."
	if result.Warning || result.Pause {
		v.detail = "Gadget detected a possible issue. Check the print."
	}
	if result.Pause && r.Settings.AutoPause && !r.Session.PauseRequested && current.CanPause && !moduleCanPause {
		v.detail = autoPausePermissionMessage
	}
	err = m.saveLocked(ctx, r)
	m.mu.Unlock()
	if err != nil {
		return
	}
	if shouldPause {
		if _, ok := m.current(ctx, p.ID, task); !ok {
			return
		}
		// A failed or timed-out command is never blindly retried; it may already
		// have reached the printer. Subsequent state confirms the outcome.
		pauseErr := m.fleet.Pause(ctx, p.ID, task.Session.JobID)
		m.mu.Lock()
		r = m.records[p.ID]
		if r.Generation == task.Generation {
			message := "Pause requested; waiting for printer confirmation."
			if pauseErr != nil {
				message = "Pause could not be confirmed. Check the printer; the command will not be repeated automatically."
			}
			m.historyLocked(r, p, "pause", message, result.Quality)
			_ = m.saveLocked(ctx, r)
		}
		m.mu.Unlock()
	}
}

func (m *Manager) workerError(ctx context.Context, p connect.Printer, task Record, err error) {
	if ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.records[p.ID]
	if r == nil || r.Generation != task.Generation {
		return
	}
	v := m.runtime[p.ID]
	v.state = "error"
	v.detail = "Inspection could not complete; retrying with a fresh image."
	delay := max(r.Session.Interval, 30*time.Second)
	var apiErr *gadget.APIError
	if errors.As(err, &apiErr) {
		v.detail = apiErr.Error()
		delay = max(delay, apiErr.RetryAfter)
		if apiErr.UseFallback && r.Session.Context.FallbackURL != "" {
			r.Session.Context.ProcessURL = r.Session.Context.FallbackURL
		}
		if !apiErr.Retryable {
			if apiErr.TerminalScope == gadget.ScopeAccount {
				m.accountProblem = apiErr.Error()
				m.credentials.AccountProblem = m.accountProblem
				// Quota and account failures survive restarts. If storage fails,
				// retain the terminal state in memory and retry its write before
				// allowing the storage recovery path to resume monitoring.
				_ = m.saveCredentialsLocked(ctx)
			} else if apiErr.TerminalScope == gadget.ScopeContext || apiErr.TerminalScope == gadget.ScopeRequest {
				r.Session.Blocked = apiErr.Error()
				r.Session.BlockedScope = apiErr.TerminalScope
				if apiErr.TerminalScope == gadget.ScopeRequest {
					r.Session.Blocked += " Monitoring is stopped. Fix the camera or configuration, then save the printer settings or camera access to retry."
				}
				v.detail = r.Session.Blocked
			}
			m.historyLocked(r, p, "error", v.detail, 0)
		}
	} else if errors.Is(err, connect.ErrNoNewFrame) || errors.Is(err, connect.ErrStaleFrame) || errors.Is(err, connect.ErrCameraUnavailable) || errors.Is(err, connect.ErrCameraUnauthorized) || errors.Is(err, connect.ErrInvalidFrame) {
		v.state = "no_camera"
		v.detail = err.Error()
	}
	r.Session.Next = m.now().Add(delay)
	_ = m.saveLocked(ctx, r)
}

func (m *Manager) readyLocked() error {
	if !m.ready {
		return errors.New("monitoring is still loading; try again shortly")
	}
	return nil
}

func (m *Manager) SaveKey(ctx context.Context, key string) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 512 {
		return errors.New("enter a valid Gadget API key")
	}
	c, err := m.gadget.Create(ctx, key, 3, 3)
	if err != nil {
		return err
	}
	defer m.cancelContext(context.WithoutCancel(ctx), c)
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = m.readyLocked(); err != nil {
		return err
	}
	next := m.credentials
	next.APIKey = key
	next.AccountProblem = ""
	changed := m.credentials.APIKey != key
	// A context bills its creating account, independently of the current key.
	// Clear every old context durably before committing a replacement key so a
	// crash between writes can never resume an old account's context under new
	// credentials. A failed record write retains the old key and stops monitoring.
	for id, r := range m.records {
		if v := m.runtime[id]; v != nil && v.cancel != nil {
			v.cancel()
		}
		r.Generation++
		if changed {
			if r.Session.Context.ID != "" {
				m.cancelContext(context.WithoutCancel(ctx), r.Session.Context)
			}
			r.Session.Context = gadget.Context{}
			r.Session.Blocked = ""
			r.Session.BlockedScope = gadget.ScopeNone
		}
		if err = m.saveLocked(ctx, r); err != nil {
			return err
		}
	}
	if err = m.store.Save(ctx, "credentials/v1", next); err != nil {
		return err
	}
	m.credentials = next
	m.credentialsDirty = false
	m.accountProblem = ""
	m.Wake()
	return nil
}

func (m *Manager) SaveCameraAccess(ctx context.Context, url, token string) error {
	if strings.TrimRight(url, "/") != m.configuredURL {
		return errors.New("change the Connect URL in Settings → Extensions → OctoEverywhere → Settings first")
	}
	camera, err := m.cameras(m.configuredURL, strings.TrimSpace(token))
	if err != nil {
		return err
	}
	if closer, ok := camera.(interface{ Close() }); ok {
		defer closer.Close()
	}
	// A sentinel device still authenticates the request while revealing no other
	// printer's images. Empty camera lists are valid before a camera is configured.
	if _, err = camera.Cameras(ctx, "octoeverywhere-credential-check"); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = m.readyLocked(); err != nil {
		return err
	}
	next := m.credentials
	next.ConnectToken = strings.TrimSpace(token)
	next.ConnectURL = m.configuredURL
	if err = m.store.Save(ctx, "credentials/v1", next); err != nil {
		return err
	}
	m.credentials = next
	m.credentialsDirty = false
	for id, r := range m.records {
		r.Generation++
		if v := m.runtime[id]; v != nil && v.cancel != nil {
			v.cancel()
		}
		if r.Session.BlockedScope == gadget.ScopeRequest {
			r.Session.Blocked = ""
			r.Session.BlockedScope = gadget.ScopeNone
			if err = m.saveLocked(ctx, r); err != nil {
				return err
			}
		}
	}
	m.Wake()
	return nil
}

func (m *Manager) ConfigurePrinter(ctx context.Context, principal, id string, settings PrinterSettings) error {
	if settings.WarningConfidence < 1 || settings.WarningConfidence > 5 || settings.PauseConfidence < 1 || settings.PauseConfidence > 5 || settings.WarmupSeconds < 60 || settings.WarmupSeconds > 1800 {
		return errors.New("confidence must be 1–5 and warm-up buffer 60–1800 seconds")
	}
	p, err := m.fleet.Get(ctx, principal, id)
	if err != nil {
		return errors.New("printer is unavailable or access was removed")
	}
	allowed, err := m.fleet.CanOperate(ctx, principal, id)
	if err != nil || !allowed {
		return errors.New("printer control permission is required to change monitoring")
	}
	if settings.Enabled && settings.AutoPause && !p.CanPause {
		return errors.New("this printer does not currently support pausing")
	}
	if settings.Enabled && settings.AutoPause {
		allowed, err := m.fleet.CanOperate(ctx, "", id)
		if err != nil || !allowed {
			return errors.New(autoPausePermissionMessage)
		}
	}
	m.mu.Lock()
	credentials := m.credentials
	m.mu.Unlock()
	if settings.Enabled && settings.CameraID == "" {
		return errors.New("choose a camera before enabling monitoring")
	}
	// Disabling must remain available when a camera or the module's command
	// permission stops working. Validate those dependencies when enabling only.
	if settings.Enabled && settings.CameraID != "" {
		camera, err := m.cameras(m.configuredURL, credentials.ConnectToken)
		if err != nil {
			return err
		}
		if closer, ok := camera.(interface{ Close() }); ok {
			defer closer.Close()
		}
		cameras, err := camera.Cameras(ctx, id)
		if err != nil {
			return err
		}
		found := false
		for _, c := range cameras {
			if c.Name == settings.CameraID && c.Snapshot {
				found = true
			}
		}
		if !found {
			return errors.New("selected camera does not belong to this printer or cannot provide snapshots")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err = m.readyLocked(); err != nil {
		return err
	}
	current := m.getRecordLocked(id)
	next := *current
	next.Settings = settings
	next.Generation++
	// Corrected camera/configuration can recover a rejected request, but cannot
	// revive a Gadget context that the service expired or cancelled.
	if next.Session.BlockedScope == gadget.ScopeRequest {
		next.Session.Blocked = ""
		next.Session.BlockedScope = gadget.ScopeNone
	}
	if current.Settings.CameraID != settings.CameraID {
		next.Session.LastFrame = ""
		next.Session.LastFrameAt = time.Time{}
	}
	// Confidence is fixed when a context is created; changes take effect next print.
	if err = m.saveLocked(ctx, &next); err != nil {
		return err
	}
	m.records[id] = &next
	if v := m.runtime[id]; v != nil && v.cancel != nil {
		v.cancel()
	}
	m.Wake()
	return nil
}

func (m *Manager) TestCamera(ctx context.Context, principal, id string) error {
	if _, err := m.fleet.Get(ctx, principal, id); err != nil {
		return errors.New("printer is unavailable")
	}
	m.mu.Lock()
	r := m.getRecordLocked(id)
	name := r.Settings.CameraID
	token := m.credentials.ConnectToken
	m.mu.Unlock()
	cameras, err := m.cameras(m.configuredURL, token)
	if err != nil {
		return err
	}
	if closer, ok := cameras.(interface{ Close() }); ok {
		defer closer.Close()
	}
	list, err := cameras.Cameras(ctx, id)
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.Name == name && c.Snapshot {
			snapshot, err := cameras.Snapshot(ctx, name, time.Time{})
			if err != nil {
				return err
			}
			return gadget.ValidateImage(snapshot.JPEG)
		}
	}
	return errors.New("choose a snapshot camera for this printer first")
}

func (m *Manager) Status(ctx context.Context, principal string) (Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	printers, err := m.fleet.List(ctx, principal)
	if err != nil {
		return Status{}, errors.New("Connect printer access is unavailable")
	}
	canManage, _ := m.fleet.CanManage(ctx, principal)
	m.mu.Lock()
	credentials := m.credentials
	out := Status{Configured: credentials.APIKey != "", CameraConfigured: credentials.ConnectToken != "", ConnectURL: m.configuredURL, CanManage: canManage, Error: m.problem, Printers: []PrinterStatus{}, History: []HistoryEntry{}}
	if out.Error == "" {
		out.Error = m.accountProblem
	}
	m.mu.Unlock()
	var camera CameraSource
	if credentials.ConnectToken != "" {
		camera, _ = m.cameras(m.configuredURL, credentials.ConnectToken)
		if closer, ok := camera.(interface{ Close() }); ok {
			defer closer.Close()
		}
	}
	for _, p := range printers {
		allowed, _ := m.fleet.CanOperate(ctx, principal, p.ID)
		m.mu.Lock()
		r := m.getRecordLocked(p.ID)
		v := m.runtime[p.ID]
		s := PrinterStatus{ID: p.ID, Name: p.Name, JobID: p.JobID, JobName: p.FileName, Progress: p.Progress, PrinterSettings: r.Settings, CanManage: canManage && allowed, Cameras: []CameraOption{}, State: "idle", Detail: "Waiting for printer state."}
		if v != nil {
			s.State = v.state
			s.Detail = v.detail
		}
		if r.Session.Result != nil {
			result := *r.Session.Result
			s.Quality = result.Quality
			s.Warning = result.Warning
			s.PauseSuggested = result.Pause
			t := r.Session.ResultAt
			s.LastInspectionAt = &t
		}
		out.History = append(out.History, r.History...)
		m.mu.Unlock()
		if camera != nil {
			cams, err := camera.Cameras(ctx, p.ID)
			if err == nil {
				for _, c := range cams {
					if c.Snapshot {
						s.Cameras = append(s.Cameras, CameraOption{ID: c.Name, Name: c.Name})
					}
				}
			}
		}
		out.Printers = append(out.Printers, s)
	}
	slices.SortFunc(out.History, func(a, b HistoryEntry) int { return b.At.Compare(a.At) })
	if len(out.History) > 100 {
		out.History = out.History[:100]
	}
	return out, nil
}

func (m *Manager) CanManage(ctx context.Context, principal string) bool {
	allowed, err := m.fleet.CanManage(ctx, principal)
	return err == nil && allowed
}
