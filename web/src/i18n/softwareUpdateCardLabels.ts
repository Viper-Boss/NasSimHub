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
    upToDateTitle: '已是最新版本',
    upToDateBody: '没有可安装的更新。',
    developmentTitle: '本机版本比发布版新',
    developmentBody: '这是开发或自建版本，更新不适用。',
    unavailableTitle: '查不到版本信息',
    unavailableBody: '所有发布镜像源都没连上，或者没有一个返回通过签名验证的版本。检查网络后重试。',
    checkingTitle: '正在检查更新',
    heroReadyTitle: '新版本已下载，等待安装',
    heroDownloadingTitle: '正在下载新版本',
    heroFailedTitle: '新版本没下载完',
    heroApplyingTitle: '正在安装更新',
    stepSignature: '签名验证',
    stepDownload: '镜像下载',
    stepInstall: '等待安装',
    stepInstalling: '正在安装',
    ringDownloaded: '已下载',
    versionTitle: '版本信息',
    lastChecked: '上次检查 {time}',
    chipLatest: '最新',
    versionChange: '版本',
    channelTitle: '更新通道',
    channelStable: '稳定版',
    channelNote: '网关只看公开发布仓库里的稳定版清单，不会装测试版或未签名的版本。',
    signingKeyTitle: '签名公钥',
    signingKeyNote: '网关验证发布清单用的公钥（前 4 字节）。签发控制台页面顶部显示的指纹和它一致，就说明这台网关认的是你的签名。',
    signingKeyInline: '本机公钥 {fingerprint}。',
    lastUpdateTitle: '上次更新',
    lastUpdateSucceeded: '成功',
    lastUpdateFailed: '失败',
    lastUpdateNone: '暂无记录',
    updateDetails: '更新详情',

    currentVersion: '当前版本',
    notAvailable: '—',

    // Sources.
    sourcesTitle: '更新来源',
    sourceDHT: 'DHT',
    sourceNKN: 'NKN',
    sourceVerified: '已验证',
    sourcePeerVerified: '{count} 台网关',
    sourcePeerSilent: '无应答',
    sourcePeerSilentNote: '问过了，没有网关回答。网络里拿着新版本、又正好在线的网关还少，这很正常；镜像源照常可用。',
    sourcePeerIdle: '未参与',
    sourcePeerIdleNote: '这台网关这次没有通过这个网络询问：网络已关闭，或还没连上。',
    shareOn: '分享中',
    shareOff: '未分享',
    shareHint: '开启时，这台网关会把自己验证过的发布清单交给来问的其他网关（NKN 和 DHT），帮连不上 GitHub 的网关收到更新。清单有发布者签名，别人改不了；每台网关两分钟内最多回答一次。',
    sourceState_unreachable: '连不上',
    sourceState_invalid_signature: '签名不对',
    sourceState_malformed: '格式不对',
    sourcesNone: '这次没有问到镜像源',
    sourcesAgree: '{agreeing}/{total} 个源给出此版本',
    sourcesNote:
      '这些镜像源是同一个公开发布仓库的不同线路。谁的内容都不直接信：只认能用本机内置公钥验证的清单，并且取所有镜像里版本最高的那个，所以一个过期或被篡改的镜像拦不住更新。DHT 和 NKN 是其他网关转交的同一份签名清单，同样要验签、同样按版本比高低。',

    // Verification. Each line is lit only by what this check reported.
    verifyTitle: '校验',
    verifyIntegrity: '镜像摘要',
    verifyIntegrityValue: '已钉死',
    verifyIntegrityNote:
      '拉取时带 sha256 digest，由 Docker 自己校验；内容对不上就拉不下来。',
    verifyConsensus: '来源一致',
    verifyConsensusNote:
      '至少两个镜像源给出同一个已验证版本时才亮。它们都来自同一个仓库，所以这一行说明的是线路之间没有分歧，可信与否靠的是下面的签名。',
    verifySignature: '签名验证',
    verifySignatureValue: '通过',
    verifySignatureNone: '未验证',
    verifyRollback: '失败回滚',
    verifyRollbackValue: '自动',
    verifyRollbackNone: '不可用',
    verifyRollbackNote: '安装后组件起不来时，更新器会自动换回原来的镜像再启动。这套部署没有更新器时，这一项不亮。',
    verifySignatureNote:
      '发布清单用离线私钥签名，网关用编译进程序的公钥验证；验不过的清单直接丢弃，不会显示成可更新。',

    // Components.
    componentsTitle: '变更组件',
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
    progressNote: '进度按组件所处阶段估算，不是下载字节数——后端没有逐字节进度。'
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
    upToDateTitle: 'Up to date',
    upToDateBody: 'There is nothing to install.',
    developmentTitle: 'This build is newer than the latest release',
    developmentBody: 'A development or self-built version. Updates do not apply.',
    unavailableTitle: 'Version information unavailable',
    unavailableBody:
      'No release mirror could be reached, or none returned a release that verified. Check the network and retry.',
    checkingTitle: 'Checking for updates',
    heroReadyTitle: 'Downloaded, ready to install',
    heroDownloadingTitle: 'Downloading the new version',
    heroFailedTitle: 'The download did not finish',
    heroApplyingTitle: 'Installing the update',
    stepSignature: 'Signature',
    stepDownload: 'Images',
    stepInstall: 'Ready to install',
    stepInstalling: 'Installing',
    ringDownloaded: 'ready',
    versionTitle: 'Version',
    lastChecked: 'Checked {time}',
    chipLatest: 'Latest',
    versionChange: 'Version',
    channelTitle: 'Channel',
    channelStable: 'Stable',
    channelNote: 'The gateway reads only the stable manifest in the public release repository; it never installs a test build or anything unsigned.',
    signingKeyTitle: 'Signing key',
    signingKeyNote: 'The key this gateway verifies release manifests with (first four bytes). When the signing console shows the same fingerprint at the top of its page, this gateway accepts what you sign.',
    signingKeyInline: 'This gateway\'s key: {fingerprint}.',
    lastUpdateTitle: 'Last update',
    lastUpdateSucceeded: 'Succeeded',
    lastUpdateFailed: 'Failed',
    lastUpdateNone: 'None yet',
    updateDetails: 'Details',

    currentVersion: 'Current',
    notAvailable: '—',

    sourcesTitle: 'Sources',
    sourceDHT: 'DHT',
    sourceNKN: 'NKN',
    sourceVerified: 'Verified',
    sourcePeerVerified: '{count} gateways',
    sourcePeerSilent: 'No answer',
    sourcePeerSilentNote: 'Asked, and no gateway answered. Few gateways yet hold a new release and happen to be online, so this is normal; the mirrors work as before.',
    sourcePeerIdle: 'Not taking part',
    sourcePeerIdleNote: 'This gateway did not ask over this network this time: the network is switched off, or not connected yet.',
    shareOn: 'Sharing',
    shareOff: 'Not sharing',
    shareHint: 'While on, this gateway hands the release manifest it verified to other gateways that ask over NKN and the DHT, so gateways that cannot reach GitHub still get updates. The manifest is signed by the publisher and cannot be altered; each gateway is answered at most once every two minutes.',
    sourceState_unreachable: 'Unreachable',
    sourceState_invalid_signature: 'Bad signature',
    sourceState_malformed: 'Malformed',
    sourcesNone: 'No mirror asked this time',
    sourcesAgree: '{agreeing}/{total} returned this release',
    sourcesNote:
      'The mirrors are different routes to one public release repository. None is trusted as such: only a manifest that verifies against the key built into this gateway counts, and the highest version across all mirrors wins, so a stale or tampered mirror cannot hold an update back. DHT and NKN carry the same signed manifest handed on by other gateways, verified and compared in exactly the same way.',

    verifyTitle: 'Checks',
    verifyIntegrity: 'Digest',
    verifyIntegrityValue: 'Pinned',
    verifyIntegrityNote:
      'Images are pulled by sha256 digest and Docker verifies it; content that does not match will not pull.',
    verifyConsensus: 'Agreement',
    verifyConsensusNote:
      'Lit when at least two mirrors returned the same verified release. They all serve one repository, so this shows the routes agree; trust comes from the signature below.',
    verifySignature: 'Signature',
    verifySignatureValue: 'Verified',
    verifySignatureNone: 'Not verified',
    verifyRollback: 'Rollback',
    verifyRollbackValue: 'Auto',
    verifyRollbackNone: 'None',
    verifyRollbackNote: 'If a component does not come back after installing, the updater puts the previous images back and starts them again. Dark when this deployment has no updater.',
    verifySignatureNote:
      'Release manifests are signed with an offline key and verified against the public key compiled into this build. A manifest that does not verify is discarded and never offered.',

    componentsTitle: 'Changing',
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
    recheck: 'Recheck',
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
      'Progress is estimated from the stage each component is in, not from bytes downloaded — the gateway exposes no byte count.'
  }
}

export function softwareUpdateCardLabels(locale: string): SoftwareUpdateCardLabels {
  // Traditional Chinese reads the simplified text rather than English: it is
  // the same language, and a reader of zh-TW is far better served by zh-CN
  // here than by falling all the way back to English.
  if (locale.startsWith('zh')) return labels['zh-CN']!
  return labels[locale] ?? labels['en-US']!
}
