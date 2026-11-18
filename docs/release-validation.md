# Live release acceptance

The local automated suite does not establish compatibility with a physical printer, camera, or a production Gadget account. Record the tested Connect version, printer model/firmware, camera driver, and outcome for each case before publishing this beta.

- Install the packaged extension through Prusa Connect's module installer on a stock host without a records/database backend. Verify its manifest identity, permissions, data-directory grant, outbound connections, page registration, and lifecycle shutdown/restart. Missing/unwritable data grants must produce a configuration error.
- Use a Connect read-scoped credential that can view the selected camera. Verify that a viewer cannot alter configuration and that a person cannot see or change another person's hidden printers/history.
- Validate a Gadget API key through the settings page. Confirm keys and context URLs are absent from status responses, UI payloads, and logs.
- Start a print with a long homing/probing/purge sequence. Verify the automatic warmup delay and telemetry gate, then fresh sequential JPEG submissions after monitoring begins. Repeat on firmware with and without extrusion odometers.
- Pause/resume, temporarily disconnect/reconnect, complete, cancel, and immediately start another print. Verify context continuity for the same job and a fresh context for the new job.
- Restart the extension and the host mid-print. Confirm the job/context and action history resume without duplicate warnings or pause commands.
- Disconnect the camera, provide an old frame, and revoke its token. Confirm a visible inactive/error state and recovery when the camera/token becomes usable.
- Exercise network failure and fallback, rate limiting, exhausted quota, revoked Gadget key, and expired/cancelled context. Confirm delayed fresh-image retries only for retryable failures.
- Verify automatic pause cannot be enabled without a per-printer Operator grant for the module principal. Grant it, then enable automatic pause on a noncritical supervised print. Trigger a controlled test recommendation, confirm exactly one pause command and a confirmed paused state, and verify a delayed result cannot act on a subsequent job. Revoke the module grant and verify it cannot issue further commands.
- Run multiple enabled printers with a slow/unavailable camera on one. Verify the others remain responsive, work stays bounded, and memory remains within the declared budget.
- Exercise the page at narrow desktop/mobile widths and with keyboard navigation. Verify management controls reflect host permissions.
- Build all declared release binaries and verify at least one installation on each supported host architecture before claiming that architecture is production-tested.

Gadget API tests in this repository never call production. Live tests using an account and image uploads must be intentional; use the account's free-only setting or billing limits appropriate to that test.

