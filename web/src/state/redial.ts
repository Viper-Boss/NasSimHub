import { reactive } from 'vue'
import { gateway } from '../api/client'
import { normalizeDialTarget } from '../utils/dialTarget'

// Session memory only. After a refresh, read authorized NAS history instead
// of retaining telephone numbers in browser storage.
export const redialNumbers = reactive<Record<string, string>>({})
let generation = 0
const reads = new Map<string, Promise<void>>()

export function redialGeneration(): number { return generation }

export function rememberDialedNumber(lineID: string, number: string, epoch = generation): void {
  if (epoch !== generation || !lineID || normalizeDialTarget(number).error) return
  redialNumbers[lineID] = number.trim()
}

export function resetRedial(): void {
  generation += 1
  reads.clear()
  for (const lineID of Object.keys(redialNumbers)) delete redialNumbers[lineID]
}

export function loadRedialNumber(lineID: string): Promise<void> {
  if (!lineID || redialNumbers[lineID]) return Promise.resolve()
  const pending = reads.get(lineID)
  if (pending) return pending
  const epoch = generation
  const read = (async () => {
    let cursor: string | undefined
    const seen = new Set<string>()
    try {
      do {
        const page = await gateway.listCalls('outgoing', { limit: 100, cursor })
        if (epoch !== generation || redialNumbers[lineID]) return
        const last = page.items.find(call => call.line_id === lineID && call.direction === 'outgoing')
        if (last) {
          rememberDialedNumber(lineID, last.remote_number, epoch)
          return
        }
        cursor = page.meta.has_more ? page.meta.next_cursor : undefined
        if (!cursor || seen.has(cursor)) return
        seen.add(cursor)
      } while (epoch === generation)
    } catch {
      // History being unavailable must never initiate a call or erase a draft.
    }
  })()
  reads.set(lineID, read)
  void read.finally(() => { if (reads.get(lineID) === read) reads.delete(lineID) })
  return read
}
