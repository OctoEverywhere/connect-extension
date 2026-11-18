# OctoEverywhere for Prusa Connect Local

Gadget AI print failure detection inside Prusa Connect Local. Select a camera for each printer, enable monitoring, and optionally let Gadget pause a print when it recommends intervention.

This is a standalone native Go extension with a Svelte page. The first beta uses the [Gadget public API](https://docs.octoeverywhere.com/ai-failure-detection-apis/overview/); remote access and the broader OctoEverywhere notification service are future work. It requires no fork of the OctoEverywhere plugin and no Python runtime.

Module: `github.com/octoeverywhere/connect-extension`
Derived namespace: `com.github.aux.octoeverywhere.connect-extension`
Prusa Connect: `>=0.1.0-beta.1`

## Setup

1. Install the extension into Prusa Connect Local and approve its permissions and private data directory. `observe` gives the module fleet visibility. `operate` declares the maximum action it may take; each printer needs a separate operator grant before automatic pause is available. Monitoring is disabled until you enable it.
2. Set the extension's `connect_url` to the HTTP origin of the Connect server reachable **from the extension process**. The default is `http://127.0.0.1:8080`. In a container, use the server address in that container's network. Supported ports are 80, 443, 8080, 8443, and 18480.
3. In Connect, open **Settings → API credentials → New credential**. Create a **read** credential for a person who can view the printers and cameras you want to monitor. Save that token on the OctoEverywhere page. The page displays the origin configured by the host; it cannot redirect the saved token to another server.
4. Open [OctoEverywhere.com/gadgetapi](https://octoeverywhere.com/gadgetapi), sign in, copy your Gadget API key, and save it on the page. Saving tests the key by creating a context; it does not upload a camera image.
5. Select a camera attached to each printer. Use **Test camera** to check its fresh JPEG snapshot, then enable monitoring. Configure warning/pause sensitivity and the startup delay. To use automatic pause, complete the printer grant below before enabling it.

The camera credential only reads through Connect. Printer state and pause commands use the extension's host-granted identity. A viewer sees only printers visible to their Connect identity; changing settings also requires the appropriate management/operate permission.

The extension stores credentials and monitoring state as bounded JSON files under `records-v1/` in the data directory granted by the host. It needs no Connect records/database backend, so persistence works on a stock first-run installation. Files are replaced after writing and flushing a private temporary file. Unix directories/files use modes 0700/0600; Windows uses the granted directory's inherited ACL. Credentials and context URLs are omitted from frontend status responses and logs. Data-directory backups contain those saved secrets; the host's `.owner` marker must remain intact.

### Permit automatic pause for a printer

An administrator must grant the module's principal **Operator** on each printer that should allow automatic pause. The installer's `-approve` flag cannot grant organization-wide operator access to a module. The camera read token does not provide this authority.

The current Connect extension Permissions tab displays requests; it does not edit per-printer module grants. Use this authenticated administrator API request:

```http
POST /api/v1/grants/device/{printer_id}
Content-Type: application/json

{"relation":"operator","subject":"sink:octoeverywhere"}
```

Use the actual sink instance name if it was renamed during installation. The role permits printer operation generally; this extension only issues pause commands. Automatic pause must still be explicitly enabled for the printer, and the extension checks its own current permission before acting. Revoking the grant stops automatic pause.

## Monitoring behavior

- One Gadget context follows one printer job across snapshots, pauses, temporary disconnects, and extension restarts.
- Warning/pause confidence changes apply to the next print. The current print keeps the confidence settings that created its Gadget context.
- Inspections run automatically after startup preparation. The default warmup delay is three minutes (180 seconds), configurable from 60 to 1,800 seconds. The gate also checks stable printing and progress; when firmware 8 supplies actual tool extrusion odometers, it requires further extrusion after the startup baseline.
- On older firmware, progress and elapsed time provide an estimate of when printing has begun. Even extrusion can include purge, so this is a startup heuristic, not proof that plastic is being deposited on the model. Increase the delay for machines with long homing, probing, or purge routines.
- Paused, offline, disabled, and finished printers do not send new inspections. Unavailable or stale camera frames produce a visible monitoring status rather than a stale healthy result.
- Snapshot uploads are sequential per printer. Scheduling follows Gadget's latest interval and minimum. Retry delays and account limits are respected.
- `PrintQuality` is displayed as 1–10, with 10 best. Warning and pause decisions use the API's `WarningSuggested` and `PauseSuggested` fields, not a local score threshold.
- Automatic pause is opt-in per printer. Before issuing a command, the extension checks the current job and printer state. It records requested/confirmed action state to avoid repeated pause commands. A pause command failure is shown for the operator; it is not blindly replayed.
- Detection and action history stays within the extension. It is not a separate email, push, or messaging integration.

The public API's free allowance and optional billing apply across all tools/printers sharing that Gadget account. The account page controls paid usage; the extension does not enable billing. Exhausted allowance, invalid credentials, and public-IP account conflicts stop inspections and produce an actionable error. See [Gadget account and usage details](https://docs.octoeverywhere.com/ai-failure-detection-apis/overview/).

## Configuration and network access

| Option | Type | Default | Purpose |
| --- | --- | --- | --- |
| `connect_url` | URL | `http://127.0.0.1:8080` | Connect origin used for authenticated camera reads. |

The install manifest declares both outbound connections:

| Destination | Ports | Data sent |
| --- | --- | --- |
| `*.octoeverywhere.com` | 443 | JPEG snapshots, confidence settings, private context IDs; the Gadget key is sent when creating a context. |
| Host in `connect_url` | 80, 443, 8080, 8443, 18480 | Camera requests and the saved Connect read token. |

The process requests a 128 MiB memory budget and a private data directory. It opens no listening port and requests no database. Configuration fails with an actionable error if the directory grant is missing or unwritable. The UI sends settings through the host's authenticated module routes.

## Build and validate

Prerequisites: Go matching `go.mod` (currently 1.26.11), Node 22.12 or newer, pnpm 11, and Git.

Until Prusa publishes the SDK/packages independently, builds use the Prusa Connect checkout pinned to `v0.1.0-beta.1` in `.core/`. The Go module replacement and npm workspace overrides point there. To update it, deliberately replace that checkout and update the core version in the build/release configuration.

On macOS/Linux:

```sh
make client    # regenerate and build the typed API client after OpenAPI changes
make check     # manifest/OpenAPI validation, Go vet/tests, generated client, UI checks/build
make release   # release/ with UI, manifest, documentation, and all four native binaries
make install   # install a local trial into bin/try; does not publish
```

On Windows with PowerShell 7:

```powershell
./scripts/build.ps1 Client
./scripts/build.ps1 Check
./scripts/build.ps1 Release
```

The PowerShell script also accepts `Core` and `UIBuild`. It restores temporary Go cross-compilation environment variables and only replaces this repository's `release/` directory.

After building, install into the same Connect configuration directory your server uses:

```sh
prusa-connect module add github.com/octoeverywhere/connect-extension@0.1.0 -from ./release -approve
```

This approves the private data directory and fleet observation. The installer reports that `operate` needs a per-printer grant; follow the automatic-pause setup above if you want that feature.

Release platforms: Linux amd64/arm64, macOS arm64, and Windows amd64. Builds disable CGO and use `-trimpath`.

Generated client consistency is checked against the current OpenAPI document rather than the last Git commit, so checks work while preparing a new change. Generated files in `client/src/generated/` and `client/openapi.json` should be committed together.

## Release validation

To test without a physical printer, follow [local testing with Prusa's simulated fleet](docs/local-testing.md). The PowerShell runner builds and installs this extension into an isolated local Connect demo.

Automated tests use local HTTP servers and fake host adapters. They do not require a Gadget key or billable image requests. Before making this beta public, complete the live acceptance checks in [docs/release-validation.md](docs/release-validation.md), including an actual camera/print lifecycle, pause confirmation, permission boundaries, and host restart.

See [the completed build and host checks](docs/verified-build.md) for the tested environment, results, and remaining validation.

The GitHub workflows retain Prusa's marketplace build/sign/attestation process. A GitHub release tagged `v<manifest version>` triggers the publish workflow; a local `Release` build does not publish anything. Before publishing, configure the public repository, marketplace author identity/trusted publisher, release environment, and `PRUSA_CONNECT_EXTENSION_SIGNING_KEY` as described in [Prusa's extension docs](https://plugin-marketplace.prusa3d.com/docs/connect).

## Code layout

| Path | Purpose |
| --- | --- |
| `main.go`, `faults.go` | Host protocol, lifecycle, permission-aware HTTP routes and registered errors |
| `internal/connect/` | Printer state, camera snapshots and pause commands |
| `internal/warmup/` | Automatic startup monitoring gate |
| `internal/detection/` | Per-printer sessions, scheduling and action state |
| `internal/gadget/` | Public Gadget API client |
| `internal/storage/` | Private atomic JSON records in the host-granted data directory |
| `ui/` | OctoEverywhere page |
| `openapi.json`, `client/` | Route contract and generated TypeScript client |
| `module.yaml` | Identity, permissions, settings, egress and supported platforms |

Licensed under MIT. The initial extension scaffold is from Prusa Research; its copyright notice is retained.
