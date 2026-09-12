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
echo
echo 'Every line above reading "Unknown error" does NOT mean the module is'
echo 'broken: mmcli on this build swallows the AT response. The agent reads the'
echo 'same values over its own D-Bus path and succeeds, so check the snapshot'
echo 'voice_verification field and the agent log rather than trusting this'
echo 'section when it comes back empty.'

# ----------------------------------------------------------- C: the route
say "C. Are the VoLTE route mixers actually connected"
# The vendor daemon hardcodes the CS-voice UCM verb and never selects the VoLTE
# one, so on a VoLTE call -- every call on a modern network -- these two stay
# off unless this software forces them.
# The agent runs its own adb server on a unix socket rather than the default
# TCP port, so a bare `adb` here reports "failed to start daemon" and the
# section printed nothing at all. Reported from the first real run of this
# script; ADB_SERVER_SOCKET is what makes it talk to the running server instead
# of trying to start a second one.
ADB_SOCKET=${ADB_SOCKET:-localfilesystem:/tmp/qdc507-adb/server.sock}
for control in \
    'SEC_AUX_PCM_RX_Voice Mixer VoLTE' \
    'VoLTE_Tx Mixer SEC_AUX_PCM_TX_VoLTE'
do
    printf '%-44s ' "$control"
    docker exec -e "ADB_SERVER_SOCKET=$ADB_SOCKET" "$HW_CONTAINER" sh -c \
        "adb shell \"amix '$control'\" 2>&1 | tr -d '\r' | tail -1" \
        2>&1 || echo "(adb unavailable)"
done
echo
echo "Both must read on. One off in the Tx direction means the far end hears"
echo "nothing while you may still hear them, which is the asymmetric case."

# ------------------------------------------------- C2: the other front end
say "C2. Is the voice session also connected to the AFE PCM front end"
# A lead from cellbridge-mac, which drives the SAME module and the SAME PCM
# bridge binary (identical sha256). Alongside the UCM VoLTE verb -- which
# enables the SEC_AUX_PCM controls section C just read -- it additionally
# connects the voice session to AFE_PCM, for all four session types.
#
# Why that might matter here: the bridge opens hw:0,4. If that device is fed by
# AFE_PCM rather than the secondary AUX PCM, then connecting only SEC_AUX_PCM
# leaves the bridge reading a front end nothing is written to -- zeros in both
# directions, with every control this script reads in section C reporting "on".
#
# This is a HYPOTHESIS, not a finding, which is why this section only reads.
# Nothing is written to the module on a guess: if these come back 1 the idea is
# dead and no harm was done, and if they come back 0 it is worth one experiment.
for control in \
    'AFE_PCM_RX_Voice Mixer VoLTE' \
    'AFE_PCM_RX_Voice Mixer CSVoice' \
    'VoLTE_Tx Mixer AFE_PCM_TX_VoLTE' \
    'Voice_Tx Mixer AFE_PCM_TX_Voice'
do
    printf '%-44s ' "$control"
    docker exec -e "ADB_SERVER_SOCKET=$ADB_SOCKET" "$HW_CONTAINER" sh -c \
        "adb shell \"amix '$control'\" 2>&1 | tr -d '\r' | tail -1" \
        2>&1 || echo "(adb unavailable)"
done
echo
echo "A control this firmware does not have will report an error rather than a"
echo "value, and that is an answer too: it would mean this front end is not"
echo "reachable on this build and the idea can be dropped."

# --------------------------------------------- C3: the one-shot route session
say "C3. How long has the route session been running, and how many calls has it carried"
# The module's PCM bridge establishes a route session that carries exactly ONE
# call: afterwards the DSP stops writing to hw:0,4 and every call is silent in
# both directions. Measured by cellbridge-mac on this same binary; the control
# experiment was to restart only the bridge, which recovered the next call.
#
# The reason this is invisible is that the bridge stays alive and keeps
# reporting a healthy session, so the two numbers below are the whole diagnosis:
# a bridge whose start time predates your last call has already been used.
docker exec -e "ADB_SERVER_SOCKET=$ADB_SOCKET" "$HW_CONTAINER" sh -c \
    "adb shell 'pidof mavo-pcm-bridge.armv7 || pidof mavo-pcm-bridge' 2>&1 | tr -d '\r'" 2>&1 \
    | sed 's/^/bridge pid: /'
docker exec -e "ADB_SERVER_SOCKET=$ADB_SOCKET" "$HW_CONTAINER" sh -c \
    "adb shell 'p=\$(pidof mavo-pcm-bridge.armv7 || pidof mavo-pcm-bridge); test -n \"\$p\" && ls -ld /proc/\$p' 2>&1 | tr -d '\r'" 2>&1 \
    | sed 's/^/bridge started: /'
echo "agent re-arm activity (empty means the agent has never restarted it):"
docker logs --since 24h "$HW_CONTAINER" 2>&1 \
    | grep -E 're-arm(ed)? (the )?QDC507 VoLTE route session' \
    | tail -8 \
    || echo "  (none)"
echo
echo "If the bridge start time is older than your most recent call and there is"
echo "no re-arm line above, that call ran on a used session and was silent for"
echo "that reason alone."

# ----------------------------------------- C4: does the call have a media binding
say "C4. Can the agent open the module's audio device at all"
# The fault that made the page sit on "ringing" forever while the call was up.
#
# The chain: the agent names an audio port for the call ("quectel-uac:" plus the
# module's sysfs path), the media manager has to resolve that port to a host
# ALSA device, and only then does the call report media_configured. The server
# will not tell the browser that media is available without it, and the browser
# will not start the audio exchange without THAT -- so an unresolvable port
# leaves the call connected, silent and permanently "ringing", with nothing in
# the UI to say why.
#
# Until r87 the resolution came only from a hand-written bindings file, which no
# installation had, so this was false everywhere. r87 derives it from sysfs.
echo "agent media binding resolution (empty means it has not run yet):"
docker logs --since 24h "$HW_CONTAINER" 2>&1 \
    | grep -E 'module USB audio device|no media binding could be derived' \
    | tail -5 \
    || echo "  (none)"
echo
echo "host ALSA cards (the module's UAC card must be one of these):"
docker exec "$HW_CONTAINER" sh -c 'cat /proc/asound/cards 2>&1' 2>&1 | sed 's/^/  /'
echo
echo "arecord/aplay present in the hardware container:"
docker exec "$HW_CONTAINER" sh -c \
    'command -v arecord >/dev/null && command -v aplay >/dev/null && echo "  yes" || echo "  NO -- install alsa-utils; without them no call can carry audio"' 2>&1
echo
echo "During a call, these two fields decide whether the browser ever starts:"
echo "  agent snapshot  -> calls[].media_configured   (needs the binding above)"
echo "  /calls/active   -> calls[].media_available    (needs media_configured)"
echo "Both false with the call in phase=active is this section's fault, not WebRTC."

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
