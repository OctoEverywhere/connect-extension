# Changelog

## 0.1.0 - 2026-11-17

### Added

- OctoEverywhere page with per-printer Gadget monitoring, selected cameras, inspection status, and event history.
- Gadget API-key validation and a scoped Connect credential for camera snapshots.
- Automatic startup timing with a configurable warmup delay and extrusion telemetry when available.
- Optional automatic pause using Gadget's pause recommendation and the current printer job.
- Persistent print contexts, retry pacing, restart recovery, and visible account/camera failures.
- Native builds for Linux amd64/arm64, macOS arm64, and Windows amd64.
- PowerShell build/check/release commands alongside the Makefile.

This beta's scope is AI failure detection. Remote access and broader OctoEverywhere notifications are planned separately.
