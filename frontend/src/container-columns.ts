export const columnDefinitions = [
 { key: 'name', label: '名称 / 容器', width: 260, min: 200, max: 600, visible: true, required: true },
 { key: 'status', label: '运行状态', width: 115, min: 100, max: 240, visible: true },
 { key: 'cpu', label: 'CPU', width: 100, min: 85, max: 220, visible: true },
 { key: 'memory', label: '内存占用', width: 165, min: 130, max: 320, visible: true },
 { key: 'ports', label: '端口', width: 175, min: 120, max: 500, visible: true },
 { key: 'git', label: 'Git / 自动更新', width: 165, min: 125, max: 360, visible: true },
 { key: 'last_task', label: '最近任务', width: 110, min: 90, max: 240, visible: true },
 { key: 'image', label: '镜像', width: 210, min: 120, max: 500, visible: false },
 { key: 'network', label: '网络 I/O', width: 175, min: 145, max: 320, visible: false },
 { key: 'disk', label: '磁盘 I/O', width: 175, min: 145, max: 320, visible: false },
 { key: 'pids', label: 'PIDs', width: 85, min: 70, max: 160, visible: false },
 { key: 'id', label: '容器 ID', width: 145, min: 110, max: 360, visible: false },
 { key: 'started', label: '启动时间', width: 175, min: 140, max: 300, visible: true },
 { key: 'restarts', label: '重启次数', width: 95, min: 85, max: 180, visible: false },
 { key: 'actions', label: '操作', width: 95, min: 95, max: 180, visible: true, required: true },
] as const

export type ColumnKey = typeof columnDefinitions[number]['key']
export type ColumnPreference = { key: ColumnKey; width: number; visible: boolean }
export const columnStorageKey = 'zhan-container-columns-v1'
export function defaultColumns(): ColumnPreference[] {
 return columnDefinitions.map(column => ({ key: column.key, width: column.width, visible: column.visible }))
}
export function normalizeColumns(raw: unknown): ColumnPreference[] {
 if (!Array.isArray(raw)) return defaultColumns()
 const result: ColumnPreference[] = [], seen = new Set<string>()
 for (const value of raw) {
  if (!value || typeof value !== 'object' || seen.has(value.key)) continue
  const definition = columnDefinitions.find(column => column.key === value.key)
  if (!definition) continue
  seen.add(value.key)
  result.push({ key: definition.key, width: typeof value.width === 'number' && Number.isFinite(value.width) ? Math.round(Math.max(definition.min, Math.min(definition.max, value.width))) : definition.width, visible: 'required' in definition || typeof value.visible !== 'boolean' ? definition.visible : value.visible })
 }
 for (const column of defaultColumns()) if (!seen.has(column.key)) result.push(column)
 return [result.find(column => column.key === 'name')!, ...result.filter(column => column.key !== 'name' && column.key !== 'actions'), result.find(column => column.key === 'actions')!]
}
