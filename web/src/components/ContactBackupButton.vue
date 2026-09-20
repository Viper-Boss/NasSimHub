<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { CloudUpload, LoaderCircle, RotateCcw } from '@lucide/vue'
import { backupContactsToNAS } from '../api/client'
import {
  listContactBackups,
  readContactBackup,
  type BackupContact,
  type ContactBackupEntry
} from '../api/contactBackups'
import { planContactImport, type TransferContact } from '../utils/contactTransfer'
import {
  bootstrapResource,
  contactsResource,
  loadBootstrap,
  loadContacts,
  saveContact
} from '../state/workspace'
import { requestConfirmation } from '../state/confirmation'
import { useI18n } from 'vue-i18n'
import { contactBackupMessages } from '../i18n/contactBackupMessages'
import SelectControl from './SelectControl.vue'

// Backup and restore. The backup half shipped on its own, which made the
// button a one-way door: contacts went to the NAS and there was no way to get
// them back.
//
// The restore is an IMPORT, and it is run through the same path the vCard and
// Google flows use rather than a new one. `planContactImport` matches each
// incoming contact against the ones already here by phone number, merges
// missing numbers into a match, and refuses an ambiguous multi-match instead of
// guessing. So a restore behaves exactly like an import of the same people,
// which is what it is, and there is no bulk-write path in the gateway that only
// this screen exercises.
//
// Nothing is ever deleted. A restore that could remove what you have now is a
// much scarier operation than what the operator asked for, and the copy says so
// rather than leaving the reader to wonder.

const i18n = useI18n()
const { t } = i18n

// Merged synchronously during setup, before the first render: `t()` does not
// track a message merge as a reactive dependency, so a merge from `onMounted`
// only reaches the screen if something else happens to re-render -- and this
// panel sits still until someone presses a button.
watch(
  i18n.locale,
  locale => {
    i18n.mergeLocaleMessage(locale, { homesim: contactBackupMessages(locale) })
  },
  { immediate: true, flush: 'sync' }
)

const busy = ref(false)
const error = ref('')
const receipt = ref('')
const storagePath = ref('')

async function backup(): Promise<void> {
  if (busy.value) return
  busy.value = true
  error.value = ''
  receipt.value = ''
  try {
    const result = await backupContactsToNAS()
    receipt.value = `${t('homesim.backupSaved', { count: result.count })} · ${result.filename}`
    storagePath.value = result.directory
    // The list the restore picker shows is now out of date by exactly one.
    if (backups.value.length) void refreshBackups()
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t('homesim.backupFailed')
  } finally {
    busy.value = false
  }
}

// ---------------------------------------------------------------------------
// Restore
// ---------------------------------------------------------------------------

const backups = ref<ContactBackupEntry[]>([])
const chosen = ref('')
const listing = ref(false)
const listed = ref(false)
const restoring = ref(false)

/**
 * The region a bare national number is read against, resolved the same way the
 * vCard and Google imports resolve it -- from the default line's home country,
 * falling back to any line that reports one.
 *
 * It matters less here than there, because a backup stores the canonical E.164
 * form alongside the original, but a contact backed up before its number could
 * be parsed still arrives as national text, and reading that against the wrong
 * country silently files it under a different number.
 */
const defaultRegion = computed(() => {
  const bootstrap = bootstrapResource.data
  if (!bootstrap) return ''
  const defaultLineID = bootstrap.line_settings.default_line_id
  return (
    bootstrap.lines.find(line => line.id === defaultLineID)?.home_country_iso ||
    bootstrap.lines.find(line => line.home_country_iso)?.home_country_iso ||
    ''
  )
})

function backupLabel(entry: ContactBackupEntry): string {
  const stamp = entry.created_at ? new Date(entry.created_at) : null
  const when = stamp && !Number.isNaN(stamp.getTime())
    ? stamp.toLocaleString()
    : entry.filename
  const size = entry.size_bytes > 0 ? ` · ${Math.max(1, Math.round(entry.size_bytes / 1024))} KB` : ''
  return `${when}${size}`
}

const backupOptions = computed(() =>
  backups.value.map(entry => ({ value: entry.filename, label: backupLabel(entry) }))
)

/** What the picker says before anything has been chosen, which is three states. */
const pickerPlaceholder = computed(() => {
  if (listing.value) return t('common.loading')
  if (listed.value && !backups.value.length) return t('homesim.restoreNone')
  return t('homesim.restoreChoose')
})

async function refreshBackups(): Promise<void> {
  if (listing.value) return
  listing.value = true
  error.value = ''
  try {
    backups.value = await listContactBackups()
    listed.value = true
    if (!backups.value.some(entry => entry.filename === chosen.value)) {
      chosen.value = backups.value[0]?.filename ?? ''
    }
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t('homesim.restoreListFailed')
  } finally {
    listing.value = false
  }
}

/**
 * One stored contact as something the importer understands.
 *
 * The canonical number is preferred and the original is the fallback: the
 * canonical form is what the matcher compares, and a contact backed up before
 * a number could be parsed still has its original text worth importing.
 */
function asTransfer(contact: BackupContact): TransferContact | null {
  const phones = (contact.phones ?? [])
    .map(phone => ({
      label: phone.label ?? '',
      number: (phone.canonical_e164 || phone.original_number || '').trim(),
      region: phone.region
    }))
    .filter(phone => phone.number)
  const name = (contact.display_name ?? '').trim()
  // A contact with neither a name nor a number is not a contact. Importing it
  // would create a blank row that the reader then has to find and delete.
  if (!name && !phones.length) return null
  return {
    display_name: name,
    avatar: contact.avatar,
    notes: contact.notes,
    phones
  }
}

// The listing is fetched once when the panel appears. It is a directory read
// on the NAS, so it is not free -- but the branded picker opens on a click
// rather than on focus, and a picker that shows nothing until it has been
// opened once reads as broken. One read on the contacts screen is the cheaper
// of the two mistakes.
onMounted(() => { void refreshBackups() })

async function restore(): Promise<void> {
  const filename = chosen.value
  if (!filename || restoring.value) return
  const confirmed = await requestConfirmation({
    title: t('homesim.restoreConfirmTitle'),
    message: t('homesim.restoreConfirmMessage', { filename })
  })
  if (!confirmed) return

  restoring.value = true
  error.value = ''
  receipt.value = ''
  try {
    const stored = await readContactBackup(filename)
    const transfers = stored
      .map(asTransfer)
      .filter((entry): entry is TransferContact => entry !== null)
    if (!transfers.length) {
      error.value = t('homesim.restoreEmpty')
      return
    }
    // Matching needs the CURRENT contacts, so they are re-read rather than
    // taken from whatever the list happened to have loaded: a stale list means
    // matching against people who are no longer there.
    await loadBootstrap()
    await loadContacts(true)

    let created = 0
    let updated = 0
    let skipped = 0
    let failed = 0
    for (const transfer of transfers) {
      const plan = planContactImport(transfer, contactsResource.data, defaultRegion.value)
      if (plan.conflict || !plan.input) {
        skipped += 1
        continue
      }
      try {
        await saveContact(plan.input, plan.existing?.id)
        if (plan.existing) updated += 1
        else created += 1
      } catch {
        failed += 1
      }
    }
    await loadContacts(true)
    receipt.value = t('contactSync.importSummary', { created, updated, skipped, failed })
  } catch (reason) {
    error.value = reason instanceof Error ? reason.message : t('homesim.restoreFailed')
  } finally {
    restoring.value = false
  }
}
</script>

<template>
  <section class="homesim-contact-backup">
    <div>
      <strong>{{ t('homesim.nasBackup') }}</strong>
      <small>{{ t('homesim.backupHint') }}</small>
    </div>
    <button class="secondary-button" type="button" :disabled="busy" @click="backup">
      <LoaderCircle v-if="busy" class="spin" :size="16" />
      <CloudUpload v-else :size="16" />{{ t(busy ? 'homesim.backupBusy' : 'homesim.backupNow') }}
    </button>

    <div class="homesim-contact-backup__restore">
      <div>
        <strong>{{ t('homesim.restoreTitle') }}</strong>
        <small>{{ t('homesim.restoreHint') }}</small>
      </div>
      <div class="homesim-contact-backup__pick">
        <SelectControl
          :model-value="chosen"
          :options="backupOptions"
          :label="t('homesim.restoreChoose')"
          :placeholder="pickerPlaceholder"
          :disabled="listing || restoring"
          compact
          @change="value => (chosen = value)"
        />
        <button
          class="secondary-button"
          type="button"
          :disabled="!chosen || restoring || listing"
          @click="restore"
        >
          <LoaderCircle v-if="restoring" class="spin" :size="16" />
          <RotateCcw v-else :size="16" />{{ t(restoring ? 'homesim.restoreBusy' : 'homesim.restoreNow') }}
        </button>
      </div>
    </div>

    <p v-if="receipt" role="status">{{ receipt }}</p>
    <p v-if="storagePath" role="status">{{ t('homesim.backupStorage', { directory: storagePath }) }}</p>
    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
  </section>
</template>

<style scoped>
.homesim-contact-backup { display: flex; flex-wrap: wrap; gap: 12px; align-items: center; justify-content: space-between; padding: 14px 18px; margin: 4px 12px 12px; background: var(--accent-soft); border: 1px solid var(--border); border-radius: var(--radius-surface); }
.homesim-contact-backup > div { display: grid; gap: 4px; }
.homesim-contact-backup strong { font-size: 14px; }
.homesim-contact-backup small, .homesim-contact-backup p { font-size: 12px; color: var(--muted); }
.homesim-contact-backup p { flex-basis: 100%; overflow-wrap: anywhere; }
.homesim-contact-backup .field-error { color: var(--danger); }

/* The restore sits under the backup, separated by a rule and taking the full
   width: it is the same subject but a different action, and it needs a picker
   beside its button where the backup needs only a button. */
.homesim-contact-backup__restore {
  display: flex;
  flex-basis: 100%;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding-top: 12px;
  border-top: 1px solid var(--border);
}

/* The title/hint pair stacks, the same way the backup's does above it. The
   rule that does that for the backup is `.homesim-contact-backup > div`, and
   this div is a child of the restore block rather than of the panel, so it was
   not matched and the hint ran on from the title with no space between them. */
.homesim-contact-backup__restore > div {
  display: grid;
  flex: 1 1 200px;
  gap: 4px;
}

.homesim-contact-backup__pick {
  display: flex;
  align-items: center;
  gap: 8px;
}

.homesim-contact-backup__pick select {
  min-width: 0;
  max-width: 260px;
  min-height: 36px;
  padding: 6px 10px;
  color: var(--text);
  font: inherit;
  font-size: 12px;
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: var(--radius-control);
}
</style>
