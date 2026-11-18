// Package detection owns print monitoring independently of the extension UI.
package detection

import (
	"context"
	"time"

	"github.com/octoeverywhere/connect-extension/internal/connect"
	"github.com/octoeverywhere/connect-extension/internal/gadget"
	"github.com/octoeverywhere/connect-extension/internal/warmup"
)

type Fleet interface {
	List(context.Context, string) ([]connect.Printer, error)
	Get(context.Context, string, string) (connect.Printer, error)
	CanOperate(context.Context, string, string) (bool, error)
	CanManage(context.Context, string) (bool, error)
	Pause(context.Context, string, string) error
}

type CameraSource interface {
	Cameras(context.Context, string) ([]connect.Camera, error)
	Snapshot(context.Context, string, time.Time) (connect.Snapshot, error)
}

type CameraFactory func(string, string) (CameraSource, error)

// Credentials never appear in a status response or in logs.
type Credentials struct {
	Schema         int    `json:"schema"`
	APIKey         string `json:"api_key"`
	ConnectURL     string `json:"connect_url"`
	ConnectToken   string `json:"connect_token"`
	AccountProblem string `json:"account_problem,omitempty"`
}

type PrinterSettings struct {
	Enabled           bool   `json:"enabled"`
	CameraID          string `json:"camera_id"`
	AutoPause         bool   `json:"auto_pause"`
	WarningConfidence int    `json:"warning_confidence"`
	PauseConfidence   int    `json:"pause_confidence"`
	WarmupSeconds     int    `json:"warmup_seconds"`
}

type Session struct {
	JobID          string         `json:"job_id"`
	Warmup         warmup.Gate    `json:"warmup"`
	ArmedAt        time.Time      `json:"armed_at"`
	Context        gadget.Context `json:"context"`
	Next           time.Time      `json:"next"`
	Interval       time.Duration  `json:"interval"`
	LastFrame      string         `json:"last_frame"`
	LastFrameAt    time.Time      `json:"last_frame_at"`
	Result         *gadget.Result `json:"result,omitempty"`
	ResultAt       time.Time      `json:"result_at"`
	WarningSent    bool           `json:"warning_sent"`
	PauseRequested bool           `json:"pause_requested"`
	PauseConfirmed bool           `json:"pause_confirmed"`
	Blocked        string         `json:"blocked"`
	BlockedScope   gadget.Scope   `json:"blocked_scope,omitempty"`
}

type Record struct {
	Schema     int             `json:"schema"`
	PrinterID  string          `json:"printer_id"`
	Settings   PrinterSettings `json:"settings"`
	Session    Session         `json:"session"`
	History    []HistoryEntry  `json:"history"`
	Generation uint64          `json:"generation"`
}

type HistoryEntry struct {
	At          time.Time `json:"at"`
	PrinterID   string    `json:"printer_id"`
	PrinterName string    `json:"printer_name"`
	JobName     string    `json:"job_name"`
	Kind        string    `json:"kind"`
	Message     string    `json:"message"`
	Quality     int       `json:"quality,omitempty"`
}

type PrinterStatus struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	State    string  `json:"state"`
	Detail   string  `json:"detail"`
	JobID    string  `json:"job_id"`
	JobName  string  `json:"job_name"`
	Progress float64 `json:"progress"`
	PrinterSettings
	CanManage        bool           `json:"can_manage"`
	Cameras          []CameraOption `json:"cameras"`
	LastInspectionAt *time.Time     `json:"last_inspection_at,omitempty"`
	Quality          int            `json:"quality,omitempty"`
	Warning          bool           `json:"warning"`
	PauseSuggested   bool           `json:"pause_suggested"`
}

type Status struct {
	Configured       bool            `json:"configured"`
	CameraConfigured bool            `json:"camera_configured"`
	ConnectURL       string          `json:"connect_url"`
	CanManage        bool            `json:"can_manage"`
	Error            string          `json:"error,omitempty"`
	Printers         []PrinterStatus `json:"printers"`
	History          []HistoryEntry  `json:"history"`
}

type CameraOption struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func defaults() PrinterSettings {
	return PrinterSettings{WarningConfidence: 3, PauseConfidence: 3, WarmupSeconds: 180}
}
