# Validation performed for 0.1.0

Validated on 2026-11-17. This records completed local checks; the physical-printer and production-account acceptance work remains in [release-validation.md](release-validation.md).

## Automated checks

- Go tests and `go vet` pass for the application, Connect adapter, Gadget client, startup gate, detection lifecycle, and private file store.
- The pinned Prusa SDK parses the manifest and OpenAPI fragment. Manifest, API, generated TypeScript client, changelog, and wire-protocol versions agree.
- TypeScript compilation, ESLint, Prettier, and Svelte checks pass with no Svelte warnings. The production UI bundle builds.
- Native binaries build for Linux amd64/arm64, macOS arm64, and Windows amd64. Only Windows was executed locally.
- The Go race detector was not run: this environment has CGO disabled and no GCC toolchain.

## Real Connect host integration

The test host was built from Prusa Connect `v0.1.0-beta.1`, commit `1c793d43533d974045b77234e2b21381e12cac5f`. It used Connect's simulator for four printers and its simulated camera sources, on Windows amd64.

- Installed the packaged extension with the actual module installer. Handshake, configuration, page registration, authenticated status, and startup succeed.
- Discovered only each printer's attached cameras through a read-scoped Connect token. Saved the camera credential and printer selection, then fetched and validated a fresh JPEG through the extension's camera-test endpoint.
- Unauthenticated status requests return 401. A read-scoped credential can read status but receives 403 when attempting a settings write.
- Enabling automatic pause is rejected without the module's per-printer Operator grant, and accepted after an administrator creates that grant. No physical pause command was exercised.
- Restarted the host with its shared record backend disabled. The extension becomes ready and retains its private credentials and selected camera without that backend.
- The running Windows process used approximately 50 MiB during the idle/setup test, below its declared 128 MiB budget. This is not a maximum-load measurement.

## Interface verification

Inspected the bundled page inside Connect at desktop 1440 x 1100 and mobile 390 x 844. No JavaScript page errors or horizontal overflow were observed. Setup used actual host status; warning, warmup, paused, history, and expanded settings views used browser-only status fixtures. Those fixtures are not shipped in the extension and are not evidence of live Gadget inference.

Compared against the live OctoEverywhere Gadget API page. The extension uses the same charcoal canvas, official artwork, Inter, and account-page colors. A separate visual reviewer returned `ship` with no material fixes after the mobile checkbox layout correction. Design detector output was empty.

## Remaining live acceptance

The [local simulator launcher](local-testing.md) was also verified through `Start`, `Status`, `Stop`, cached restart, and `Start -Rebuild`. It found all four simulated printers/cameras, saved camera selections, and passed a fresh JPEG test without a Gadget key. Rebuilding and reinstalling preserved settings. It was stopped after validation. Checkout-path rebasing exists but an actual repository relocation was not tested.

No production Gadget key was supplied, and no production image inference was requested. Real first-layer timing, live quota/billing behavior, physical pause confirmation, and execution on Linux/macOS still need the acceptance checks linked above. Exact first-layer detection is not available from the current Connect telemetry; startup remains an explicit estimate.

On this Windows test host, updating an already running executable through the host's install API failed with `MODULE_INSTALL_FAILED`. Stopping the test host, installing through the CLI, and restarting succeeded. Installation on other host platforms has not been exercised here.
