export function nodeResourceCopy(locale: string) {
  return locale.startsWith('zh')
    ? { cpu: '棒子 CPU', memory: '棒子内存', uptime: '运行时间', processes: '进程', top: '内存占用前 12 个进程', pid: 'PID', name: '名称', rss: '内存', updated: '采样时间' }
    : { cpu: 'Node CPU', memory: 'Node memory', uptime: 'Uptime', processes: 'Processes', top: 'Top 12 processes by memory', pid: 'PID', name: 'Name', rss: 'Memory', updated: 'Sample time' }
}

export function nodeDashboardCopy(locale: string) {
 return locale.startsWith('zh')
  ? { title: '棒子运行状态', missing: '设备未上报资源数据', detailsMissing: '温度、存储和蜂窝详细指标尚未上报', memoryUsage: '内存使用率', dji: '大疆 4G 模组', modem: '4G 模组', global: 'NAS 全局服务' }
  : { title: 'Node resources', missing: 'Resources not reported', detailsMissing: 'Temperature, storage and detailed radio metrics are not reported', memoryUsage: 'Memory usage', dji: 'DJI 4G module', modem: '4G module', global: 'NAS services' }
}