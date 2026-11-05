import type { Container, ContainerStats, ResourceSnapshot } from './api'

export function formatBytes(value: number | null | undefined): string {
 if (value == null || !Number.isFinite(value) || value < 0) return '—'
 const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
 let number = value, unit = 0
 while (number >= 1024 && unit < units.length - 1) { number /= 1024; unit++ }
 return `${Number(number.toFixed(unit ? 1 : 0))} ${units[unit]}`
}
export function formatCPU(value: number | null | undefined): string {
 return value == null || !Number.isFinite(value) ? '—' : `${value.toFixed(2)}%`
}
export function aggregateResources(containers: Container[], snapshot?: ResourceSnapshot) {
 const active = containers.filter(container => container.state === 'running' || container.state === 'paused')
 const values = active.map(container => snapshot?.containers[container.id]).filter((item): item is ContainerStats => !!item)
 if (!values.length) return null
 const sum = (key: keyof ContainerStats) => {
  const numbers = values.map(item => item[key]).filter((value): value is number => value != null && Number.isFinite(value))
  return numbers.length ? numbers.reduce((total, value) => total + value, 0) : null
 }
 return {
  cpu_percent: sum('cpu_percent'), memory_bytes: sum('memory_bytes'),
  memory_limit: active.length === 1 ? values[0].memory_limit : null,
  memory_percent: active.length === 1 ? values[0].memory_percent : null,
  network_rx: sum('network_rx'), network_tx: sum('network_tx'),
  block_read: sum('block_read'), block_write: sum('block_write'), pids: sum('pids'),
  partial: values.length < active.length || values.some(item => item.cpu_percent == null || item.memory_bytes == null),
  count: values.length, active: active.length,
 }
}
