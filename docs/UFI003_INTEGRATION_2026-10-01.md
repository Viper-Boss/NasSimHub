# UFI003 integration checkpoint — 2026-10-01

This checkpoint preserves the NAS integration and a separately buildable Node Agent snapshot under node/. Kernel, DTB and Factory/OTA images are outside this snapshot.

Changes include USB discovery/reconciliation, per-device dashboard cards and metrics, line-scoped communication statistics, call duration display, serving operator labels, and fast Node call polling. Device cards reuse the existing module templates and keep their data scoped to each device.

The Agent snapshot includes native D-Bus call observation, lightweight modem health checks, resource/radio reporting and the existing audio deployment helpers. The detailed modem status endpoint continues collecting SIM and radio telemetry. No device identity, keys, pairing records, local validation markers, phone numbers, Wi-Fi credentials, SMS or recordings are included.

Validation of this repository:

- Web type check and lint passed; 639 contract tests passed.
- NAS communication, httpapi, store, nassimhubnode and agentmedia tests passed.
- node/agent independently passed GOWORK=off go test ./... and a static linux/arm64 build.
- Device health read measured approximately 0.11 s; native call reads approximately 0.09–0.18 s.
- The production StartCall library path returned normally during an authorized carrier test with the NAS service running. Sustained browser-to-phone two-way audio remains pending user acceptance. This checkpoint is not a claim that voice, OTA or generic firmware release acceptance is complete.

See node/README.md for build and hardware prerequisites. Runtime identity must remain on persistent storage. UFI003's hardware EDL gesture is unchanged.
