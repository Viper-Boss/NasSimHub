# RustSimHub via Classic Bluetooth

This optional NasSimHub provider connects one explicitly assigned AirSIM gateway
line to a paired RustSimHub stick. The gateway still checks its existing local
accounts and line assignments. The caller can read device state, incoming SMS
and call history, handle calls, and use the existing NasSimHub downstream
AirSIM PCM path. SMS writes, arbitrary network requests and internet sharing
are outside this provider. There are no new listening TCP ports.

## Pairing and host

Build with `go build ./cmd/rustsimhub-bluetooth`. Pair the N1 and stick with
BlueZ using numeric confirmation on both devices. The stick operator must pin
the N1 Bluetooth address and turn on the NasSimHub mode. On the stick console,
open the existing AirSIM gateway enrollment window for this separate NAS
credential. On the N1, run the command as the same effective UID that will
read the credential:

```sh
rustsimhub-bluetooth pair STICK_MAC APPROVED_LINE_ID /private/peer.json /run/rustsimhub-bt
```

Provide the six-digit code on stdin. The directory must already be private
(0700). Do not pass the code in an argument. The file is created once with
0600 permissions; it is not printed or overwritten. If pairing's result is
unknown, check the list of pairings on RustSimHub and revoke an orphaned
NasSimHub-Bluetooth credential before attempting a fresh enrollment.

For a native NasSimHub process that can use host Bluetooth, omit the final
socket-directory argument, then set `MODEMDECK_RUSTSIMHUB_BT_CONFIG` to the
private config path. For Docker, create a host directory with mode 0700,
and run the following host process under the same UID mapped into the container:

```sh
rustsimhub-bluetooth relay STICK_MAC /private/bt-host-sockets
```

Add `docker-compose.rustsimhub-bluetooth.yml` to the existing Compose files.
Set `MODEMDECK_RUSTSIMHUB_BT_CONFIG_FILE` and
`MODEMDECK_RUSTSIMHUB_BT_HOST_SOCKETS` to the two host paths. The host process
uses RFCOMM; the container gets only the two private Unix sockets and its
own credential. The config's line ID must be assigned to the authorized
NasSimHub account and selected in the existing AirSIM settings. This change
does not register a new line in the native web/Node device catalog.
The existing AirSIM gateway remains subject to its original allowed private
network access rules. Do not publish its plaintext ports on the Internet.

## Validation

`go test -race ./internal/rustsimbt` uses two in-memory application streams to
exercise sealed pairing, signed device and SMS reads, signed dialing, strict
per-downstream call ownership, media in both directions, hangup and an
ambiguous reply without retries. This does not measure N1 radio performance,
its firmware, cross-device pairing or physical call quality.
