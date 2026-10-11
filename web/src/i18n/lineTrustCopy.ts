import type { LineSummary } from '../api/types'

export function lineTrustReminder(notice: LineSummary['trust_notice'], locale: string): string {
 if (!notice || !notice.enforced || notice.outgoing_allowed || notice.state === 'TRUSTED') return ''
 const zh = locale.toLowerCase().startsWith('zh')
 if (notice.state === 'OBSERVATION') {
  const hours = Math.ceil(notice.remaining_seconds / 3600)
  return zh ? `设备处于观察期：新配对或换卡会触发，期间可接收电话和短信，暂不能拨号、发短信。${hours > 0 ? `约剩 ${hours} 小时。` : ''}详情见“系统 → 安全中心”。`
   : `This device is in observation after pairing or a SIM change. Incoming calls and SMS remain available; outgoing calls and SMS are blocked.${hours > 0 ? ` About ${hours} hours remain.` : ''} See System → Security centre.`
 }
 if (notice.state === 'RESTRICTED') return zh ? '设备已限制外发：暂不能拨号或发短信。请在“系统 → 安全中心”查看原因及恢复选项。' : 'Outgoing calls and SMS are restricted. See System → Security centre for the reason and recovery options.'
 if (notice.state === 'QUARANTINE') return zh ? '设备已隔离，电话和短信暂不可用。请在“系统 → 安全中心”检查并恢复设备。' : 'This device is quarantined. Calls and SMS are unavailable. Review it in System → Security centre.'
 return zh ? '设备尚未建立使用信任，请在“SIM 节点”完成配对并查看安全状态。' : 'Device trust is not established. Complete pairing and check security in SIM nodes.'
}
