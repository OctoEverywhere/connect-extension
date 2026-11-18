package warmup

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/octoeverywhere/connect-extension/internal/connect"
)

func printing() connect.Printer {
	return connect.Printer{ID: "p1", JobID: "job1", JobActive: true, Connected: true, Printing: true, State: "PRINTING", Progress: 1, ProgressKnown: true, DurationKnown: true, DurationSeconds: 180}
}

func TestPreparingAndPurgeCannotBypassStartupChecks(t *testing.T) {
	start := time.Unix(1000, 0)
	for _, state := range []string{"HEATING", "HOMING", "BUSY", "ATTENTION", "IDLE", "PAUSED"} {
		t.Run(state, func(t *testing.T) {
			var g Gate
			p := printing()
			p.Printing = false
			p.State = state
			p.Paused = state == "PAUSED"
			p.DurationSeconds = 900
			if ready, _ := g.Observe(p, start, 180); ready {
				t.Fatal("prepared state admitted")
			}
			if ready, _ := g.Observe(p, start.Add(10*time.Minute), 180); ready {
				t.Fatal("time admitted a prepared state")
			}
		})
	}
	var g Gate
	p := printing()
	p.Progress = 0
	p.DurationSeconds = 900
	p.Odometers = map[string]connect.Odometer{"0": {ID: 1, ExtrudedMM: 100}}
	if ready, _ := g.Observe(p, start, 180); ready {
		t.Fatal("purge at zero progress admitted")
	}
	p.Odometers["0"] = connect.Odometer{ID: 1, ExtrudedMM: 200}
	if ready, _ := g.Observe(p, start.Add(30*time.Second), 180); ready {
		t.Fatal("purge extrusion overrode zero progress")
	}
}

func TestDelayAndStabilityThenFallbackAttachMidPrint(t *testing.T) {
	start := time.Unix(1000, 0)
	var g Gate
	p := printing()
	p.Progress = 50
	p.DurationSeconds = 3600
	if ready, _ := g.Observe(p, start, 180); ready {
		t.Fatal("no stability window")
	}
	if ready, _ := g.Observe(p, start.Add(29*time.Second), 180); ready {
		t.Fatal("short stability window")
	}
	ready, reason := g.Observe(p, start.Add(30*time.Second), 180)
	if !ready || !strings.Contains(reason, "Estimated") || !strings.Contains(reason, "unavailable") {
		t.Fatalf("midprint fallback: %t %s", ready, reason)
	}
	g.Reset()
	p.DurationSeconds = 60
	g.Observe(p, start, 180)
	if ready, _ = g.Observe(p, start.Add(30*time.Second), 180); ready {
		t.Fatal("ignored startup delay")
	}
	p.DurationSeconds = 180
	if ready, _ = g.Observe(p, start.Add(60*time.Second), 180); !ready {
		t.Fatal("never admitted after delay")
	}
}

func TestExtrusionMustAdvanceAfterDelayAndCounterResetRebases(t *testing.T) {
	start := time.Unix(1000, 0)
	var g Gate
	p := printing()
	p.Odometers = map[string]connect.Odometer{"0": {ID: 1, ExtrudedMM: 1000}}
	g.Observe(p, start, 180)
	if ready, _ := g.Observe(p, start.Add(30*time.Second), 180); ready {
		t.Fatal("lifetime counter admitted without a delta")
	}
	p.Odometers["0"] = connect.Odometer{ID: 2, ExtrudedMM: 10}
	if ready, _ := g.Observe(p, start.Add(40*time.Second), 180); ready {
		t.Fatal("reset admitted")
	}
	if ready, _ := g.Observe(p, start.Add(50*time.Second), 180); ready {
		t.Fatal("stationary counter admitted")
	}
	p.Odometers["0"] = connect.Odometer{ID: 2, ExtrudedMM: 12}
	ready, reason := g.Observe(p, start.Add(time.Minute), 180)
	if !ready || !strings.Contains(reason, "measured extrusion") {
		t.Fatalf("extrusion delta: %t %s", ready, reason)
	}
}

func TestMissingDurationUsesOnlyObservedPrinting(t *testing.T) {
	start := time.Unix(1000, 0)
	var g Gate
	p := printing()
	p.DurationKnown = false
	p.DurationSeconds = 999999
	for i := 0; i < 6; i++ {
		if ready, _ := g.Observe(p, start.Add(time.Duration(i)*30*time.Second), 180); ready {
			t.Fatalf("admitted at %ds", i*30)
		}
	}
	if ready, _ := g.Observe(p, start.Add(180*time.Second), 180); !ready {
		t.Fatal("missing duration blocks forever")
	}
	g.Reset()
	g.Observe(p, start, 180)
	if ready, _ := g.Observe(p, start.Add(time.Hour), 180); ready {
		t.Fatal("unobserved gap counted")
	}
	if g.ObservedPrintingSeconds != 0 {
		t.Fatal("gap added printing time")
	}
}

func TestPauseJobReplacementAndPersistence(t *testing.T) {
	start := time.Unix(1000, 0)
	var g Gate
	p := printing()
	g.Observe(p, start, 180)
	g.Observe(p, start.Add(30*time.Second), 180)
	encoded, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	var restored Gate
	if err = json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	p.Printing = false
	p.Paused = true
	if ready, _ := restored.Observe(p, start.Add(40*time.Second), 180); ready {
		t.Fatal("paused admitted")
	}
	p.Printing = true
	p.Paused = false
	if ready, _ := restored.Observe(p, start.Add(50*time.Second), 180); ready {
		t.Fatal("resume ignored stability")
	}
	if ready, _ := restored.Observe(p, start.Add(80*time.Second), 180); !ready {
		t.Fatal("resume lost session")
	}
	p.JobID = "job2"
	p.Progress = 0
	p.DurationSeconds = 0
	if ready, _ := restored.Observe(p, start.Add(90*time.Second), 180); ready || restored.Ready {
		t.Fatal("new job retained ready flag")
	}
	p.JobID = ""
	p.JobTerminal = true
	restored.Observe(p, start.Add(100*time.Second), 180)
	if restored.JobID != "" {
		t.Fatal("terminal job retained")
	}
}
