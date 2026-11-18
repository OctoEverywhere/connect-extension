// Package connect adapts the public Prusa Connect module APIs.
package connect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/busapi"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

const pauseVerb = "core.command.pause_print"

var (
	ErrJobChanged       = errors.New("the print changed before the command could be sent")
	ErrNotPrinting      = errors.New("the printer is not currently printing")
	ErrPauseUnsupported = errors.New("this printer does not support pause")
	ErrPauseRefused     = errors.New("the printer refused the pause command")
)

// Printer is a snapshot, never an assertion that a machine has started depositing plastic.
type Printer struct {
	ID                   string              `json:"id"`
	Name                 string              `json:"name"`
	Model                string              `json:"model"`
	Connected            bool                `json:"connected"`
	State                string              `json:"state"`
	JobID                string              `json:"job_id,omitempty"`
	JobActive            bool                `json:"job_active"`
	JobTerminal          bool                `json:"job_terminal"`
	FileName             string              `json:"file_name,omitempty"`
	Progress             float64             `json:"progress"`
	ProgressKnown        bool                `json:"progress_known"`
	DurationSeconds      float64             `json:"duration_seconds"`
	DurationKnown        bool                `json:"duration_known"`
	FilamentUsedMM       *float64            `json:"filament_used_mm,omitempty"`
	Odometers            map[string]Odometer `json:"odometers,omitempty"`
	TimeRemainingSeconds float64             `json:"time_remaining_seconds"`
	JobStartedAt         time.Time           `json:"job_started_at,omitempty"`
	Printing             bool                `json:"printing"`
	Paused               bool                `json:"paused"`
	CanPause             bool                `json:"can_pause"`
	DepositionConfirmed  bool                `json:"deposition_confirmed"`
	DepositionReason     string              `json:"deposition_reason"`
}

// Odometer is actual tool extrusion since its reset, not the slicer's estimate.
// A positive change can include a purge or filament loading, not only the model.
type Odometer struct {
	ID         int64   `json:"id"`
	ExtrudedMM float64 `json:"extruded_mm"`
}

type fleet interface {
	List(context.Context, deviceapi.ListParams) (deviceapi.Page, error)
	Get(context.Context, deviceapi.GetParams) (deviceapi.Device, error)
	Jobs(context.Context, deviceapi.JobsParams) (deviceapi.Jobs, error)
	Command(context.Context, deviceapi.CommandParams) (deviceapi.Result, error)
}

type access interface {
	Check(context.Context, accessapi.CheckParams) (accessapi.Decision, error)
}

type Client struct {
	fleet  fleet
	access access
	conn   *wirerpc.Conn
}

func New(conn *wirerpc.Conn) *Client {
	return &Client{fleet: deviceapi.NewClient(conn), access: accessapi.NewClient(conn), conn: conn}
}

// List resolves visibility as principal on every host call. An empty principal is
// the module's own background identity, never a browser identity fallback.
func (c *Client) List(ctx context.Context, principal string) ([]Printer, error) {
	printers := make([]Printer, 0)
	for offset := 0; ; {
		page, err := c.fleet.List(ctx, deviceapi.ListParams{Principal: principal, Limit: deviceapi.MaxPageLimit, Offset: offset})
		if err != nil {
			return nil, err
		}
		for _, device := range page.Items {
			if device.Kind != "printer" || device.Retired {
				continue
			}
			jobs, err := c.fleet.Jobs(ctx, deviceapi.JobsParams{Principal: principal, ID: device.ID})
			if err != nil {
				return nil, err
			}
			printers = append(printers, normalize(device, jobs))
		}
		offset += len(page.Items)
		if len(page.Items) == 0 || offset >= page.Total {
			return printers, nil
		}
		if offset > 10000 {
			return nil, errors.New("printer listing exceeds the supported fleet size")
		}
	}
}

func (c *Client) Get(ctx context.Context, principal, id string) (Printer, error) {
	device, err := c.fleet.Get(ctx, deviceapi.GetParams{Principal: principal, ID: id})
	if err != nil {
		return Printer{}, err
	}
	if device.Kind != "printer" || device.Retired {
		return Printer{}, deviceapi.ErrNoDevice
	}
	jobs, err := c.fleet.Jobs(ctx, deviceapi.JobsParams{Principal: principal, ID: id})
	if err != nil {
		return Printer{}, err
	}
	return normalize(device, jobs), nil
}

// CanOperate checks the serving principal for UI changes, or the module's own
// identity when principal is empty for background commands. The manifest must
// declare operate in permissions or checks for access.check to answer it.
func (c *Client) CanOperate(ctx context.Context, principal, id string) (bool, error) {
	decision, err := c.access.Check(ctx, accessapi.CheckParams{Subject: principal, Action: "operate", ObjectType: "device", ObjectID: id})
	return decision.Allowed, err
}

// CanManage mirrors the host's permission for changing a sink module through
// its HTTP routes. The manifest must list operate_resource under checks.
func (c *Client) CanManage(ctx context.Context, principal string) (bool, error) {
	decision, err := c.access.Check(ctx, accessapi.CheckParams{Subject: principal, Action: "operate_resource", ObjectType: "resource_kind", ObjectID: "sink"})
	return decision.Allowed, err
}

// Pause checks the current job immediately before issuing exactly one command.
// Connect's command contract has no job-id fence: a firmware-side race remains
// between this read and the command, so failures are never automatically retried.
func (c *Client) Pause(ctx context.Context, printerID, jobID string) error {
	current, err := c.Get(ctx, "", printerID)
	if err != nil {
		return err
	}
	if jobID == "" || current.JobID != jobID {
		return ErrJobChanged
	}
	if !current.Printing {
		return ErrNotPrinting
	}
	if !current.CanPause {
		return ErrPauseUnsupported
	}
	result, err := c.fleet.Command(ctx, deviceapi.CommandParams{ID: printerID, Verb: pauseVerb})
	if err != nil {
		return err
	}
	if !result.OK {
		return fmt.Errorf("%w: %s", ErrPauseRefused, result.MachineReason)
	}
	return nil
}

// Subscribe leaves cursor persistence and resynchronization to the caller.
// onWindow must enqueue bounded work rather than run network inference inline.
func (c *Client) Subscribe(ctx context.Context, cursor string, onWindow func(busapi.Window) error) error {
	if c.conn == nil {
		return errors.New("Connect event session is unavailable")
	}
	return busapi.Subscribe(ctx, c.conn, busapi.SubscribeParams{Resume: cursor, Types: []string{
		"com.prusa.connect.device.job.started", "com.prusa.connect.device.job.settled",
		"com.prusa.connect.device.record.changed", "com.prusa.connect.device.presence.changed",
		"com.prusa.connect.device.telemetry.sampled", "com.prusa.connect.device.issues.changed",
		"com.prusa.connect.device.cameras.changed",
	}}, onWindow)
}

func normalize(device deviceapi.Device, jobs deviceapi.Jobs) Printer {
	p := Printer{ID: device.ID, Name: device.Name, Model: device.Model,
		Connected: device.Connected && !device.Retired, State: stateString(device.State, "/state"),
		CanPause:         slices.Contains(device.Capabilities, pauseVerb),
		DepositionReason: "Model deposition is not reported; automatic startup timing is an estimate.",
	}
	if p.Name == "" {
		p.Name = device.ID
	}
	if p.State == "" {
		p.State = "UNKNOWN"
	}
	if job := jobs.Current; job != nil && job.Ended == nil && job.Outcome == "" {
		p.JobID, p.FileName, p.JobStartedAt = job.ID, job.DisplayName, job.Started
		p.JobActive = job.ID != ""
		if p.FileName == "" {
			p.FileName = job.Path
		}
		p.Progress = finiteNonnegative(job.Progress)
		if value, known := optionalNumber(device.State, connectorapi.JobProgressPath); known {
			p.Progress, p.ProgressKnown = value, true
		}
		p.TimeRemainingSeconds = finiteNonnegative(float64(job.TimeRemaining))
		p.DurationSeconds, p.DurationKnown = optionalNumber(device.State, connectorapi.JobTimePrintingPath)
		if value, known := optionalNumber(device.State, connectorapi.JobFilamentUsedPath); known {
			p.FilamentUsedMM = &value
		}
		p.Odometers = toolOdometers(device.State)
		p.Printing = p.Connected && p.State == "PRINTING" && job.ID != ""
		p.Paused = p.Connected && p.State == "PAUSED" && job.ID != ""
	}
	p.JobTerminal = p.Connected && !p.JobActive && (p.State == "FINISHED" || p.State == "STOPPED" || p.State == "IDLE" || p.State == "READY")
	// The pinned beta.1 xbuddy driver maps PRINTING, progress, elapsed time and
	// Z height and (on firmware 8) actual tool extrusion, but no model-deposition
	// phase. Purge can advance extrusion too. Keep confirmation false; the
	// separate warmup policy can make the explicitly requested timing estimate.
	p.DepositionConfirmed = false
	if !p.Connected {
		p.DepositionReason = "Printer is disconnected."
	} else if p.JobID == "" {
		p.DepositionReason = "No active print."
	} else if !p.Printing {
		p.DepositionReason = "Printer is not actively printing."
	}
	return p
}

func stateString(state map[string]json.RawMessage, key string) string {
	var value string
	if json.Unmarshal(state[key], &value) != nil {
		return ""
	}
	return strings.ToUpper(value)
}

func optionalNumber(state map[string]json.RawMessage, key string) (float64, bool) {
	var value float64
	raw, exists := state[key]
	if !exists || string(raw) == "null" || json.Unmarshal(raw, &value) != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, false
	}
	return value, true
}

func toolOdometers(state map[string]json.RawMessage) map[string]Odometer {
	var readings map[string]Odometer
	for path := range state {
		id, member, valid := connectorapi.SplitToolPath(path)
		if !valid || member != connectorapi.OdometerE {
			continue
		}
		value, known := optionalNumber(state, path)
		var epoch int64
		epochRaw := state[connectorapi.ToolPath(id, connectorapi.OdometerID)]
		if !known || len(epochRaw) == 0 || string(epochRaw) == "null" || json.Unmarshal(epochRaw, &epoch) != nil || epoch < 0 {
			continue
		}
		if readings == nil {
			readings = make(map[string]Odometer)
		}
		readings[string(id)] = Odometer{ID: epoch, ExtrudedMM: value}
	}
	return readings
}

func finiteNonnegative(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0
	}
	return value
}
