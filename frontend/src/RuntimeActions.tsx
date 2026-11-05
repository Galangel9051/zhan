import { useEffect, useId, useLayoutEffect, useRef, useState, type KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
import { ArrowUpRight, Ellipsis, Play, RotateCw, Square, Terminal } from 'lucide-react'
import { runtimeAction } from './runtime-action'

type Props = {
 label: string
 status: string
 onAction: (action: 'start' | 'stop' | 'restart') => void
 onDetails?: () => void
 onLogs?: () => void
 variant?: 'icon' | 'button'
 busy?: boolean
}

export default function RuntimeActions({ label, status, onAction, onDetails, onLogs, variant = 'icon', busy = false }: Props) {
 const [open, setOpen] = useState(false)
 const [position, setPosition] = useState<{ left: number; top: number } | null>(null)
 const trigger = useRef<HTMLButtonElement>(null), menu = useRef<HTMLDivElement>(null)
 const focusLast = useRef(false)
 const menuID = useId()
 const action = runtimeAction(status)
 const actionLabel = action === 'stop' ? '停止' : action === 'start' ? '启动' : '启停'
 const disabled = busy || action === null
 const buttonClass = variant === 'button' ? 'button compact' : 'icon-button'
 const items = [
  { label: '重启', icon: RotateCw, disabled, select: () => onAction('restart') },
  ...(onDetails ? [{ label: '查看详情', icon: ArrowUpRight, disabled: false, select: onDetails }] : []),
 ]

 function close(restoreFocus = false) {
  setOpen(false); setPosition(null)
  if (restoreFocus) trigger.current?.focus({ preventScroll: true })
 }
 useLayoutEffect(() => {
  if (!open || !trigger.current || !menu.current) return
  const anchor = trigger.current.getBoundingClientRect(), rect = menu.current.getBoundingClientRect()
  const top = anchor.bottom + 6 + rect.height <= window.innerHeight - 8 ? anchor.bottom + 6 : Math.max(8, anchor.top - rect.height - 6)
  setPosition({ left: Math.max(8, Math.min(anchor.right - rect.width, window.innerWidth - rect.width - 8)), top })
 }, [open])
 useLayoutEffect(() => {
  if (!open || !position || !menu.current) return
  // Wait until the positioned portal is visible before transferring focus.
  const frame = window.requestAnimationFrame(() => {
   const buttons = menu.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')
   if (buttons) buttons[focusLast.current ? buttons.length - 1 : 0]?.focus({ preventScroll: true })
  })
  return () => window.cancelAnimationFrame(frame)
 }, [open, position])
 useEffect(() => {
  if (!open) return
  function outside(event: PointerEvent) {
   if (!menu.current?.contains(event.target as Node) && !trigger.current?.contains(event.target as Node)) close()
  }
  function dismiss() { close() }
  function scroll(event: Event) { if (!menu.current?.contains(event.target as Node)) close() }
  document.addEventListener('pointerdown', outside)
  window.addEventListener('resize', dismiss)
  window.addEventListener('scroll', scroll, true)
  return () => {
   document.removeEventListener('pointerdown', outside)
   window.removeEventListener('resize', dismiss)
   window.removeEventListener('scroll', scroll, true)
  }
 }, [open])
 function keyboard(event: KeyboardEvent<HTMLDivElement>) {
  if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close(true); return }
  if (event.key === 'Tab') { close(true); return }
  const buttons = Array.from(menu.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])
  if (!buttons.length) return
  const current = buttons.indexOf(document.activeElement as HTMLButtonElement)
  let next: number
  if (event.key === 'ArrowDown') next = (current + 1) % buttons.length
  else if (event.key === 'ArrowUp') next = (current - 1 + buttons.length) % buttons.length
  else if (event.key === 'Home') next = 0
  else if (event.key === 'End') next = buttons.length - 1
  else return
  event.preventDefault(); buttons[next]?.focus({ preventScroll: true })
 }

 return <div className={`row-actions runtime-actions ${onLogs ? 'has-logs' : ''}`}>
  {onLogs && <button type="button" className="icon-button" aria-label={`查看${label}日志`} title="查看日志" onClick={onLogs}><Terminal size={14} /><span className="mobile-action-label">日志</span></button>}
  <button type="button" className={`${buttonClass} runtime-toggle`} aria-label={`${actionLabel}${label}`} title={busy ? '任务正在执行' : action === null ? '状态未知，请刷新后重试' : actionLabel} disabled={disabled} onClick={() => { if (action) onAction(action) }}>
   <span className="runtime-toggle-content" key={actionLabel}>{action === 'stop' ? <Square size={14} /> : <Play size={14} />}<span className={variant === 'icon' ? 'mobile-action-label' : undefined}>{actionLabel}</span></span>
  </button>
  <button ref={trigger} type="button" className={`${buttonClass} runtime-more`} aria-label={`${label}更多操作`} title="更多操作" aria-haspopup="menu" aria-expanded={open} aria-controls={open ? menuID : undefined} onClick={() => { focusLast.current = false; if (open) close(); else setOpen(true) }} onKeyDown={event => { if (event.key === 'Escape' && open) { event.preventDefault(); close(true); return } if (event.key === 'ArrowDown' || event.key === 'ArrowUp') { event.preventDefault(); focusLast.current = event.key === 'ArrowUp'; setOpen(true) } }}><Ellipsis size={17} /><span className={variant === 'icon' ? 'mobile-action-label' : undefined}>更多</span></button>
  {open && createPortal(<div id={menuID} ref={menu} role="menu" aria-label={`${label}操作菜单`} className="runtime-menu" style={position ? { left: position.left, top: position.top } : { left: 0, top: 0, opacity: 0, pointerEvents: 'none' }} onKeyDown={keyboard}>
   {items.map(item => <button key={item.label} type="button" role="menuitem" tabIndex={-1} disabled={item.disabled} onClick={() => { close(true); item.select() }}><item.icon size={15} /><span>{item.label}</span></button>)}
  </div>, document.body)}
 </div>
}
