import { useEffect, useRef, useState, type PointerEvent, type ReactNode } from 'react'
import { ArrowDown, ArrowUp, Boxes, ChevronRight, Container as ContainerIcon, RotateCcw, Settings2 } from 'lucide-react'
import { Badge } from './App'
import { date, short, type Container, type Stack } from './api'
import RuntimeActions from './RuntimeActions'
import { useContainerStats } from './ResourceMetrics'
import { aggregateResources, formatBytes, formatCPU } from './resource-utils'
import { columnDefinitions, columnStorageKey, defaultColumns, normalizeColumns, type ColumnKey, type ColumnPreference } from './container-columns'

type Props = { stacks: Stack[]; open: (id: number) => void; action: (stack: Stack, kind: string, service?: string, container?: string) => void }
type TreeRow = { key: string; level: number; kind: 'project' | 'container'; stack: Stack; container?: Container; containers: Container[] }

function readColumns() {
 try { const saved = JSON.parse(localStorage.getItem(columnStorageKey) ?? 'null'); return normalizeColumns(saved?.version === 1 ? saved.columns : null) } catch { return defaultColumns() }
}
function WidthInput({ column, commit }: { column: ColumnPreference; commit: (width: number) => void }) {
 const [value, setValue] = useState(String(column.width))
 useEffect(() => setValue(String(column.width)), [column.width])
 const definition = columnDefinitions.find(item => item.key === column.key)!
 return <input type="number" aria-label={`${definition.label}列宽`} min={definition.min} max={definition.max} value={value} onChange={event => setValue(event.target.value)} onBlur={event => { const text = event.currentTarget.value; const next = Number(text); const width = text && Number.isFinite(next) ? Math.max(definition.min, Math.min(definition.max, Math.round(next))) : column.width; commit(width); setValue(String(width)) }} onKeyDown={event => { if (event.key === 'Enter') event.currentTarget.blur(); if (event.key === 'Escape') { event.currentTarget.value = String(column.width); setValue(String(column.width)); event.currentTarget.blur() } }} />
}

export default function ContainerTable({ stacks, open, action }: Props) {
 const [columns, setColumns] = useState(readColumns), [settings, setSettings] = useState(false), [storageFailed, setStorageFailed] = useState(false)
 const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
 const [resizing, setResizing] = useState(false)
 const drag = useRef<{ key: ColumnKey; start: number; width: number } | null>(null)
 const stats = useContainerStats(stacks.length > 0)
 const snapshot = stats.error ? undefined : stats.data
 useEffect(() => {
  const timer = window.setTimeout(() => { try { localStorage.setItem(columnStorageKey, JSON.stringify({ version: 1, columns })); setStorageFailed(false) } catch { setStorageFailed(true) } }, 300)
  return () => window.clearTimeout(timer)
 }, [columns])
 const visible = columns.filter(column => column.visible)
 const allContainers = stacks.flatMap(stack => stack.services.flatMap(service => service.containers))
 const totals = aggregateResources(allContainers, snapshot)
 const unavailableCount = snapshot ? allContainers.filter(container => (container.state === 'running' || container.state === 'paused') && !snapshot.containers[container.id]).length : 0
 const activeCount = allContainers.filter(container => container.state === 'running' || container.state === 'paused').length
 function updateWidth(key: ColumnKey, width: number) { setColumns(previous => normalizeColumns(previous.map(column => column.key === key ? { ...column, width } : column))) }
 function toggle(key: string) { setExpanded(previous => { const next = new Set(previous); if (next.has(key)) next.delete(key); else next.add(key); return next }) }
 function move(key: ColumnKey, delta: number) {
  setColumns(previous => { const next = [...previous], from = next.findIndex(column => column.key === key), to = from + delta; if (from <= 0 || from >= next.length - 1 || to <= 0 || to >= next.length - 1) return previous; [next[from], next[to]] = [next[to], next[from]]; return next })
 }
 function resizeStart(event: PointerEvent<HTMLSpanElement>, column: ColumnPreference) { event.preventDefault(); drag.current = { key: column.key, start: event.clientX, width: column.width }; setResizing(true); event.currentTarget.setPointerCapture(event.pointerId) }
 function resizeMove(event: PointerEvent<HTMLSpanElement>) { if (drag.current) updateWidth(drag.current.key, drag.current.width + event.clientX - drag.current.start) }
 function resizeEnd() { drag.current = null; setResizing(false) }
 const rows: TreeRow[] = []
 for (const stack of stacks) {
  const key = `project:${stack.id}`
  const containers = stack.services.flatMap(service => service.containers)
  rows.push({ key, level: 0, kind: 'project', stack, containers })
  if (!expanded.has(key)) continue
  for (const container of containers) rows.push({ key: `container:${stack.id}:${container.id}`, level: 1, kind: 'container', stack, container, containers: [container] })
 }
 function cell(row: TreeRow, key: ColumnKey): ReactNode {
  const metrics = aggregateResources(row.containers, snapshot)
  const name = row.container?.name ?? row.stack.name
  const status = row.container?.state ?? row.stack.runtime_status
  const metricTitle = metrics?.partial ? `仅采样到 ${metrics.count} / ${metrics.active} 个运行容器，显示已采样合计` : '来自 Docker 实时采样'
  switch (key) {
   case 'name': {
    const children = row.containers.length
    return <div className="container-tree-name" style={{ paddingLeft: row.level * 18 }}>
     {row.kind !== 'container' ? <button type="button" className="icon-button tree-expander" aria-label={`${expanded.has(row.key) ? '收起' : '展开'} ${name}`} aria-expanded={expanded.has(row.key)} disabled={!children} onClick={() => toggle(row.key)}><ChevronRight size={14} className={`expand-chevron ${expanded.has(row.key) ? 'is-open' : ''}`} /></button> : <span className="tree-expander-space" />}
     {row.kind === 'project' ? <Boxes size={19} /> : <ContainerIcon size={16} />}
     <div className="container-tree-title">{row.kind === 'container' ? <strong title={name}>{name}</strong> : <button type="button" onClick={() => open(row.stack.id)} title={name}>{name}</button>}<small>{row.kind === 'project' ? `${row.containers.length} 个容器 · ${row.stack.project_name}` : short(row.container!.id)}</small></div>
    </div>
   }
   case 'status': return <div className="container-state"><Badge status={status} />{row.container?.health && row.container.health !== 'healthy' && <Badge status={row.container.health} />}{row.kind === 'project' && row.stack.desired_state === 'stopped' && <small className="subtle">自动部署已暂停</small>}{row.kind === 'project' && row.stack.runtime_error && <small className="resource-partial" title={row.stack.runtime_error}>读取状态失败</small>}</div>
   case 'cpu': return <div className="resource-value" title={metricTitle}>{formatCPU(metrics?.cpu_percent)}{metrics?.partial && <small className="resource-partial">部分采样</small>}</div>
   case 'memory': return <div className="resource-value" title={metricTitle}><span>{formatBytes(metrics?.memory_bytes)}</span>{metrics?.memory_limit ? <small>/ {formatBytes(metrics.memory_limit)}</small> : metrics && row.kind !== 'container' ? <small>{metrics.count} 个运行容器合计</small> : null}{metrics?.memory_percent != null && <span className="memory-meter" aria-label={`内存使用 ${metrics.memory_percent.toFixed(1)}%`}><i style={{ width: `${Math.min(100, Math.max(0, metrics.memory_percent))}%` }} /></span>}</div>
   case 'network': return <div className="resource-value" title="容器启动以来累计接收 / 发送字节"><span>{formatBytes(metrics?.network_rx)} / {formatBytes(metrics?.network_tx)}</span><small>接收 / 发送</small></div>
   case 'disk': return <div className="resource-value" title="容器启动以来累计磁盘读取 / 写入字节，并非磁盘空间占用"><span>{formatBytes(metrics?.block_read)} / {formatBytes(metrics?.block_write)}</span><small>读取 / 写入</small></div>
   case 'pids': return metrics?.pids ?? '—'
   case 'ports': { const ports = [...new Set(row.containers.map(container => container.ports).filter(Boolean))].join(', '); return <span className="container-cell-text" title={ports}>{ports || '—'}</span> }
   case 'image': { const images = row.container ? row.container.image : [...new Set(row.containers.map(container => container.image).filter(Boolean))].join(', '); return <code className="container-cell-text" title={images}>{images || '—'}</code> }
   case 'id': return row.container ? <code title={row.container.id}>{short(row.container.id)}</code> : '—'
   case 'started': return row.container?.started_at && !row.container.started_at.startsWith('0001-') ? <span title={row.container.started_at}>{date(row.container.started_at)}</span> : '—'
   case 'restarts': return row.container?.restart_count ?? '—'
   case 'git': return row.kind === 'project' ? <div className="resource-value"><span className="container-cell-text" title={row.stack.branch}>{row.stack.branch || '未绑定'}</span><small>{row.stack.polling_enabled ? `${row.stack.poll_interval}s · ${row.stack.auto_deploy ? '自动部署' : '仅检查'}` : '手动更新'}</small></div> : '—'
   case 'last_task': return row.kind === 'project' && row.stack.last_task ? <Badge status={row.stack.last_task.status} /> : '—'
   case 'actions': return <RuntimeActions label={`${row.kind === 'project' ? '项目' : '容器'} ${name}`} status={status} onAction={kind => action(row.stack, kind, undefined, row.container?.id)} onDetails={row.kind === 'project' ? () => open(row.stack.id) : undefined} />
  }
 }
 return <div className={`container-table-workspace ${resizing ? 'is-resizing' : ''}`}>
  <div className="container-view-toolbar"><div className="container-resource-total"><span><i className={stats.error ? 'sample-dot is-error' : 'sample-dot'} />{activeCount} 个运行容器</span><span>CPU <strong>{formatCPU(totals?.cpu_percent)}</strong></span><span>内存 <strong>{formatBytes(totals?.memory_bytes)}</strong></span>{stats.isFetching && !stats.data && <span>正在采样…</span>}<small>{stats.error ? '资源采样暂不可用，启停操作仍可使用' : snapshot ? `${new Date(snapshot.sampled_at).toLocaleTimeString('zh-CN')} 更新 · 每 5 秒采样` : '停止或未采样显示 —'}{!stats.error && !!unavailableCount && ` · ${unavailableCount} 个容器暂未采样`}</small></div><button type="button" className="button compact" aria-expanded={settings} aria-controls="container-column-settings" onClick={() => setSettings(value => !value)}><Settings2 size={14} />列设置</button></div>
  {settings && <section id="container-column-settings" className="container-column-settings motion-enter" aria-label="自定义表头"><div className="column-settings-heading"><div><strong>自定义列</strong><p>{storageFailed ? '当前浏览器不允许保存，设置仅在本次使用期间有效。' : '拖拽表头分隔线调整宽度；双击恢复该列默认宽度。设置保存在当前浏览器。'}</p></div><button type="button" className="text-button" onClick={() => setColumns(defaultColumns())}><RotateCcw size={13} />恢复默认</button></div><div className="column-settings-grid">{columns.map((column, index) => { const definition = columnDefinitions.find(item => item.key === column.key)!, fixed = column.key === 'name' || column.key === 'actions'; return <div className="column-setting" key={column.key}><label className="checkbox-label"><input type="checkbox" checked={column.visible} disabled={fixed} onChange={event => setColumns(previous => previous.map(item => item.key === column.key ? { ...item, visible: event.target.checked } : item))} />{definition.label}</label><div className="column-width-setting"><WidthInput column={column} commit={width => updateWidth(column.key, width)} /><span>px</span></div><button type="button" className="icon-button" aria-label={`前移${definition.label}列`} disabled={fixed || index <= 1} onClick={() => move(column.key, -1)}><ArrowUp size={13} /></button><button type="button" className="icon-button" aria-label={`后移${definition.label}列`} disabled={fixed || index >= columns.length - 2} onClick={() => move(column.key, 1)}><ArrowDown size={13} /></button></div> })}</div></section>}
  <div className="container-table-scroll"><table className="container-data-table" style={{ width: visible.reduce((width, column) => width + column.width, 0) }}><colgroup>{visible.map(column => <col key={column.key} style={{ width: column.width }} />)}</colgroup><thead><tr>{visible.map(column => { const definition = columnDefinitions.find(item => item.key === column.key)!; return <th key={column.key} scope="col" data-column={column.key}><span>{definition.label}</span><span role="separator" tabIndex={0} aria-label={`调整${definition.label}列宽`} aria-orientation="vertical" aria-valuemin={definition.min} aria-valuemax={definition.max} aria-valuenow={column.width} className="column-resize-handle" title="拖拽调整列宽，双击恢复默认；方向键调整" onPointerDown={event => resizeStart(event, column)} onPointerMove={resizeMove} onPointerUp={resizeEnd} onLostPointerCapture={resizeEnd} onDoubleClick={() => updateWidth(column.key, definition.width)} onKeyDown={event => { if (event.key === 'ArrowLeft' || event.key === 'ArrowRight') { event.preventDefault(); updateWidth(column.key, column.width + (event.key === 'ArrowRight' ? 10 : -10)) } if (event.key === 'Home' || event.key === 'End') { event.preventDefault(); updateWidth(column.key, event.key === 'Home' ? definition.min : definition.max) } }} /></th> })}</tr></thead><tbody>{rows.map(row => <tr key={row.key} className={`container-tree-row level-${row.level}`} data-kind={row.kind}>{visible.map(column => <td key={column.key} data-column={column.key} data-label={columnDefinitions.find(item => item.key === column.key)!.label}>{cell(row, column.key)}</td>)}</tr>)}{!rows.length && <tr><td colSpan={visible.length} className="container-table-empty">没有匹配的项目，可更换搜索条件或导入项目。</td></tr>}</tbody></table></div>
 </div>
}
