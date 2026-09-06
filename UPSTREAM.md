# NAS communication gateway — development baseline

- Upstream: https://github.com/human-agent65535/ModemDeck
- Branch: `modemdeck`
- Baseline commit: `0437f29795d80642c44dbd913e82153456a93717`
- Local milestone: fnOS deployment and read-only preflight, 0.1.

This is a noncommercial derivative, not an original replacement for ModemDeck.
Keep `LICENSE`, `NOTICE.md`, and `THIRD_PARTY_NOTICES.md` when distributing it.
Module-side binaries are not supplied or automatically downloaded here.

The original application and hardware layers remain intact. Our first changes
live in `deploy/fnos/`, `scripts/fnos_preflight.py`, and the NAS preflight panel
inside Settings → Diagnostics. See `deploy/fnos/README.md` for the scope,
deployment gates, and what has **not** been hardware-tested.
