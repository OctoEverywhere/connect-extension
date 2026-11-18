# Testing locally before using a real printer

Start with the real Connect host and its simulated fleet. You do not need your MK4, Docker, a camera, or a Gadget account to test installation and run the automated detection tests.

## 1. Start the local fleet on Windows

From this extension repository, in PowerShell 7:

```powershell
pwsh -NoProfile -File scripts/local-demo.ps1 Start
```

The first run builds the host UI, Connect's demo/installer, and the extension. This needs Git, Go 1.26, Node.js 22.12 or newer, pnpm, and internet access for dependencies. Later starts reuse the build. The runner prints the browser address and the local `demo` login password.

Open **http://127.0.0.1:18480**. Choose **OctoEverywhere** in the sidebar. The runner prepares the local camera credential and camera selections; Gadget remains unconfigured and printer monitoring stays off until you explicitly enable it.

On later starts, your saved Gadget key and monitoring choices are preserved. If you have entered a real key and enabled a printer, restarting can resume real API requests.

The fleet contains three simulated Prusa printers plus a simulated Klipper printer. One Prusa starts printing; another demonstrates a filament-runout dialog. Use the printing CoreOne for the first checks. These are software simulators speaking to the actual host, not physical printers. [Prusa's demo documentation](https://raw.githubusercontent.com/prusa3d/prusa-connect/v0.1.0-beta.1/docs/src/content/docs/getting-started.md)

Useful commands:

```powershell
# Check whether this runner's host is running.
pwsh -NoProfile -File scripts/local-demo.ps1 Status

# Stop the local host and its simulated printers.
pwsh -NoProfile -File scripts/local-demo.ps1 Stop

# After changing source, rebuild and restart the local test environment.
pwsh -NoProfile -File scripts/local-demo.ps1 Start -Rebuild
```

Everything belongs to `bin/local-demo/`, apart from shared build outputs in `.core/` and `release/`. The host listens only on this PC's loopback address. It does not reconfigure a real printer or publish the extension. Keep this demo separate from a server intended to control hardware.

Stop the demo before renaming or moving this repository. Start it again from the new location afterward.

## 2. Test the detection logic without a Gadget key

```powershell
go test -v ./internal/warmup ./internal/detection ./internal/gadget

# All backend, manifest, generated-client, and frontend checks:
pwsh -NoProfile -File scripts/build.ps1 Check
```

The detection tests use controlled mock printers, JPEG images, and Gadget responses. They cover:

- Heating/startup delay, zero progress, extrusion counters, and the older-firmware fallback.
- Pausing, disconnecting, disabling monitoring, and replacing the active job.
- Warning and pause recommendations, optional automatic pause, permission revocation, and duplicate-command prevention.
- Context reuse after restart, quota exhaustion, account-key replacement, API pacing, and stale responses.
- Invalid images, camera ownership, secret omission, and storage failures.

No production Gadget API call is made by these tests. This is the repeatable way to verify failure recommendations; a simulated printer does not need to produce a real failed print.

## 3. Explore the running host

| Check | Expected result |
| --- | --- |
| Open Printers and the printing CoreOne | Progress changes; the simulated camera updates. |
| Open OctoEverywhere | Account setup and the real host's printer list load. No key is prefilled. |
| Open a printer's Settings in OctoEverywhere and choose Test camera | A fresh JPEG succeeds without calling Gadget. |
| Restart the demo | Extension settings remain in its own granted data directory. |
| Pause/resume the simulated print in Connect | The host's reported printer state changes. |
| Add a real Gadget key deliberately | The normal production key-validation call runs. Enabling monitoring then permits real API requests. |

To test **Automatic pausing**, first grant the extension Operator access to the simulated printers:

```powershell
pwsh -NoProfile -File scripts/local-demo.ps1 GrantPause
```

Then enable **Automatic pausing** in the desired printer's OctoEverywhere settings and save. If a save was already rejected with an Operator-access error, save those settings again after running the command.

The command checks that this checkout's demo is running and grants access only to connected printers with the simulator's known serial numbers. It does not restart the host, edit your Gadget key, or change monitoring or automatic-pausing settings. Running it again is safe. To grant access to only one simulated printer, add `-PrinterId '<printer ID from the Connect printer URL>'`. For a hardware server, follow the [per-printer grant instructions](../README.md#permit-automatic-pause-for-a-printer).

**The demo has a simulated printer/camera, but it does not substitute a fake Gadget service inside the running extension.** Do not enter a real key merely to see simulated failures. The demo cameras generate drawings, labels, and progress bars, not print photographs; sending those to Gadget exercises connectivity and consumes account usage, but cannot establish detection accuracy. Use the no-key tests above for deterministic AI decisions. [Prusa's simulator documentation](https://raw.githubusercontent.com/prusa3d/prusa-connect/v0.1.0-beta.1/docs/src/content/docs/emulator.md)

## 4. Move to the MK4 when the local tests pass

The stock MK4 is supported with firmware **6.4.0 or later**; no MK4S upgrade is required. As checked on November 17, 2026, **8.0.2** is the current stable release supporting MK4. [Supported printers](https://raw.githubusercontent.com/prusa3d/prusa-connect/v0.1.0-beta.1/README.md), [official firmware release](https://github.com/prusa3d/Prusa-Firmware-Buddy/releases/tag/v8.0.2)

Use a **normal Connect server accessible on your home LAN**, separate from this loopback-only demo. Its Add a printer flow supplies `prusa_printer_settings.ini`. Load that file from USB using the MK4's **Settings → Network → Prusa Connect → Load Settings**, then register/approve the printer with that local server. The address must be the server PC's LAN address, not `localhost`.

A printer connects to only **one Connect server at a time**. Save its existing cloud `prusa_printer_settings.ini` before switching; that backup lets you restore its cloud registration afterward. [Prusa's configuration and restoration instructions](https://raw.githubusercontent.com/prusa3d/prusa-connect/v0.1.0-beta.1/docs/src/content/docs/printer-configuration.md)

Add a camera with a clear view of the bed and a fresh JPEG snapshot through Connect. For an RTSP source, configure a separate snapshot URL as required by the driver. [Camera source documentation](https://raw.githubusercontent.com/prusa3d/prusa-connect/v0.1.0-beta.1/docs/src/content/docs/extensions.md#camera-sources)

Start with one supervised print and automatic pause off. Check when monitoring begins against actual preparation/first-layer behavior, then check fresh inspections. Only enable automatic pause after granting the extension per-printer Operator access as described in the [setup guide](../README.md#permit-automatic-pause-for-a-printer). Work through [live release acceptance](release-validation.md) before publishing.
