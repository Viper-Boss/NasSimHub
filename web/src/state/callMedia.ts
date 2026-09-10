import { reactive, watch } from 'vue'
import { fixtureCallMediaPreview, gateway } from '../api/client'
import type { CallMediaICEConfiguration, CallSession } from '../api/types'
import { ApiError } from '../api/types'
import { translate } from '../i18n'
import {
  applySelectedAudioOutput,
  audioState,
  cancelSelectedAudioOutputApplication,
  markAudioInputActive,
  markAudioInputError,
  markAudioInputInactive,
  markAudioInputSwitching,
  markAudioOutputInactive,
  refreshAudioDevices,
  selectedAudioInputConstraints
} from './audio'

export type CallMediaStatus =
  | 'idle'
  | 'unavailable'
  | 'requesting'
  | 'connecting'
  | 'recovering'
  | 'active'
  | 'error'

const ICE_GATHERING_TIMEOUT_MS = 5000
const MEDIA_RECOVERY_TIMEOUT_MS = 15_000
const MEDIA_RESTART_GRACE_MS = 2000
const MEDIA_RESTART_CONNECT_TIMEOUT_MS = 8000
const MEDIA_MAX_CONSECUTIVE_RESTARTS = 3
const MEDIA_MAX_TOTAL_RESTARTS = 12
const MEDIA_LOCK_PREFIX = 'modemdeck-call-media:'

export const callMediaState = reactive<{
  callID: string
  status: CallMediaStatus
  error: string
  muted: boolean
  playbackBlocked: boolean
}>({
  callID: '',
  status: 'idle',
  error: '',
  muted: false,
  playbackBlocked: false
})

let generation = 0
let attemptedCallID = ''
let currentCallID = ''
let peer: RTCPeerConnection | undefined
let localStream: MediaStream | undefined
let remoteStream: MediaStream | undefined
let remoteAudio: HTMLAudioElement | undefined
let inputReplaceGeneration = 0
let queuedInputDeviceID: string | undefined
let inputSwitchPromise: Promise<void> | undefined
let recoveryTimeoutID: number | undefined
let mediaLockAbort: AbortController | undefined
let iceConfiguration: CallMediaICEConfiguration | undefined
let restartTimeoutID: number | undefined
let peerRecovering = false
let consecutiveRestarts = 0
let totalRestarts = 0

type MediaOwnership = {
  ownerToken: string
  claimed: boolean
  release: () => void
  cleanup: Promise<void>
}

let mediaOwnership: MediaOwnership | undefined

type MicrophonePipeline = {
  capture: MediaStream
  context: AudioContext
  source: MediaStreamAudioSourceNode
  gain: GainNode
  limiter: DynamicsCompressorNode
  destination: MediaStreamAudioDestinationNode
  track: MediaStreamTrack
}

let microphonePipeline: MicrophonePipeline | undefined

function clearRecoveryWindow(): void {
  if (recoveryTimeoutID === undefined) return
  window.clearTimeout(recoveryTimeoutID)
  recoveryTimeoutID = undefined
}

function clearRestartWindow(): void {
  if (restartTimeoutID === undefined) return
  window.clearTimeout(restartTimeoutID)
  restartTimeoutID = undefined
}

function stopMicrophonePipeline(pipeline: MicrophonePipeline): void {
  for (const track of pipeline.capture.getTracks()) track.stop()
  for (const track of pipeline.destination.stream.getTracks()) track.stop()
  pipeline.source.disconnect()
  pipeline.gain.disconnect()
  pipeline.limiter.disconnect()
  pipeline.destination.disconnect()
  void pipeline.context.close()
}

async function createMicrophonePipeline(
  capture: MediaStream
): Promise<MicrophonePipeline> {
  const context = new AudioContext()
  try {
    await context.resume()
    const source = context.createMediaStreamSource(capture)
    const gain = context.createGain()
    gain.gain.value = audioState.microphoneGain / 100

    const limiter = context.createDynamicsCompressor()
    limiter.threshold.value = -3
    limiter.knee.value = 0
    limiter.ratio.value = 20
    limiter.attack.value = 0.003
    limiter.release.value = 0.1

    const destination = context.createMediaStreamDestination()
    source.connect(gain).connect(limiter).connect(destination)
    const track = destination.stream.getAudioTracks()[0]
    if (!track) throw new Error(translate('runtime.microphoneTrackMissing'))

    return { capture, context, source, gain, limiter, destination, track }
  } catch (error) {
    for (const track of capture.getTracks()) track.stop()
    void context.close()
    throw error
  }
}

function releasePeer(): void {
  const connection = peer
  if (!connection) return
  peer = undefined
  connection.onconnectionstatechange = null
  connection.ontrack = null
  connection.close()
}

function stopResources(): void {
  generation += 1
  inputReplaceGeneration += 1
  queuedInputDeviceID = undefined
  currentCallID = ''
  clearRecoveryWindow()
  clearRestartWindow()
  peerRecovering = false
  iceConfiguration = undefined
  consecutiveRestarts = 0
  totalRestarts = 0
  mediaLockAbort?.abort()
  mediaLockAbort = undefined
  mediaOwnership?.release()

  releasePeer()
  if (microphonePipeline) {
    stopMicrophonePipeline(microphonePipeline)
    microphonePipeline = undefined
  } else {
    for (const track of localStream?.getTracks() || []) track.stop()
  }
  for (const track of remoteStream?.getTracks() || []) track.stop()
  localStream = undefined
  remoteStream = undefined

  if (remoteAudio) {
    cancelSelectedAudioOutputApplication(remoteAudio)
    remoteAudio.pause()
    remoteAudio.srcObject = null
    remoteAudio.remove()
    remoteAudio = undefined
  }
  markAudioInputInactive()
  markAudioOutputInactive()
}

function setIdle(status: 'idle' | 'unavailable', callID = ''): void {
  stopResources()
  callMediaState.callID = callID
  callMediaState.status = status
  callMediaState.error = ''
  callMediaState.muted = false
  callMediaState.playbackBlocked = false
}

function mediaError(error: unknown): string {
  if (error instanceof ApiError && error.code === 'turn_unavailable') {
    return translate('runtime.externalCallTURNUnavailable')
  }
  if (error instanceof DOMException) {
    if (error.name === 'NotAllowedError') return translate('runtime.microphoneUnauthorized')
    if (error.name === 'NotFoundError' || error.name === 'OverconstrainedError') {
      return audioState.selectedInputID
        ? translate('runtime.microphoneUnavailable')
        : translate('runtime.noMicrophone')
    }
    if (error.name === 'NotReadableError') return translate('runtime.microphoneBusy')
  }
  return error instanceof Error ? error.message : translate('runtime.callAudioFailed')
}

function waitForICEGathering(connection: RTCPeerConnection): Promise<void> {
  if (connection.iceGatheringState === 'complete') return Promise.resolve()

  return new Promise((resolve, reject) => {
    const timeout = window.setTimeout(() => {
      connection.removeEventListener('icegatheringstatechange', onStateChange)
      reject(new Error(translate('runtime.audioNegotiationTimeout')))
    }, ICE_GATHERING_TIMEOUT_MS)
    const onStateChange = () => {
      if (connection.iceGatheringState !== 'complete') return
      window.clearTimeout(timeout)
      connection.removeEventListener('icegatheringstatechange', onStateChange)
      resolve()
    }
    connection.addEventListener('icegatheringstatechange', onStateChange)
  })
}

async function playRemoteAudio(): Promise<void> {
  const element = remoteAudio
  if (!element) return
  element.volume = audioState.callVolume / 100
  if (!(await applySelectedAudioOutput(element))) {
    element.pause()
    callMediaState.playbackBlocked = false
    return
  }
  if (remoteAudio !== element) return
  await element.play().then(
    () => {
      callMediaState.playbackBlocked = false
    },
    () => {
      callMediaState.playbackBlocked = true
    }
  )
}

function attachRemoteAudio(stream: MediaStream): void {
  if (!remoteAudio) {
    remoteAudio = document.createElement('audio')
    remoteAudio.autoplay = true
    remoteAudio.setAttribute('playsinline', '')
    remoteAudio.hidden = true
    document.body.append(remoteAudio)
  }
  remoteAudio.srcObject = stream
  void playRemoteAudio()
}

function failConnection(callID: string, token: number, error: unknown): void {
  if (generation !== token || currentCallID !== callID) return
  stopResources()
  callMediaState.callID = callID
  callMediaState.status = 'error'
  callMediaState.error = mediaError(error)
  callMediaState.muted = false
  markAudioInputError(callMediaState.error)
}

function beginRecoveryWindow(callID: string, token: number): void {
  if (recoveryTimeoutID !== undefined) return
  recoveryTimeoutID = window.setTimeout(() => {
    recoveryTimeoutID = undefined
    failConnection(
      callID,
      token,
      new Error(translate('runtime.callAudioConnectionFailed'))
    )
  }, MEDIA_RECOVERY_TIMEOUT_MS)
}

function iceServerList(configuration: CallMediaICEConfiguration) {
  return configuration.ice_servers.map(server => ({
    urls: server.urls,
    ...(server.username ? { username: server.username } : {}),
    ...(server.credential ? { credential: server.credential } : {})
  }))
}

// establishPeer builds a peer for the call and completes one offer/answer
// exchange with the server. It is shared by the first connection and by every
// renegotiation: when ICE breaks, the only reliable recovery is a new transport
// with fresh candidates, because waiting on a dead candidate pair never
// recovers. The server tolerates the repeat exchange as long as the browser
// presents the owner token it already holds.
async function establishPeer(
  callID: string,
  token: number,
  ownership: MediaOwnership,
  configuration: CallMediaICEConfiguration,
  pipeline: MicrophonePipeline,
  signal: AbortSignal
): Promise<RTCPeerConnection> {
  const connection = new RTCPeerConnection({
    iceServers: iceServerList(configuration),
    iceTransportPolicy: configuration.ice_transport_policy
  })
  peer = connection

  connection.ontrack = event => {
    if (generation !== token || currentCallID !== callID || peer !== connection) return
    const stream = event.streams[0] ?? new MediaStream([event.track])
    remoteStream = stream
    attachRemoteAudio(stream)
  }
  connection.onconnectionstatechange = () => {
    if (generation !== token || currentCallID !== callID || peer !== connection) return
    handleConnectionState(callID, token, ownership, signal, connection)
  }

  for (const track of pipeline.destination.stream.getAudioTracks()) {
    connection.addTrack(track, pipeline.destination.stream)
  }
  const offer = await connection.createOffer()
  await connection.setLocalDescription(offer)
  await waitForICEGathering(connection)
  if (generation !== token || currentCallID !== callID || peer !== connection) {
    return connection
  }

  const offerSDP = connection.localDescription?.sdp
  if (!offerSDP) throw new Error(translate('runtime.audioOfferMissing'))
  ownership.claimed = true
  const answerSDP = await gateway.exchangeCallMedia(
    callID,
    ownership.ownerToken,
    offerSDP,
    signal
  )
  if (generation !== token || currentCallID !== callID || peer !== connection) {
    return connection
  }
  await connection.setRemoteDescription({ type: 'answer', sdp: answerSDP })
  return connection
}

function handleConnectionState(
  callID: string,
  token: number,
  ownership: MediaOwnership,
  signal: AbortSignal,
  connection: RTCPeerConnection
): void {
  const state = connection.connectionState
  if (state === 'connected') {
    peerRecovering = false
    consecutiveRestarts = 0
    clearRestartWindow()
    clearRecoveryWindow()
    callMediaState.status = 'active'
    callMediaState.error = ''
    return
  }
  if (state === 'connecting') {
    callMediaState.status = peerRecovering ? 'recovering' : 'connecting'
    return
  }
  if (state === 'disconnected' || state === 'failed') {
    peerRecovering = true
    clearRecoveryWindow()
    callMediaState.status = 'recovering'
    callMediaState.error = ''
    if (iceConfiguration && microphonePipeline) {
      scheduleRestart(
        callID,
        token,
        ownership,
        signal,
        state === 'failed' ? 0 : MEDIA_RESTART_GRACE_MS
      )
    } else {
      beginRecoveryWindow(callID, token)
    }
    return
  }
  if (state === 'closed') {
    failConnection(
      callID,
      token,
      new Error(translate('runtime.callAudioConnectionFailed'))
    )
  }
}

function scheduleRestart(
  callID: string,
  token: number,
  ownership: MediaOwnership,
  signal: AbortSignal,
  delayMS: number
): void {
  if (restartTimeoutID !== undefined) return
  restartTimeoutID = window.setTimeout(() => {
    restartTimeoutID = undefined
    void attemptRestart(callID, token, ownership, signal)
  }, delayMS)
}

function waitForConnection(
  connection: RTCPeerConnection,
  timeoutMS: number
): Promise<boolean> {
  const state = connection.connectionState
  if (state === 'connected') return Promise.resolve(true)
  if (state === 'failed' || state === 'closed') return Promise.resolve(false)

  return new Promise(resolve => {
    const finish = (): void => {
      window.clearTimeout(timeoutID)
      connection.removeEventListener('connectionstatechange', onStateChange)
      resolve(connection.connectionState === 'connected')
    }
    const onStateChange = (): void => {
      const next = connection.connectionState
      if (next === 'connected' || next === 'failed' || next === 'closed') finish()
    }
    const timeoutID = window.setTimeout(finish, timeoutMS)
    connection.addEventListener('connectionstatechange', onStateChange)
  })
}

async function attemptRestart(
  callID: string,
  token: number,
  ownership: MediaOwnership,
  signal: AbortSignal
): Promise<void> {
  if (generation !== token || currentCallID !== callID) return
  const configuration = iceConfiguration
  const pipeline = microphonePipeline
  if (
    !configuration ||
    !pipeline ||
    signal.aborted ||
    consecutiveRestarts >= MEDIA_MAX_CONSECUTIVE_RESTARTS ||
    totalRestarts >= MEDIA_MAX_TOTAL_RESTARTS
  ) {
    failConnection(
      callID,
      token,
      new Error(translate('runtime.callAudioConnectionFailed'))
    )
    return
  }
  consecutiveRestarts += 1
  totalRestarts += 1
  callMediaState.status = 'recovering'
  releasePeer()

  let connected = false
  try {
    const connection = await establishPeer(
      callID,
      token,
      ownership,
      configuration,
      pipeline,
      signal
    )
    if (generation !== token || currentCallID !== callID || peer !== connection) return
    connected = await waitForConnection(connection, MEDIA_RESTART_CONNECT_TIMEOUT_MS)
  } catch {
    connected = false
  }
  if (generation !== token || currentCallID !== callID) return
  if (connected) {
    consecutiveRestarts = 0
    clearRecoveryWindow()
    callMediaState.status = 'active'
    callMediaState.error = ''
    return
  }
  scheduleRestart(
    callID,
    token,
    ownership,
    signal,
    MEDIA_RESTART_GRACE_MS * consecutiveRestarts
  )
}

async function connect(
  callID: string,
  token: number,
  ownership: MediaOwnership,
  signal: AbortSignal
): Promise<void> {
  let pendingMicrophone: MediaStream | undefined
  let pendingPipeline: MicrophonePipeline | undefined
  try {
    callMediaState.status = 'requesting'
    if (!navigator.mediaDevices?.getUserMedia) {
      throw new Error(translate('runtime.microphoneHTTPSRequired'))
    }
    const configuration = await gateway.getCallMediaICEConfiguration(
      callID,
      signal
    )
    if (generation !== token || currentCallID !== callID) return
    iceConfiguration = configuration
    pendingMicrophone = await navigator.mediaDevices.getUserMedia({
      audio: selectedAudioInputConstraints(),
      video: false
    })
    pendingPipeline = await createMicrophonePipeline(pendingMicrophone)
    if (generation !== token || currentCallID !== callID) {
      stopMicrophonePipeline(pendingPipeline)
      return
    }
    const microphone = pendingMicrophone
    const pipeline = pendingPipeline
    localStream = microphone
    microphonePipeline = pipeline
    pendingMicrophone = undefined
    pendingPipeline = undefined
    markAudioInputActive()

    callMediaState.status = 'connecting'
    await establishPeer(callID, token, ownership, configuration, pipeline, signal)
  } catch (error) {
    if (pendingPipeline) stopMicrophonePipeline(pendingPipeline)
    else for (const track of pendingMicrophone?.getTracks() || []) track.stop()
    failConnection(callID, token, error)
  }
}

async function ownAndConnect(
  callID: string,
  token: number,
  controller: AbortController
): Promise<void> {
  try {
    if (!navigator.locks?.request) {
      throw new Error(translate('runtime.callAudioFailed'))
    }
    await navigator.locks.request(
      `${MEDIA_LOCK_PREFIX}${callID}`,
      { mode: 'exclusive', signal: controller.signal },
      async lock => {
        if (!lock || generation !== token || currentCallID !== callID) return

        let releaseLock: () => void = () => undefined
        const released = new Promise<void>(resolve => {
          releaseLock = resolve
        })
        let completeCleanup: () => void = () => undefined
        const cleanup = new Promise<void>(resolve => {
          completeCleanup = resolve
        })
        const ownership: MediaOwnership = {
          ownerToken: globalThis.crypto.randomUUID(),
          claimed: false,
          release: releaseLock,
          cleanup
        }
        mediaOwnership = ownership
        try {
          await connect(callID, token, ownership, controller.signal)
          await released
        } finally {
          try {
            if (ownership.claimed) {
              await gateway
                .releaseCallMedia(callID, ownership.ownerToken)
                .catch(() => undefined)
            }
          } finally {
            if (mediaOwnership === ownership) mediaOwnership = undefined
            completeCleanup()
          }
        }
      }
    )
  } catch (error) {
    if (controller.signal.aborted || generation !== token || currentCallID !== callID) {
      return
    }
    failConnection(callID, token, error)
  } finally {
    if (mediaLockAbort === controller) mediaLockAbort = undefined
  }
}

export function syncCallMedia(session: CallSession | null): void {
  if (fixtureCallMediaPreview) {
    if (!session || session.phase !== 'active') {
      attemptedCallID = ''
      setIdle('idle')
      return
    }
    if (callMediaState.callID === session.id && callMediaState.status === 'active') return
    setIdle('idle')
    callMediaState.callID = session.id
    callMediaState.status = 'active'
    return
  }
  if (!session || session.phase !== 'active') {
    attemptedCallID = ''
    setIdle('idle')
    return
  }
  if (!session.media_available) {
    attemptedCallID = ''
    setIdle('unavailable', session.id)
    return
  }
  if (currentCallID === session.id || attemptedCallID === session.id) return

  stopResources()
  attemptedCallID = session.id
  currentCallID = session.id
  callMediaState.callID = session.id
  callMediaState.status = 'requesting'
  callMediaState.error = ''
  callMediaState.muted = false
  callMediaState.playbackBlocked = false
  const token = generation
  const controller = new AbortController()
  mediaLockAbort = controller
  void ownAndConnect(session.id, token, controller)
}

export function retryCallMedia(session: CallSession | null): void {
  if (!session || session.phase !== 'active' || !session.media_available) return
  attemptedCallID = ''
  syncCallMedia(session)
}

export function toggleCallMute(): void {
  if (fixtureCallMediaPreview) {
    if (callMediaState.status === 'active') {
      callMediaState.muted = !callMediaState.muted
    }
    return
  }
  if (!localStream) return
  const muted = !callMediaState.muted
  for (const track of localStream.getAudioTracks()) track.enabled = !muted
  for (const track of microphonePipeline?.destination.stream.getAudioTracks() || []) {
    track.enabled = !muted
  }
  callMediaState.muted = muted
}

export function resumeCallAudio(): void {
  if (!remoteAudio) return
  void playRemoteAudio()
}

async function replaceCallInput(deviceID: string): Promise<void> {
  if (!peer || !localStream || !microphonePipeline || !currentCallID) return
  const connection = peer
  const stream = localStream
  const pipeline = microphonePipeline
  const callID = currentCallID
  const token = ++inputReplaceGeneration
  let replacement: MediaStream | undefined
  let replacementPipeline: MicrophonePipeline | undefined

  markAudioInputSwitching()
  try {
    replacement = await navigator.mediaDevices.getUserMedia({
      audio: selectedAudioInputConstraints(deviceID),
      video: false
    })
    replacementPipeline = await createMicrophonePipeline(replacement)
    if (
      token !== inputReplaceGeneration ||
      connection !== peer ||
      stream !== localStream ||
      callID !== currentCallID
    ) {
      stopMicrophonePipeline(replacementPipeline)
      return
    }

    const newTrack = replacementPipeline.track
    const sender = connection
      .getSenders()
      .find(candidate => candidate.track?.kind === 'audio')
    if (!newTrack || !sender) throw new Error(translate('runtime.microphoneTrackMissing'))

    await sender.replaceTrack(newTrack)
    if (
      token !== inputReplaceGeneration ||
      connection !== peer ||
      stream !== localStream ||
      callID !== currentCallID
    ) {
      stopMicrophonePipeline(replacementPipeline)
      return
    }

    localStream = replacement
    microphonePipeline = replacementPipeline
    replacement = undefined
    replacementPipeline = undefined
    stopMicrophonePipeline(pipeline)
    callMediaState.muted = false
    markAudioInputActive()
    void refreshAudioDevices()
  } catch (error) {
    if (replacementPipeline) stopMicrophonePipeline(replacementPipeline)
    else for (const track of replacement?.getTracks() || []) track.stop()
    if (token !== inputReplaceGeneration || callID !== currentCallID) return
    markAudioInputError(mediaError(error))
  }
}

function queueCallInputReplacement(deviceID: string): void {
  queuedInputDeviceID = deviceID
  if (inputSwitchPromise) return

  inputSwitchPromise = (async () => {
    while (queuedInputDeviceID !== undefined) {
      const nextDeviceID = queuedInputDeviceID
      queuedInputDeviceID = undefined
      await replaceCallInput(nextDeviceID)
    }
  })().finally(() => {
    inputSwitchPromise = undefined
  })
}

export function shutdownCallMedia(): void {
  attemptedCallID = ''
  setIdle('idle')
}

export async function releaseCallMediaForSessionEnd(): Promise<void> {
  const ownership = mediaOwnership
  shutdownCallMedia()
  if (ownership?.claimed) await ownership.cleanup
}

watch(
  () => [audioState.selectedOutputID, audioState.devicesRevision] as const,
  () => {
    if (remoteAudio) void playRemoteAudio()
  }
)

watch(
  () => audioState.callVolume,
  volume => {
    if (remoteAudio) remoteAudio.volume = volume / 100
  }
)

watch(
  () => audioState.microphoneGain,
  gain => {
    const pipeline = microphonePipeline
    if (!pipeline) return
    pipeline.gain.gain.setTargetAtTime(
      gain / 100,
      pipeline.context.currentTime,
      0.015
    )
  }
)

watch(
  () => audioState.selectedInputID,
  deviceID => {
    if (peer && localStream && currentCallID) queueCallInputReplacement(deviceID)
  }
)
