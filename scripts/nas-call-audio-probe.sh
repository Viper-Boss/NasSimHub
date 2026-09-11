#!/bin/sh
# Find out WHERE call audio dies, instead of guessing.
#
# The symptom -- the call connects, neither side hears anything -- is produced
# by at least five different faults, and every one of them looks identical from
# the web UI. So this walks the chain link by link and prints what each one
# actually reports. Run it, then make a call while it watches.
#
#   sh nas-call-audio-probe.sh
#
# The chain, in the order audio has to cross it:
#
#   far end <-> network <-> module baseband
#                             |  (1) VoLTE route mixers
#                             |  (2) secondary AUX PCM + its CLOCK
#                           module PCM
#                             |  (3) USB / PCM bridge
#                           NAS ALSA device
#                             |  (4) agent PCM socket  <-- MEASURED HERE
#                           agent
#                             |  (5) Opus / WebRTC
#                           browser
#
# Link 4 is the one that splits the problem in half, and the agent already
# measures it: it logs the RMS level of both directions every two seconds
# during a call. Section D below reads those lines. What they say decides
# where to look next, and section E spells that out.

set -u

API_CONTAINER=${API_CONTAINER:-nas-comm-api}
HW_CONTAINER=${HW_CONTAINER:-nas-comm-hardware}

say() { printf '\n=== %s ===\n' "$1"; }

# ---------------------------------------------------------------- A: the setup
say "A. Is the module voice runtime even switched on"
# Found the hard way on another feature in this project: a capability that is
# never wired looks exactly like a capability the hardware does not have.
if ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q "^${HW_CONTAINER}$"; then
    echo "the hardware container ${HW_CONTAINER} is not running; nothing below will work"
    exit 1
fi
printf '%-42s ' "MODEMDECK_QDC507_VOICE_RUNTIME_DIR"
docker exec "$HW_CONTAINER" sh -c 'echo "${MODEMDECK_QDC507_VOICE_RUNTIME_DIR:-(unset)}"' 2>/dev/null
echo "runtime directory contents (four reviewed artifacts expected):"
docker exec "$HW_CONTAINER" ls -la /tmp/qdc507-voice 2>/dev/null || echo "  (not mounted)"
echo
echo "If that directory is unset or empty, the voice reconciler never runs, the"
echo "VoLTE route mixers are never forced, and audio cannot work. That is the"
echo "first thing to fix and everything below is noise until it is."

# ------------------------------------------------------------- B: the AT chain
say "B. What the module says about its own audio path"
AT() {
    printf '%-28s ' "$1"
    docker exec "$HW_CONTAINER" mmcli -m any --command="$1" 2>&1 | tr -d '\r' | tr '\n' ' '
    echo
}
AT 'AT+QPCMV?'
# The clock. Not documented anywhere; found in atfwd_daemon, which maps
# AT+QCFG="pcmclk" onto /sys/devices/soc:qcom,msm-sec-auxpcm/enable_clk. With
# this off there is no bit clock to carry samples, and NOTHING ELSE SHOWS IT:
# QPCMV still answers 1,2 and the mixers still read back on.
AT 'AT+QCFG="pcmclk"'
AT 'AT+QDAI?'
AT 'AT+CLCC'
AT 'AT+QCFG="ims"'
echo
echo 'If AT+QCFG="pcmclk" reports 0, that alone explains silence in both'
echo 'directions. Tell me the value either way -- if it answers something this'
echo 'script did not anticipate, the code deliberately leaves it alone rather'
echo 'than writing a guessed number into a clock register.'

# ----------------------------------------------------------- C: the route
say "C. Are the VoLTE route mixers actually connected"
# The vendor daemon hardcodes the CS-voice UCM verb and never selects the VoLTE
# one, so on a VoLTE call -- every call on a modern network -- these two stay
# off unless this software forces them.
for control in \
    'SEC_AUX_PCM_RX_Voice Mixer VoLTE' \
    'VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE'
do
    printf '%-44s ' "$control"
    docker exec "$HW_CONTAINER" sh -c \
        "adb shell \"amix '$control'\" 2>/dev/null | tr -d '\r' | tail -1" \
        2>/dev/null || echo "(adb unavailable)"
done
echo
echo "Both must read on. One off in the Tx direction means the far end hears"
echo "nothing while you may still hear them, which is the asymmetric case."

# --------------------------------------------------- D: the decisive measurement
say "D. Is there audio at the agent boundary (make a call NOW)"
echo "Watching the agent log for 60 seconds. Place a call and TALK, and have"
echo "the far end talk too -- silence on the line reads the same as a broken"
echo "path, so both sides have to make noise for this to mean anything."
echo
echo "capture  = module -> agent  (what the far end sent you)"
echo "playback = agent -> module  (what you sent the far end)"
echo
timeout 60 docker logs -f --since 1s "$HW_CONTAINER" 2>&1 \
    | grep --line-buffered 'modem call audio level' \
    | sed 's/.*direction=/direction=/' \
    || echo "(no level lines appeared)"

# ------------------------------------------------------- E: what the answer means
say "E. Reading section D"
cat <<'GUIDE'
No level lines at all
    The agent never opened the PCM device, so the fault is before link 4.
    Check section A first, then the agent log for "call media" errors.

Both directions silent=true
    The PCM path carries nothing in either direction. That is the clock
    (section B) or the route (section C) -- not WebRTC, and not the browser.

capture silent=true, playback has level
    Your voice reaches the module but nothing comes back. Downlink route:
    the RX mixer, or the module never got audio from the network.

capture has level, playback silent=true
    You are hearing them but sending nothing. The browser is not delivering
    microphone audio -- check the site has microphone permission and that the
    page is on HTTPS or localhost, since getUserMedia is refused otherwise.

Both directions have level, but nobody hears anything
    The audio crosses the modem boundary fine, so the fault is above link 4:
    the Opus bridge or the browser. This is the one case where the module and
    its firmware are innocent.

rms_dbfs near -120 with silent=false
    Not silence but almost nothing -- a level leak rather than a connected
    route. Historically this is what a half-connected mixer looked like on
    this module.
GUIDE
echo
echo "Send me section B, C and D output and I can tell you which link it is."
