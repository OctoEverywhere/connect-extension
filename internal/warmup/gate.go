// Package warmup estimates when a print has passed startup without requiring
// manual confirmation. Connect does not expose a model-deposition phase: this
// policy deliberately waits, and cannot guarantee that every custom start
// G-code has finished purging. Its reasons describe an estimate, not proof.
package warmup

import (
	"fmt"
	"maps"
	"time"

	"github.com/octoeverywhere/connect-extension/internal/connect"
)

const DefaultDelaySeconds = 180
const StablePrintingSeconds = 30

// Gate belongs to one monitored printer and can be persisted with its session.
// Only Observe changes it; callers serialize access with their session lock.
type Gate struct {
	JobID                   string                      `json:"job_id,omitempty"`
	StableSince             time.Time                   `json:"stable_since,omitempty"`
	LastObservedAt          time.Time                   `json:"last_observed_at,omitempty"`
	LastPrinting            bool                        `json:"last_printing"`
	ObservedPrintingSeconds float64                     `json:"observed_printing_seconds"`
	Odometers               map[string]connect.Odometer `json:"odometers,omitempty"`
	FilamentUsedMM          *float64                    `json:"filament_used_mm,omitempty"`
	Ready                   bool                        `json:"ready"`
	Evidence                string                      `json:"evidence,omitempty"`
}

func (g *Gate) Reset() { *g = Gate{} }

// Observe combines the best available runtime signals. Progress must be above
// zero; PRINTING alone also covers startup. The delay is measured from reported
// print duration, or accumulated observed printing time when duration is absent.
// Firmware with odometers must additionally extrude after the delay/stability
// checks; older firmware uses the less certain progress-and-time fallback.
func (g *Gate) Observe(p connect.Printer, now time.Time, delaySeconds int) (bool, string) {
	if p.JobTerminal {
		g.Reset()
		return false, "No active print."
	}
	if p.JobID != "" && g.JobID != p.JobID {
		g.Reset()
		g.JobID = p.JobID
	}
	if delaySeconds <= 0 {
		delaySeconds = DefaultDelaySeconds
	}
	if !p.Connected || !p.Printing || p.JobID == "" {
		g.StableSince = time.Time{}
		g.LastObservedAt = now
		g.LastPrinting = false
		g.Odometers = nil
		g.FilamentUsedMM = nil
		if !p.Connected {
			return false, "Waiting for the printer to reconnect."
		}
		if p.Paused {
			return false, "Monitoring is suspended while the print is paused."
		}
		return false, "Waiting for the printer to report an active print."
	}
	if !g.LastObservedAt.IsZero() && g.LastPrinting {
		gap := now.Sub(g.LastObservedAt)
		if gap < 0 || gap > time.Minute {
			// A long gap is not evidence that the printer kept printing. On
			// restart/reconnect, establish a fresh stability window.
			g.StableSince = time.Time{}
		} else {
			g.ObservedPrintingSeconds += gap.Seconds()
		}
	}
	g.LastObservedAt, g.LastPrinting = now, true
	if g.StableSince.IsZero() {
		g.StableSince = now
	}
	if p.Progress <= 0 {
		g.Odometers = nil
		g.FilamentUsedMM = nil
		return false, "Waiting for print progress beyond zero before the automatic startup delay."
	}
	if remaining := StablePrintingSeconds - now.Sub(g.StableSince).Seconds(); remaining > 0 {
		return false, fmt.Sprintf("Waiting for stable printing (%ds remaining).", int(remaining)+1)
	}
	elapsed, source := p.DurationSeconds, "reported print time"
	if !p.DurationKnown {
		elapsed, source = g.ObservedPrintingSeconds, "observed printing time"
	}
	if elapsed < float64(delaySeconds) {
		g.Odometers = nil
		g.FilamentUsedMM = nil
		return false, fmt.Sprintf("Automatic startup delay: %ds of %ds using %s.", int(elapsed), delaySeconds, source)
	}
	if g.Ready {
		return true, g.Evidence
	}
	if len(p.Odometers) > 0 {
		before := g.Odometers
		g.Odometers = maps.Clone(p.Odometers)
		if len(before) == 0 {
			return false, "Startup delay passed; waiting for further measured extrusion (estimated print start)."
		}
		advanced := false
		for tool, current := range p.Odometers {
			previous, exists := before[tool]
			if !exists {
				continue
			}
			if current.ID != previous.ID || current.ExtrudedMM < previous.ExtrudedMM {
				return false, "Extrusion counter reset; waiting for another measurement."
			}
			if current.ExtrudedMM > previous.ExtrudedMM {
				advanced = true
			}
		}
		if !advanced {
			return false, "Waiting for further measured extrusion after startup (estimated print start)."
		}
		g.Evidence = "Estimated print start: stable printing, positive progress, startup delay, and measured extrusion."
	} else if p.FilamentUsedMM != nil {
		previous := g.FilamentUsedMM
		current := *p.FilamentUsedMM
		g.FilamentUsedMM = &current
		if previous == nil {
			return false, "Startup delay passed; waiting for further reported filament consumption (estimated print start)."
		}
		if current < *previous {
			return false, "Filament consumption counter reset; waiting for another measurement."
		}
		if current == *previous {
			return false, "Waiting for further reported filament consumption after startup (estimated print start)."
		}
		g.Evidence = "Estimated print start: stable printing, positive progress, startup delay, and increasing reported filament consumption."
	} else {
		// Older firmware exposes no extrusion counter. Requiring it would
		// prevent automatic monitoring permanently, so use the requested
		// conservative fallback and disclose its weaker evidence.
		g.Evidence = "Estimated print start: stable printing, positive progress, and startup delay; extrusion telemetry is unavailable."
	}
	g.Ready = true
	return true, g.Evidence
}
