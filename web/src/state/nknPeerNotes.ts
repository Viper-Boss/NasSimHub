import { reactive, ref } from 'vue'
import { getNKNChatPeerNotes, saveNKNChatPeerNote, type NKNChatPeerNote } from '../api/client'

// Local names for NKN addresses, shared by the chat page and the contacts page
// so a rename on one is visible on the other without a reload. The notes are
// small and change rarely, so one fetch per session is enough; pass
// `{ force: true }` after a write elsewhere.

const notes = reactive(new Map<string, NKNChatPeerNote>())
const loaded = ref(false)
let inFlight: Promise<void> | null = null

export const nknPeerNotes = notes

export function noteForAddress(address: string): NKNChatPeerNote | undefined {
  return notes.get(address)
}

// Every address that points at this contact. A contact can hold more than one
// device, so this is a list rather than a single address.
export function addressesForContact(contactID: string): string[] {
  if (!contactID) return []
  return [...notes.values()]
    .filter(note => note.contact_id === contactID)
    .map(note => note.address)
    .sort()
}

export function peerNotesLoaded(): boolean {
  return loaded.value
}

export async function loadNknPeerNotes(options: { force?: boolean } = {}): Promise<void> {
  if (loaded.value && !options.force) return
  if (inFlight) return inFlight
  inFlight = (async () => {
    try {
      const fetched = await getNKNChatPeerNotes()
      notes.clear()
      for (const note of fetched) notes.set(note.address, note)
      loaded.value = true
    } catch {
      // Names are a convenience. A failure leaves raw addresses on screen
      // rather than taking either page down.
    } finally {
      inFlight = null
    }
  })()
  return inFlight
}

export async function saveNknPeerNote(note: NKNChatPeerNote): Promise<void> {
  const alias = (note.alias ?? '').trim()
  const contactID = (note.contact_id ?? '').trim()
  await saveNKNChatPeerNote({ address: note.address, alias, contact_id: contactID })
  if (alias || contactID) {
    notes.set(note.address, {
      address: note.address,
      alias,
      contact_id: contactID || undefined,
      updated_at: Math.floor(Date.now() / 1000)
    })
  } else {
    notes.delete(note.address)
  }
}
