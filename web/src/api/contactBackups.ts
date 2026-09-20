// Reading the NAS contact backups back.
//
// Writing one lives in `client.ts` next to the rest of the gateway calls, but
// these two are reachable only from the contacts screen, which loads lazily, so
// they stay out of the startup shell -- which is what `get` is exported for.
//
// There is deliberately NO restore call here. The gateway serves the backup and
// the browser imports it through the same path the vCard and Google flows use:
// match each contact against the current ones by phone number, merge numbers
// into a match, refuse an ambiguous multi-match, report what happened. A
// restore IS an import, so it should behave identically, and doing it this way
// adds no second bulk-write path into the contact store.

import { API_ROOT, get } from './client'

export interface ContactBackupEntry {
  filename: string
  /** When the contacts were read, from the name the writer stamped. */
  created_at: string
  size_bytes: number
}

/**
 * GET /api/v1/contacts/backup
 *
 * Newest first, capped by the gateway. An empty list means nothing has been
 * backed up yet, which is not an error.
 */
export async function listContactBackups(): Promise<ContactBackupEntry[]> {
  const payload = (await get(`${API_ROOT}/contacts/backup`)) as {
    backups?: unknown
  }
  const backups = Array.isArray(payload?.backups) ? payload.backups : []
  return backups
    .map(entry => {
      if (!entry || typeof entry !== 'object') return null
      const row = entry as Record<string, unknown>
      if (typeof row.filename !== 'string' || !row.filename) return null
      return {
        filename: row.filename,
        created_at: typeof row.created_at === 'string' ? row.created_at : '',
        size_bytes: typeof row.size_bytes === 'number' ? row.size_bytes : 0
      }
    })
    .filter((entry): entry is ContactBackupEntry => entry !== null)
}

/** One contact as the backup writer stored it. Every field may be missing. */
export interface BackupContact {
  display_name?: string
  avatar?: string
  notes?: string
  phones?: {
    label?: string
    original_number?: string
    canonical_e164?: string
    region?: string
  }[]
}

/**
 * GET /api/v1/contacts/backup/{filename}
 *
 * The format is checked rather than assumed: the directory belongs to the
 * operator and may hold other things, and importing an unknown shape would
 * silently create a pile of nameless contacts.
 */
export async function readContactBackup(filename: string): Promise<BackupContact[]> {
  const payload = (await get(`${API_ROOT}/contacts/backup/${encodeURIComponent(filename)}`)) as {
    format?: unknown
    contacts?: unknown
  }
  if (payload?.format !== 'homesim-contacts-v1') {
    throw new Error('unrecognised backup format')
  }
  return Array.isArray(payload.contacts) ? (payload.contacts as BackupContact[]) : []
}
