package connect

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
)

func activeDevice() deviceapi.Device {
	return deviceapi.Device{Kind: "printer", ID: "p1", Connected: true, Capabilities: []string{pauseVerb}, State: map[string]json.RawMessage{
		"/state": json.RawMessage(`"PRINTING"`), "/job/time_printing": json.RawMessage(`300`),
		"/axis/z": json.RawMessage(`0.2`), "/temperature/nozzle": json.RawMessage(`220`),
	}}
}

func activeJobs() deviceapi.Jobs {
	return deviceapi.Jobs{Current: &deviceapi.Job{ID: "job-1", State: "PRINTING", Progress: 10}}
}

func TestDepositionNeverInferredFromStartupTelemetry(t *testing.T) {
	for _, state := range []string{"PRINTING", "BUSY", "PAUSED", "ATTENTION", "HEATING", "HOMING", "PURGING", "IDLE"} {
		t.Run(state, func(t *testing.T) {
			device := activeDevice()
			device.State["/state"], _ = json.Marshal(state)
			// Unknown driver paths are not a supported contract and cannot
			// silently turn a safety gate into a heuristic.
			device.State["/job/layer"] = json.RawMessage(`0`)
			device.State["/job/extruding"] = json.RawMessage(`true`)
			printer := normalize(device, activeJobs())
			if printer.DepositionConfirmed || printer.DepositionReason == "" {
				t.Fatal("deposition must remain unconfirmed with an explanation")
			}
			if printer.Printing != (state == "PRINTING") {
				t.Fatalf("printing=%v in %s", printer.Printing, state)
			}
		})
	}
}

func TestDisconnectAndPauseKeepJobIdentity(t *testing.T) {
	device := activeDevice()
	device.Connected = false
	p := normalize(device, activeJobs())
	if !p.JobActive || p.JobTerminal || p.Printing || p.JobID != "job-1" {
		t.Fatalf("disconnected job lost: %+v", p)
	}
	device.Connected = true
	device.State["/state"] = json.RawMessage(`"PAUSED"`)
	p = normalize(device, activeJobs())
	if !p.Paused || !p.JobActive || p.JobTerminal {
		t.Fatalf("paused job lost: %+v", p)
	}
	device.State["/state"] = json.RawMessage(`"FINISHED"`)
	p = normalize(device, deviceapi.Jobs{})
	if !p.JobTerminal || p.JobActive {
		t.Fatalf("finished state not terminal: %+v", p)
	}
}

func TestRealExtrusionTelemetryPreservesEpochAndUnknown(t *testing.T) {
	device := activeDevice()
	device.State["/tools/0.1/odometer_id"] = json.RawMessage(`7`)
	device.State["/tools/0.1/odometer_e"] = json.RawMessage(`12.5`)
	device.State["/tools/1/odometer_e"] = json.RawMessage(`100`)
	device.State["/job/filament_used_mm"] = json.RawMessage(`0`)
	device.State["/job/progress"] = json.RawMessage(`2`)
	p := normalize(device, activeJobs())
	if len(p.Odometers) != 1 || p.Odometers["0.1"].ID != 7 || p.Odometers["0.1"].ExtrudedMM != 12.5 {
		t.Fatalf("bad odometers %+v", p.Odometers)
	}
	if p.FilamentUsedMM == nil || *p.FilamentUsedMM != 0 || !p.DurationKnown || !p.ProgressKnown || p.Progress != 2 {
		t.Fatalf("real zero became unknown: %+v", p)
	}
	device.State["/job/filament_used_mm"] = json.RawMessage(`null`)
	device.State["/tools/0.1/odometer_id"] = json.RawMessage(`null`)
	device.State["/job/time_printing"] = json.RawMessage(`null`)
	p = normalize(device, activeJobs())
	if p.FilamentUsedMM != nil || len(p.Odometers) != 0 || p.DurationKnown {
		t.Fatalf("null became known: %+v", p)
	}
}

type fakeFleet struct {
	device     deviceapi.Device
	jobs       deviceapi.Jobs
	result     deviceapi.Result
	commands   []deviceapi.CommandParams
	principals []string
}

func (f *fakeFleet) List(_ context.Context, p deviceapi.ListParams) (deviceapi.Page, error) {
	f.principals = append(f.principals, p.Principal)
	return deviceapi.Page{Items: []deviceapi.Device{f.device}, Total: 1}, nil
}
func (f *fakeFleet) Get(_ context.Context, p deviceapi.GetParams) (deviceapi.Device, error) {
	f.principals = append(f.principals, p.Principal)
	return f.device, nil
}
func (f *fakeFleet) Jobs(_ context.Context, p deviceapi.JobsParams) (deviceapi.Jobs, error) {
	f.principals = append(f.principals, p.Principal)
	return f.jobs, nil
}
func (f *fakeFleet) Command(_ context.Context, p deviceapi.CommandParams) (deviceapi.Result, error) {
	f.commands = append(f.commands, p)
	return f.result, nil
}

func TestPauseChecksJobAndDoesNotRetry(t *testing.T) {
	f := &fakeFleet{device: activeDevice(), jobs: activeJobs(), result: deviceapi.Result{OK: false, MachineReason: "busy"}}
	c := &Client{fleet: f}
	if err := c.Pause(context.Background(), "p1", "old-job"); !errors.Is(err, ErrJobChanged) {
		t.Fatalf("job mismatch: %v", err)
	}
	if len(f.commands) != 0 {
		t.Fatal("sent command for old job")
	}
	if err := c.Pause(context.Background(), "p1", "job-1"); !errors.Is(err, ErrPauseRefused) {
		t.Fatalf("refusal: %v", err)
	}
	if len(f.commands) != 1 || f.commands[0].Verb != pauseVerb || f.commands[0].Principal != "" {
		t.Fatalf("unexpected commands: %+v", f.commands)
	}
}

type fakeAccess struct{ params accessapi.CheckParams }

func (f *fakeAccess) Check(_ context.Context, p accessapi.CheckParams) (accessapi.Decision, error) {
	f.params = p
	return accessapi.Decision{Allowed: true}, nil
}

func TestPrincipalForwardedForEveryReadAndPermission(t *testing.T) {
	f := &fakeFleet{device: activeDevice(), jobs: activeJobs()}
	a := &fakeAccess{}
	c := &Client{fleet: f, access: a}
	if _, err := c.List(context.Background(), "user:viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "user:viewer", "p1"); err != nil {
		t.Fatal(err)
	}
	for _, principal := range f.principals {
		if principal != "user:viewer" {
			t.Fatalf("principal dropped: %q", principal)
		}
	}
	if _, err := c.CanOperate(context.Background(), "user:viewer", "p1"); err != nil {
		t.Fatal(err)
	}
	if a.params.Subject != "user:viewer" || a.params.ObjectID != "p1" || a.params.Action != "operate" {
		t.Fatalf("bad access check: %+v", a.params)
	}
}
