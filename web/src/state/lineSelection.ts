import { reactive, shallowReactive, watch } from 'vue'
import type { CommunicationCapabilityName, LineSummary } from '../api/types'
import { isRegisteredNetwork } from '../utils/operatorNetwork'
import {
  bootstrapResource,
  contactForNumber,
  lineKey,
  resolveLine
} from './workspace'

// THE selected line: one piece of state, read by the top bar, the dialer and
// the message composer, and through those by every screen that starts a call
// or a message (calls, recordings, contacts).
//
// The owner's rule: 顶栏、拨号、短信、录音、联系人同一稳定线路。
//
// It holds a line ID and nothing else, because the ID is the stable thing: it
// survives the line's device dropping off and coming back. So when the device
// of the selected line goes offline THE SELECTION STAYS. Nothing here, and
// nothing that reads this, replaces it with whichever line happens to be up --
// least of all a Node's line with the modem on the NAS. The screens show the
// line as offline and disable what cannot be done on it, and the only thing
// that moves the selection is the user choosing another line.
//
// Until the user has chosen, the selection follows the gateway's configured
// default line while it is online. A fresh page skips an offline historical
// default when a live line is available; the selection is then kept.
export const lineSelectionState = reactive({
  selectedLineID: '',
  /** True once the user picked a line in this session; it is then never moved. */
  explicit: false
})

// Every line seen up in this session, by ID. The gateway's catalog lists the
// lines it has persisted, and a line is not necessarily among them (a Node's
// line that has no history yet); without this, such a line going offline would
// look like a line that never existed and the selection would be given up --
// the silent switch again, by another route. Used to NAME an offline line and
// for nothing else: the readings in a remembered record are a past session's.
const seenLines = shallowReactive(new Map<string, LineSummary>())

function liveLine(id: string): LineSummary | undefined {
  return id ? bootstrapResource.data?.lines.find(line => lineKey(line) === id) : undefined
}

function pastLine(id: string): LineSummary | undefined {
  if (!id) return undefined
  return (
    bootstrapResource.data?.line_catalog.find(line => lineKey(line) === id) ?? seenLines.get(id)
  )
}

/** The user chose a line. Empty IDs are ignored: there is no "none" choice. */
export function selectLine(id: string): void {
  const next = id.trim()
  if (!next) return
  lineSelectionState.selectedLineID = next
  lineSelectionState.explicit = true
}

/** The selected line while its device is up; undefined when it is not. */
export function selectedLiveLine(): LineSummary | undefined {
  return liveLine(lineSelectionState.selectedLineID)
}

/** A known line whose device is not up right now. */
export function lineIsOffline(id: string): boolean {
  const key = id.trim()
  if (!key || !bootstrapResource.data || liveLine(key)) return false
  return pastLine(key) !== undefined
}

/**
 * The stable record of `id` when that line is offline, for a selector to list
 * as "selected, device offline" instead of dropping it from the list. Its
 * signal, registration and capabilities are a past session's and must not be
 * drawn as current.
 */
export function offlineLineRecord(id: string): LineSummary | undefined {
  const key = id.trim()
  return lineIsOffline(key) ? pastLine(key) : undefined
}

/**
 * The selected line's record whether or not its device is up: the live line,
 * or its stable record while it is offline. For showing WHICH line is
 * selected. Never another line in its place.
 */
export function selectedLineRecord(): LineSummary | undefined {
  const id = lineSelectionState.selectedLineID
  return liveLine(id) ?? offlineLineRecord(id)
}

/** What a draft (a call about to be placed, a message about to be sent) uses. */
export interface LineChoice {
  /** The chosen line's ID, or '' when nothing names a line. */
  id: string
  /** The live line. Absent when `id` names a line whose device is offline. */
  line?: LineSummary
  /** True when `id` is a known line that is not up right now. */
  offline: boolean
}

/**
 * Which line a new draft starts on.
 *
 * The same order as workspace.resolveLine() -- an explicitly preferred line,
 * the line the draft came from, the contact's preferred line -- and then THE
 * SELECTED LINE instead of going straight to the configured default (which
 * the selection follows until the user picks a line, so nothing changes for
 * someone who never does).
 *
 * Two things differ from resolveLine(), and both are the point:
 *
 *   A named line whose device is OFFLINE ends the search. It is the answer,
 *   marked offline, and the caller shows it disabled with the reason. The
 *   search does not carry on to some other line that happens to be up --
 *   that is how a call meant for a Node's SIM would be placed on the modem
 *   attached to the NAS without anyone having asked for it.
 *
 *   The selected line is the answer whenever it is a line at all, even when
 *   it cannot do what is asked. The caller says so.
 *
 * A named context or contact line stays selected even if it lacks the
 * capability; the action is disabled until the user chooses another SIM. An ID that
 * names no line the gateway or this session has ever listed is skipped too --
 * there is nothing to show for it.
 */
export function resolveLineChoice(
  capability: CommunicationCapabilityName,
  options: {
    contextKey?: string
    preferredLineID?: string
    number?: string
  } = {}
): LineChoice {
  const contact =
    options.preferredLineID || !options.number
      ? undefined
      : contactForNumber(options.number)
  for (const candidate of [
    options.preferredLineID,
    options.contextKey,
    contact?.preferred_line_id
  ]) {
    const id = candidate?.trim()
    if (!id) continue
    const line = liveLine(id)
    if (line) {
      return { id, line, offline: false }
    }
    if (lineIsOffline(id)) return { id, offline: true }
  }
  const selected = lineSelectionState.selectedLineID
  const line = liveLine(selected)
  if (line) return { id: selected, line, offline: false }
  if (lineIsOffline(selected)) return { id: selected, offline: true }
  const fallback = resolveLine(capability)
  return fallback
    ? { id: lineKey(fallback), line: fallback, offline: false }
    : { id: '', offline: false }
}

watch(
  () =>
    [
      bootstrapResource.data?.lines,
      bootstrapResource.data?.line_catalog,
      bootstrapResource.data?.line_settings.default_line_id
    ] as const,
  ([lines, , defaultLineID]) => {
    if (!bootstrapResource.data) {
      // Signed out: the next session starts from its own default, and knows
      // nothing of the lines this one saw.
      lineSelectionState.selectedLineID = ''
      lineSelectionState.explicit = false
      seenLines.clear()
      return
    }
    for (const line of lines || []) {
      const key = lineKey(line)
      if (key) seenLines.set(key, line)
    }
    if (lineSelectionState.explicit) return
    const known = (id: string) => Boolean(id) && (liveLine(id) !== undefined || pastLine(id) !== undefined)
    const configured = (defaultLineID || '').trim()
    // On a fresh page, a historical default must not hide a connected SIM.
    // Once selected, keep it even if it disconnects: no draft changes SIM.
    if (liveLine(configured) || (known(configured) && (
      configured === lineSelectionState.selectedLineID || !(lines || []).length
    ))) {
      lineSelectionState.selectedLineID = configured
      return
    }
    // No usable default. What was taken before is kept for as long as it names
    // a line -- up or not. Re-picking "the first line that is up" on every
    // change is exactly the silent switch this module exists to prevent.
    if (known(lineSelectionState.selectedLineID)) return
    const first = (lines || []).find(line => isRegisteredNetwork(line)) ?? (lines || [])[0]
    lineSelectionState.selectedLineID = first ? lineKey(first) : ''
  },
  // Synchronously, so a reader computing from the same bootstrap change never
  // sees the new lines with the previous answer's selection.
  { immediate: true, flush: 'sync' }
)
