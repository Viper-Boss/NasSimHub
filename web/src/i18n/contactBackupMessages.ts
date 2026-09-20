// Copy for the contact backup RESTORE, kept out of the startup bundle.
//
// `homesimData.ts`, where the backup half's strings live, is imported by every
// locale chunk, which means the bundler hoists it into the app shell: measured,
// eleven strings in two languages added 425 bytes to the payload every visitor
// downloads before the app has drawn anything. The restore is reachable from
// one panel on one screen, so its copy loads with that panel instead -- the
// same arrangement the DHT and advanced-network panels use.
//
// Two languages, matching the rest of the `homesim` namespace, which is
// English and Chinese and falls back to English elsewhere. Adding a third here
// while the backup half beside it has two would read as a bug, not a feature.

export type ContactBackupMessages = Record<string, string>

const messages: Record<string, ContactBackupMessages> = {
  'en-US': {
    // Worded to be exact about what a restore does and does not touch: one
    // that might delete what you have is a different and much scarier
    // operation, and this one never does.
    restoreTitle: 'Restore from a backup',
    restoreHint:
      'Reads a backup from the NAS and imports it, matching each contact against the ones ' +
      'you have by phone number. Nothing you already have is deleted.',
    restoreNow: 'Restore',
    restoreBusy: 'Restoring…',
    restoreChoose: 'Choose a backup',
    restoreNone: 'No backups yet',
    restoreConfirmTitle: 'Restore from this backup?',
    restoreConfirmMessage:
      'Contacts in {filename} will be matched by phone number and imported. Existing ' +
      'contacts gain any numbers they are missing; none are deleted.',
    restoreFailed: 'Could not restore',
    restoreEmpty: 'That backup has no contacts in it',
    restoreListFailed: 'Could not list the backups'
  },
  'zh-CN': {
    restoreTitle: '从备份恢复',
    restoreHint: '读取 NAS 上的备份并导入，按手机号与现有联系人比对。不会删除任何现有联系人。',
    restoreNow: '恢复',
    restoreBusy: '正在恢复…',
    restoreChoose: '选择备份',
    restoreNone: '还没有备份',
    restoreConfirmTitle: '从这个备份恢复？',
    restoreConfirmMessage: '将按手机号比对后导入 {filename}。已有的联系人会补上缺少的号码，不会被删除。',
    restoreFailed: '恢复失败',
    restoreEmpty: '这个备份里没有联系人',
    restoreListFailed: '无法读取备份列表'
  }
}

/** Traditional Chinese reads the simplified copy, as the rest of this namespace does. */
export function contactBackupMessages(locale: string): ContactBackupMessages {
  if (locale.startsWith('zh')) return messages['zh-CN']!
  return messages['en-US']!
}
