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

    // Auto-download: a switch, reporting what the updater holds.
    autoDownloadOn: '自动下载',
    autoDownloadOff: '自动下载已关',
    autoDownloadHint:
      '开着时，网关一看到新的签名版本就在后台把镜像下好，只下载、不安装。安装永远要你点「立即更新」，那时只需重启几秒。',
    autoDownloadHintShort: '自动下载已关：可以点「现在下载」，或者直接更新（更新时再拉镜像，会久一些）。',

    // Headline states.
    availableTitle: '发现新版本',
    availableBody: '这个版本的签名已用本机内置公钥验证。下载好后点「立即更新」。',
    upToDateTitle: '已是最新版本',
    upToDateBody: '没有可安装的更新。',
    developmentTitle: '本机版本比发布版新',
    developmentBody: '这是开发或自建版本，更新不适用。',
    unavailableTitle: '查不到版本信息',
    unavailableBody: '所有发布镜像源都没连上，或者没有一个返回通过签名验证的版本。检查网络后重试。',
    checkingTitle: '正在检查更新',

    currentVersion: '当前版本',
    latestVersion: '最新版本',
    notAvailable: '—',

    // Sources.
    sourcesTitle: '更新来源',
    sourceDHT: 'DHT',
    sourceNKN: 'NKN',
    sourceNotWired: '未接入',
    sourceVerified: '已验证 {version}',
    sourceState_unreachable: '连不上',
    sourceState_invalid_signature: '签名不对，已拒绝',
    sourceState_malformed: '格式不对，已拒绝',
    sourcesNone: '这次检查没有问到任何镜像源',
    sourcesAgree: '{agreeing}/{total} 个镜像源给出了这个签名版本',
    sourcesNote:
      '这些镜像源是同一个公开发布仓库的不同线路。谁的内容都不直接信：只认能用本机内置公钥验证的清单，并且取所有镜像里版本最高的那个，所以一个过期或被篡改的镜像拦不住更新。DHT 和 NKN 渠道还没做。',

    // Verification. Each line is lit only by what this check reported.
    verifyTitle: '校验',
    verifyIntegrity: '完整性校验',
    verifyIntegrityValue: '镜像 digest 已钉死',
    verifyIntegrityNote:
      '拉取时带 sha256 digest，由 Docker 自己校验；内容对不上就拉不下来。',
    verifyConsensus: '来源一致性',
    verifyConsensusNote:
      '至少两个镜像源给出同一个已验证版本时才亮。它们都来自同一个仓库，所以这一行说明的是线路之间没有分歧，可信与否靠的是下面的签名。',
    verifySignature: '签名验证',
    verifySignatureValue: '已验证（内置公钥）',
    verifySignatureNone: '没有通过验证的版本',
    verifySignatureNote:
      '发布清单用离线私钥签名，网关用编译进程序的公钥验证；验不过的清单直接丢弃，不会显示成可更新。',

    // Components.
    componentsTitle: '本次涉及的组件',
    componentsNone: '本次不改动任何组件。',
    componentUnchanged: '不变',
    componentTo: '→ {version}',

    // The button and its ring.
    update: '立即更新',
    updating: '更新中',
    updatingHint: '组件逐个重启，完成后页面会自动刷新。别关电源。',
    downloading: '下载中',
    downloadNow: '现在下载',
    downloadRetry: '重新下载',
    downloadingHint: '正在后台下载新版本镜像，只下载不安装。下完按钮会变成「立即更新」。',
    downloadReadyHint: '已下载完成。点「立即更新」只需重启相关组件，几秒到一分钟。',
    downloadFailedHint:
      '后台下载没完成（多半是网络问题）。可以点「重新下载」，也可以直接更新——更新时会再拉一次。',
    downloadQueuedHint: '自动下载已开，网关会在后台把镜像下好，完成后这里会提示。',
    downloadProgressLabel: '下载进度',
    downloadStartFailed: '没能开始下载，请稍后重试。',
    preferenceFailed: '设置没保存上，请稍后重试。',
    recheck: '重新检查',
    checking: '检查中',
    releaseNotes: '发行说明',
    applyUnavailable: '暂时不能安装',
    applyUnavailableNote:
      '能看到新版本，但它的签名清单或镜像信息暂时取不到，所以装不了。多半是网络问题，稍后重试。',
    needsIntermediateNote:
      '这个版本要求先升级到一个中间版本才能装（数据格式有变）。请联系提供方获取升级路径。',
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

    autoDownloadOn: 'Auto-download',
    autoDownloadOff: 'Auto-download off',
    autoDownloadHint:
      'While on, the gateway fetches a new signed release in the background as soon as it sees one. It only downloads: installing always waits for you to press Update now, which is then a restart of a few seconds.',
    autoDownloadHintShort:
      'Auto-download is off: press Download now, or update directly (images are then pulled during the update, which takes longer).',

    availableTitle: 'A new version is available',
    availableBody: 'Its signature has been verified against the key built into this gateway. Press Update now once it has downloaded.',
    upToDateTitle: 'Up to date',
    upToDateBody: 'There is nothing to install.',
    developmentTitle: 'This build is newer than the latest release',
    developmentBody: 'A development or self-built version. Updates do not apply.',
    unavailableTitle: 'Version information unavailable',
    unavailableBody:
      'No release mirror could be reached, or none returned a release that verified. Check the network and retry.',
    checkingTitle: 'Checking for updates',

    currentVersion: 'Current',
    latestVersion: 'Latest',
    notAvailable: '—',

    sourcesTitle: 'Sources',
    sourceDHT: 'DHT',
    sourceNKN: 'NKN',
    sourceNotWired: 'Not wired up',
    sourceVerified: 'Verified {version}',
    sourceState_unreachable: 'Unreachable',
    sourceState_invalid_signature: 'Bad signature, rejected',
    sourceState_malformed: 'Malformed, rejected',
    sourcesNone: 'No release mirror was asked on this check',
    sourcesAgree: '{agreeing} of {total} mirrors returned this signed release',
    sourcesNote:
      'The mirrors are different routes to one public release repository. None is trusted as such: only a manifest that verifies against the key built into this gateway counts, and the highest version across all mirrors wins, so a stale or tampered mirror cannot hold an update back. DHT and NKN channels are not built yet.',

    verifyTitle: 'Checks',
    verifyIntegrity: 'Integrity',
    verifyIntegrityValue: 'Image digest pinned',
    verifyIntegrityNote:
      'Images are pulled by sha256 digest and Docker verifies it; content that does not match will not pull.',
    verifyConsensus: 'Source agreement',
    verifyConsensusNote:
      'Lit when at least two mirrors returned the same verified release. They all serve one repository, so this shows the routes agree; trust comes from the signature below.',
    verifySignature: 'Signature',
    verifySignatureValue: 'Verified (built-in key)',
    verifySignatureNone: 'No release verified',
    verifySignatureNote:
      'Release manifests are signed with an offline key and verified against the public key compiled into this build. A manifest that does not verify is discarded and never offered.',

    componentsTitle: 'Components in this update',
    componentsNone: 'No components change.',
    componentUnchanged: 'unchanged',
    componentTo: '→ {version}',

    update: 'Update now',
    updating: 'Updating',
    updatingHint:
      'Components restart one at a time. The page reloads when it finishes. Do not cut power.',
    downloading: 'Downloading',
    downloadNow: 'Download now',
    downloadRetry: 'Retry download',
    downloadingHint:
      'Downloading the new release in the background. Nothing is installed; the button turns into Update now when it is done.',
    downloadReadyHint:
      'Downloaded. Update now only restarts the components that changed — seconds to a minute.',
    downloadFailedHint:
      'The background download did not finish, usually a network problem. Retry it, or update directly — the update pulls again.',
    downloadQueuedHint:
      'Auto-download is on: the gateway fetches the images in the background and this card says when they are ready.',
    downloadProgressLabel: 'Download progress',
    downloadStartFailed: 'The download could not be started. Try again shortly.',
    preferenceFailed: 'The setting was not saved. Try again shortly.',
    recheck: 'Check again',
    checking: 'Checking',
    releaseNotes: 'Release notes',
    applyUnavailable: 'Cannot install right now',
    applyUnavailableNote:
      'The release is visible but its signed manifest or image details could not be fetched, so it cannot be installed. Usually a network problem; try again shortly.',
    needsIntermediateNote:
      'This release has to be reached through an intermediate release first (its data format changed). Ask your provider for the upgrade path.',
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
