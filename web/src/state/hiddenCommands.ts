import { i18n, resolvedLocale, translate } from '../i18n'
import { showFeedback } from './feedback'
import { sessionState } from './session'
import { revealAdvancedNetwork } from './advancedNetwork'
import { unlockDHT } from './dhtUnlock'
import { unlockNknMenu } from './nknUnlock'
import { unlockWireGuardChart } from './wireguardUnlock'

// The hidden commands, in one place.
//
// They used to be four `if` branches in the dial pad plus a keyboard listener
// of their own, and the inconsistencies that grew between them were not
// cosmetic:
//
//   TWO COMMANDS OWNED THE SAME WORD. `editNumber` matched "wireguard" for
//   administrators, revealed the advanced-network settings and cleared the
//   field before returning -- so the watch that reveals the home page's
//   WireGuard traffic card never saw the phrase, and the card could not be
//   unlocked from the dial pad by exactly the people most likely to want it.
//   Measured: typing w-i-r-e-g-u-a-r-d character by character, the field held
//   "wireguar" and then emptied, with the chart's flag still unset.
//
//   TWO OF THE FIVE SAID NOTHING when they fired, and the two that did spoke
//   in different places for different lengths of time: an inline note in the
//   dial pad for 4 seconds, another for 5.
//
//   ONE WORKED FROM ANYWHERE and the rest only from the dial pad, with no rule
//   saying which was which.
//
// So: one list, one way to match, one confirmation. A command names what it
// reveals and where it appears, because a reveal nobody can find reads exactly
// like a reveal that did not happen.

export interface HiddenCommand {
  /** Stable name, for tests and for talking about them. */
  id: string
  /** What the operator types. Compared against the whole trimmed entry. */
  phrase: string
  /** Applies the reveal. Idempotent. Returns the message key to confirm with. */
  run(): string
  /**
   * True for a command that changes what the GATEWAY does rather than what this
   * browser shows. Those are not offered to the page-wide listener: a reveal
   * typed by accident costs a panel appearing, and this costs a safety limit.
   */
  serverSide?: boolean
}

export const HIDDEN_COMMANDS: readonly HiddenCommand[] = [
  {
    id: 'wireguard',
    phrase: 'wireguard',
    run() {
      // One word, one feature area, both of its surfaces. The card is for
      // everyone; the settings panel is an administrator's, so a member gets
      // the card alone and is told so rather than being promised a panel that
      // will not be in their menu.
      unlockWireGuardChart()
      if (sessionState.role === 'admin') {
        revealAdvancedNetwork()
        return 'wireguardAll'
      }
      return 'wireguardCard'
    }
  },
  {
    id: 'dht',
    phrase: 'dht',
    run() {
      unlockDHT()
      return 'dht'
    }
  },
  {
    id: 'nkn',
    phrase: 'nkn',
    run() {
      unlockNknMenu()
      return 'nkn'
    }
  }
]

/**
 * The command for an entry, or undefined.
 *
 * The whole entry has to BE the phrase. The old branches used `includes`,
 * which meant any text that happened to contain "dht" or "nkn" -- a contact
 * name typed into the dial pad's search, say -- silently changed what the app
 * showed. An exact match is also the only rule that can be explained in one
 * sentence, which matters for a gesture with no menu entry.
 */
export function matchHiddenCommand(entry: string): HiddenCommand | undefined {
  const normalized = entry.trim().toLowerCase()
  if (!normalized) return undefined
  return HIDDEN_COMMANDS.find(command => command.phrase === normalized)
}

/** The longest phrase, which is how much typing the page listener remembers. */
export const LONGEST_PHRASE = HIDDEN_COMMANDS.reduce(
  (longest, command) => Math.max(longest, command.phrase.length),
  0
)

/**
 * Runs a command and confirms it.
 *
 * The confirmation goes through the app's own feedback surface rather than a
 * note inside one panel, so it reads the same wherever the phrase was typed --
 * and so it is visible at all when the phrase was typed on a page that has no
 * dial pad open.
 */
export function runHiddenCommand(command: HiddenCommand): void {
  const key = command.run()
  void import('../i18n/hiddenCommandMessages')
    .then(({ hiddenCommandMessages }) => {
      const locale = resolvedLocale()
      i18n.global.mergeLocaleMessage(locale, {
        hiddenCommands: hiddenCommandMessages(locale)
      })
      showFeedback(translate(`hiddenCommands.${key}`))
    })
    .catch(() => {
      // The reveal already happened; only its sentence is missing. Saying
      // nothing is better than saying a raw key name.
    })
}

// ---------------------------------------------------------------------------
// Typing the phrase on the page
// ---------------------------------------------------------------------------

let typed = ''
let listening = false

function onKeydown(event: KeyboardEvent): void {
  // Never consume keystrokes aimed at a field. Someone typing "wireguard" into
  // a search box or a peer name means it as text, not as a gesture.
  const target = event.target as HTMLElement | null
  if (target) {
    const tag = target.tagName
    if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || target.isContentEditable) return
  }
  // Modified keys are shortcuts, not typing.
  if (event.ctrlKey || event.metaKey || event.altKey) return
  if (event.key.length !== 1) return

  typed = (typed + event.key.toLowerCase()).slice(-LONGEST_PHRASE)
  // Longest first, so "wireguard" is not shadowed by a shorter phrase that
  // happens to end it. None do today; the ordering keeps that a non-issue.
  const match = [...HIDDEN_COMMANDS]
    .filter(command => !command.serverSide)
    .sort((left, right) => right.phrase.length - left.phrase.length)
    .find(command => typed.endsWith(command.phrase))
  if (!match) return
  typed = ''
  runHiddenCommand(match)
}

/**
 * Starts listening for the phrases. Safe to call more than once; the listener
 * is installed on the first call and lives as long as the app does, so the
 * gesture works from any page.
 */
export function listenForHiddenCommands(): void {
  if (listening || typeof window === 'undefined') return
  listening = true
  window.addEventListener('keydown', onKeydown)
}
