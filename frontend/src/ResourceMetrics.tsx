import { useQuery } from '@tanstack/react-query'
import { get, type Container, type ResourceSnapshot } from './api'
import { aggregateResources, formatBytes, formatCPU } from './resource-utils'

export function useContainerStats(enabled = true) {
 return useQuery({ queryKey: ['container-stats'], queryFn: () => get<ResourceSnapshot>('/containers/stats'), enabled, staleTime: 3000, retry: false, refetchInterval: 5000, refetchIntervalInBackground: false, refetchOnWindowFocus: true })
}

export function ResourceSummary({ containers, snapshot }: { containers: Container[]; snapshot?: ResourceSnapshot }) {
 const metrics = aggregateResources(containers, snapshot)
 return <dl className="resource-summary" aria-label="资源使用情况">
  <div><dt>CPU</dt><dd>{formatCPU(metrics?.cpu_percent)}</dd></div>
  <div><dt>内存</dt><dd>{formatBytes(metrics?.memory_bytes)}{metrics?.memory_limit ? <small> / {formatBytes(metrics.memory_limit)}</small> : null}</dd></div>
  <div><dt>网络 I/O · 累计收 / 发</dt><dd>{formatBytes(metrics?.network_rx)} / {formatBytes(metrics?.network_tx)}</dd></div>
  <div><dt>磁盘 I/O · 累计读 / 写</dt><dd>{formatBytes(metrics?.block_read)} / {formatBytes(metrics?.block_write)}</dd></div>
  <div><dt>进程 / 线程（PIDs）</dt><dd>{metrics?.pids ?? '—'}{metrics?.partial && <small> · 部分采样</small>}</dd></div>
 </dl>
}
