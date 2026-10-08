export function nodeResourceCopy(locale: string) {
  return locale.startsWith('zh')
    ? { cpu: '棒子 CPU', memory: '棒子内存', uptime: '运行时间', processes: '进程', top: '内存占用前 12 个进程', pid: 'PID', name: '名称', rss: '内存', updated: '采样时间' }
    : { cpu: 'Node CPU', memory: 'Node memory', uptime: 'Uptime', processes: 'Processes', top: 'Top 12 processes by memory', pid: 'PID', name: 'Name', rss: 'Memory', updated: 'Sample time' }
}
