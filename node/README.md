# UFI003 Node Agent source snapshot

This directory preserves the current Linux user-space Agent and deployment helpers alongside the NAS source. It does not contain boot/rootfs images, device NV, private keys, pairing state, recordings or user configuration. The original Node input directory is read-only; future edits should be merged with the firmware development source before replacing this snapshot.

Build independently from the gateway (Go 1.24 or newer):

```sh
cd node/agent
GOWORK=off go test ./...
GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/nassimhub-agent ./cmd/nassimhub-agent
```

Agent module replacements resolve to the sibling proto/ and xport/ snapshots. NAS protocol dependencies remain under third_party/nassimhub-node/.

The UFI003 audio path still requires matching q6 kernel modules, enabled audio device tree, holder, permissions and local hardware validation. See deploy/audio/README-voice.md. Generic images must not contain local IMS/audio validation markers or device identity. Identity state belongs on persistent /var/lib/nassimhub.

The current call reader uses native D-Bus with NoAutoStart; retained terminated calls do not trigger one subprocess per property. The health endpoint queries modem lifecycle without collecting full SIM/signal telemetry. Real webpage sustained two-way audio still requires acceptance testing.

The standalone local management console uses HTTPS port 7581 and separate administrator authentication. First setup supports a private per-device shortcut that lets the owner directly choose and confirm a password. See [local management documentation](deploy/README-local-admin.md). No bootstrap proof or credentials belong in this repository.
