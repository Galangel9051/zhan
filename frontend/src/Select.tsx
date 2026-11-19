import { Children, isValidElement, useId, useLayoutEffect, useRef, useState, type ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { Check, ChevronDown } from 'lucide-react'
import './select.css'

type Props = { value?: string | number; defaultValue?: string | number; onChange: (event: { target: { value: string } }) => void; children: ReactNode; disabled?: boolean; required?: boolean; 'aria-label'?: string; 'aria-describedby'?: string }
export default function Select({ value: controlledValue, defaultValue = '', onChange, children, disabled, required, 'aria-label': label, 'aria-describedby': describedBy }: Props) {
 const [internalValue, setInternalValue] = useState(defaultValue)
 const value = controlledValue ?? internalValue
 const options = Children.toArray(children).filter(isValidElement).map(child => {
  const p = child.props as { value?: string | number; children: ReactNode; disabled?: boolean }
  const text = Children.toArray(p.children).join('')
  return { value: String(p.value ?? text), label: text, disabled: p.disabled }
 })
 const id = useId(), trigger = useRef<HTMLButtonElement>(null), menu = useRef<HTMLDivElement>(null)
 const [open, setOpen] = useState(false), [active, setActive] = useState(0)
 const [position, setPosition] = useState({ left: 0, top: 0, width: 0, maxHeight: 280 })
 const selected = options.findIndex(o => o.value === String(value))
 const search = useRef({ text: '', time: 0 })
 function show() { if (trigger.current?.matches(':disabled')) return; setActive(Math.max(0, selected)); setOpen(true) }
 function choose(index: number) { const o = options[index]; if (!o || o.disabled || trigger.current?.matches(':disabled')) return; setInternalValue(o.value); onChange({ target: { value: o.value } }); setOpen(false); trigger.current?.focus() }
 useLayoutEffect(() => {
  if (!open) return
  function place() {
   const r = trigger.current!.getBoundingClientRect(), below = innerHeight - r.bottom - 8, above = r.top - 8
   const height = Math.min(280, Math.max(above, below) - 8), width = Math.min(r.width, innerWidth - 16)
   setPosition({ left: Math.max(8, Math.min(r.left, innerWidth - width - 8)), top: below >= Math.min(280, options.length * 38 + 8) || below >= above ? r.bottom + 6 : Math.max(8, r.top - Math.min(height, options.length * 38 + 8) - 6), width, maxHeight: height })
  }
  function outside(e: PointerEvent) { if (!trigger.current?.contains(e.target as Node) && !menu.current?.contains(e.target as Node)) setOpen(false) }
  place(); document.addEventListener('pointerdown', outside); window.addEventListener('resize', place); window.addEventListener('scroll', place, true)
  return () => { document.removeEventListener('pointerdown', outside); window.removeEventListener('resize', place); window.removeEventListener('scroll', place, true) }
 }, [open, options.length])
 useLayoutEffect(() => { if (open) document.getElementById(`${id}-${active}`)?.scrollIntoView({ block: 'nearest' }) }, [open, active, id])
 return <span className="custom-select"><button ref={trigger} type="button" role="combobox" aria-label={label} aria-describedby={describedBy} aria-expanded={open} aria-controls={open ? id : undefined} aria-activedescendant={open ? `${id}-${active}` : undefined} aria-haspopup="listbox" aria-required={required} disabled={disabled} className="select-trigger" onClick={e => { e.preventDefault(); if (open) setOpen(false); else show() }} onKeyDown={e => {
  if (e.key === 'Escape' && open) { e.preventDefault(); e.stopPropagation(); setOpen(false); return }
  if (e.key === 'Tab') { setOpen(false); return }
  if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) {
   e.preventDefault(); if (!open) { show(); return }
   const enabled = options.map((o, i) => o.disabled ? -1 : i).filter(i => i >= 0), index = enabled.indexOf(active)
   if (!enabled.length) return
   setActive(e.key === 'Home' ? enabled[0] : e.key === 'End' ? enabled.at(-1)! : enabled[(index + (e.key === 'ArrowDown' ? 1 : -1) + enabled.length) % enabled.length]); return
  }
  if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); if (open) choose(active); else show(); return }
  if (e.key.length === 1 && !e.ctrlKey && !e.metaKey) { e.preventDefault(); const now = Date.now(); search.current = { text: (now - search.current.time < 600 ? search.current.text : '') + e.key.toLowerCase(), time: now }; const i = options.findIndex(o => !o.disabled && o.label.toLowerCase().startsWith(search.current.text)); if (i >= 0) { setActive(i); setOpen(true) } }
 }}><span>{options[selected]?.label || '请选择'}</span><ChevronDown size={16} aria-hidden="true" /></button>
 {required && <input className="select-validation" tabIndex={-1} aria-hidden="true" required disabled={disabled} value={value} onChange={() => {}} onInvalid={e => { e.preventDefault(); trigger.current?.focus(); show() }} />}
 {open && createPortal(<div ref={menu} id={id} role="listbox" aria-label={label} className="select-menu" style={position} onClick={e => e.stopPropagation()}>{options.map((o, i) => <button type="button" key={o.value} id={`${id}-${i}`} role="option" aria-selected={String(value) === o.value} disabled={o.disabled} tabIndex={-1} className={`select-option ${active === i ? 'is-active' : ''}`} onPointerMove={() => { if (!o.disabled) setActive(i) }} onMouseDown={e => e.preventDefault()} onClick={e => { e.preventDefault(); choose(i) }}><span>{o.label}</span>{String(value) === o.value && <Check size={15} />}</button>)}</div>, document.body)}
 </span>
}
