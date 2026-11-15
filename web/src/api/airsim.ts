import { API_ROOT, fixtureMode, get, writeJSON } from './client'

// AirSIM iPhone app compatibility (internal/airsimgw on the server).
//
// Its own module, like callServices.ts: client.ts is the startup bundle and
// this is reachable from one administrator settings page.

export interface AirSIMDevice {
  id: string
  user_id: string
  device_hint?: string
  created_at: string
  last_seen_at?: string
  last_peer?: string
}

export interface AirSIMPairingWindow {
  code: string
  expires_at: string
}

export interface AirSIMStatus {
  enabled: boolean
  line_id: string
  line_ids?: string[]
  agent_id: string
  listening: boolean
  listen_error?: string
  control_port: number
  pcm_port: number
  pairing_port: number
  pairing?: AirSIMPairingWindow | null
  last_paired?: AirSIMDevice | null
  devices: AirSIMDevice[]
}

export interface AirSIMSettings {
  available: boolean
  status?: AirSIMStatus
}

function parseSettings(value: unknown): AirSIMSettings {
  const result = value as AirSIMSettings | null
  if (!result || typeof result.available !== 'boolean') {
    throw new Error('Invalid AirSIM settings response.')
  }
  if (result.status && !Array.isArray(result.status.devices)) {
    result.status.devices = []
  }
  return result
}

const fixtureStatus: AirSIMStatus = {
  enabled: false,
  line_id: '',
  line_ids: [],
  agent_id: 'nassimhub-fixture',
  listening: true,
  control_port: 7592,
  pcm_port: 7593,
  pairing_port: 7594,
  devices: []
}

export async function getAirSIMSettings(): Promise<AirSIMSettings> {
  if (fixtureMode) return { available: true, status: { ...fixtureStatus } }
  return parseSettings(await get(`${API_ROOT}/settings/airsim`))
}

export async function saveAirSIMSettings(
  enabled: boolean,
  lineID: string | readonly string[]
): Promise<AirSIMSettings> {
  if (fixtureMode)
    throw new Error('Demo mode: nothing was saved to the gateway.')
  const lineIDs =
    typeof lineID === 'string' ? (lineID ? [lineID] : []) : [...lineID]
  return parseSettings(
    await writeJSON(
      `${API_ROOT}/settings/airsim`,
      'PUT',
      { enabled, line_id: lineIDs[0] || '', line_ids: lineIDs },
      200
    )
  )
}

export async function startAirSIMPairing(): Promise<AirSIMPairingWindow> {
  if (fixtureMode) {
    return {
      code: '042917',
      expires_at: new Date(Date.now() + 120_000).toISOString()
    }
  }
  const window = (await writeJSON(
    `${API_ROOT}/settings/airsim/pairing`,
    'POST',
    {},
    200
  )) as AirSIMPairingWindow
  if (!window || !/^\d{6}$/.test(window.code) || !window.expires_at) {
    throw new Error('Invalid AirSIM pairing response.')
  }
  return window
}

export async function cancelAirSIMPairing(): Promise<void> {
  if (fixtureMode) return
  await writeJSON(`${API_ROOT}/settings/airsim/pairing`, 'DELETE', {}, 204)
}

export async function revokeAirSIMDevice(id: string): Promise<void> {
  if (fixtureMode) return
  await writeJSON(
    `${API_ROOT}/settings/airsim/devices/revoke`,
    'POST',
    { id },
    204
  )
}
