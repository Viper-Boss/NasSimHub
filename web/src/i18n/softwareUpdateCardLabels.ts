// The dashboard update card's labels, kept out of the startup locale.
//
// Same reasoning as networkCardNotes beside it: this card renders on one
// screen, in one place, and its text is long enough that carrying it in the
// startup locale would cost every visitor bytes before the app had drawn
// anything. The startup budget has about a kilobyte of headroom, so this is
// not a theoretical concern.
//
// TWO LANGUAGES, AND THE REST FALL BACK TO ENGLISH, which is a decision rather
// than an unfinished job. The operator of this build works in Chinese and the
// product's fallback everywhere else is English; a machine-guessed Japanese or
// Vietnamese rendering of sentences that say what is and is not verified would
// be worse than the English, because these are the strings a customer reads to
// decide whether an update is safe to install. When someone who speaks those
// languages wants them, they are one object each.

export type SoftwareUpdateCardLabels = Record<string, string>

const labels: Record<string, SoftwareUpdateCardLabels> = {
  'zh-CN': {
    title: '版本与更新',
    subtitle: 'VERSION & UPDATE',

    // Auto-download. Not built; the card says so rather than implying it.
    autoDownloadOff: '自动下载未启用',
    autoDownloadHint:
      '目前没有预下载：镜像是在点「立即更新」之后才开始拉取的，所以按钮按下去到重启之间要等。',

    // Headline states.
    availableTitle: '发现新版本',
    availableBody: '点「立即更新」开始拉取镜像并重启相关组件。',
    upToDateTitle: '已是最新版本',
    upToDateBody: '没有可安装的更新。',
    developmentTitle: '本机版本比发布版新',
    developmentBody: '这是开发或自建版本，更新不适用。',
    unavailableTitle: '查不到版本信息',
    unavailableBody: '连不上 GitHub 发布接口。检查网络后重试。',
    checkingTitle: '正在检查更新',

    currentVersion: '当前版本',
    latestVersion: '最新版本',
    notAvailable: '—',

    // Sources.
    sourcesTitle: '更新来源',
    sourceGitHub: 'GitHub',
    sourceDHT: 'DHT',
    sourceNKN: 'NKN',
    sourceCheckedAt: '查询于 {time}',
    sourceNotWired: '未接入',
    sourcesNote: '目前只有 GitHub 一个来源。DHT 和 NKN 查版本还没有做。',

    // Verification. Each line says what is actually checked.
    sourcesNoteShort: '目前只有 GitHub 一个来源。',
    autoDownloadHintShort: '没有预下载：点更新之后才开始拉镜像。',
    verifyTitle: '校验',
    verifyIntegrity: '完整性校验',
    verifyIntegrityValue: '镜像 digest 已钉死',
    verifyIntegrityNote:
      '拉取时带 sha256 digest，由 Docker 自己校验；内容对不上就拉不下来。',
    verifyConsensus: '来源一致性',
    verifyConsensusValue: '单一来源',
    verifyConsensusNote: '只有一个来源，没有可比对的第二个，所以谈不上一致性。',
    verifySignature: '签名验证',
    verifySignatureValue: '未启用',
    verifySignatureNote: '发布物没有加密签名，现在没有任何一步在验签。',

    // Components.
    componentsTitle: '本次涉及的组件',
    componentsNone: '本次不改动任何组件。',
    componentUnchanged: '不变',
    componentTo: '→ {version}',

    // The button and its ring.
    update: '立即更新',
    updating: '更新中',
    updatingHint: '组件逐个拉取并重启，完成后页面会自动刷新。别关电源。',
    recheck: '重新检查',
    checking: '检查中',
    releaseNotes: '发行说明',
    applyUnavailable: '暂时不能安装',
    applyUnavailableNote:
      '能看到新版本，但取不到镜像清单或仓库信息，所以装不了。多半是网络问题，稍后重试。',
    updaterUnavailable: '更新器未运行',
    updaterUnavailableNote: '这套部署没有配更新器组件，只能查看版本，不能从这里安装。',

    // Progress ladder, echoing the About panel's wording.
    stagePending: '等待中',
    stagePulling: '拉取中',
    stageStaged: '已就绪',
    stageRestarting: '重启中',
    stageRollingBack: '回滚中',
    stageRolledBack: '已回滚',
    stageReady: '完成',
    stageFailed: '失败',

    progressLabel: '更新进度',
    progressNote: '进度按组件所处阶段估算，不是下载字节数——后端没有逐字节进度。',
    adminOnly: '只有管理员能从这里安装更新。'
  },

  'en-US': {
    title: 'Version & update',
    subtitle: 'VERSION & UPDATE',

    autoDownloadOff: 'Auto-download off',
    autoDownloadHint:
      'Nothing is pre-downloaded: images are pulled after you press Update, so there is a wait between the button and the restart.',

    availableTitle: 'A new version is available',
    availableBody: 'Update pulls the images and restarts the components that changed.',
    upToDateTitle: 'Up to date',
    upToDateBody: 'There is nothing to install.',
    developmentTitle: 'This build is newer than the latest release',
    developmentBody: 'A development or self-built version. Updates do not apply.',
    unavailableTitle: 'Version information unavailable',
    unavailableBody: 'The GitHub releases endpoint could not be reached. Check the network and retry.',
    checkingTitle: 'Checking for updates',

    currentVersion: 'Current',
    latestVersion: 'Latest',
    notAvailable: '—',

    sourcesTitle: 'Sources',
    sourceGitHub: 'GitHub',
    sourceDHT: 'DHT',
    sourceNKN: 'NKN',
    sourceCheckedAt: 'Checked {time}',
    sourceNotWired: 'Not wired up',
    sourcesNote: 'GitHub is the only source. Version lookup over DHT and NKN has not been built.',

    sourcesNoteShort: 'GitHub is the only source.',
    autoDownloadHintShort: 'Nothing is pre-downloaded; images pull after you press Update.',
    verifyTitle: 'Checks',
    verifyIntegrity: 'Integrity',
    verifyIntegrityValue: 'Image digest pinned',
    verifyIntegrityNote:
      'Images are pulled by sha256 digest and Docker verifies it; content that does not match will not pull.',
    verifyConsensus: 'Source agreement',
    verifyConsensusValue: 'Single source',
    verifyConsensusNote: 'With one source there is no second answer to compare against.',
    verifySignature: 'Signature',
    verifySignatureValue: 'Not enabled',
    verifySignatureNote: 'Releases carry no cryptographic signature, and nothing here verifies one.',

    componentsTitle: 'Components in this update',
    componentsNone: 'No components change.',
    componentUnchanged: 'unchanged',
    componentTo: '→ {version}',

    update: 'Update now',
    updating: 'Updating',
    updatingHint:
      'Components are pulled and restarted one at a time. The page reloads when it finishes. Do not cut power.',
    recheck: 'Check again',
    checking: 'Checking',
    releaseNotes: 'Release notes',
    applyUnavailable: 'Cannot install right now',
    applyUnavailableNote:
      'The release is visible but its image manifest could not be resolved, so it cannot be installed. Usually a network problem; try again shortly.',
    updaterUnavailable: 'Updater not running',
    updaterUnavailableNote:
      'This deployment has no updater component, so versions can be read here but not installed.',

    stagePending: 'Waiting',
    stagePulling: 'Pulling',
    stageStaged: 'Staged',
    stageRestarting: 'Restarting',
    stageRollingBack: 'Rolling back',
    stageRolledBack: 'Rolled back',
    stageReady: 'Done',
    stageFailed: 'Failed',

    progressLabel: 'Update progress',
    progressNote:
      'Progress is estimated from the stage each component is in, not from bytes downloaded — the gateway exposes no byte count.',
    adminOnly: 'Only an administrator can install updates from here.'
  }
}

export function softwareUpdateCardLabels(locale: string): SoftwareUpdateCardLabels {
  // Traditional Chinese reads the simplified text rather than English: it is
  // the same language, and a reader of zh-TW is far better served by zh-CN
  // here than by falling all the way back to English.
  if (locale.startsWith('zh')) return labels['zh-CN']!
  return labels[locale] ?? labels['en-US']!
}
