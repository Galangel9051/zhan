import { useLayoutEffect, useRef, useState, type KeyboardEvent, type ReactNode } from 'react'

type Indicator = { x: number; y: number; width: number; height: number; animate: boolean }
type Props<T extends string> = {
 value: T
 items: { value: T; content: ReactNode; label?: string }[]
 onChange: (value: T) => void
 variant: 'sidebar' | 'tabs'
 label: string
 id?: string
}

// One persistent highlight moves between buttons, including during rapid changes.
export default function SlidingNav<T extends string>({ value, items, onChange, variant, label, id }: Props<T>) {
 const root = useRef<HTMLDivElement>(null)
 const [indicator, setIndicator] = useState<Indicator | null>(null)
 const measure = () => {
  const selected = root.current?.querySelector<HTMLButtonElement>('[data-selected="true"]')
  if (!selected) return
  const next = { x: selected.offsetLeft, y: selected.offsetTop, width: selected.offsetWidth, height: selected.offsetHeight }
  setIndicator(previous => previous && previous.x === next.x && previous.y === next.y && previous.width === next.width && previous.height === next.height ? previous : { ...next, animate: previous !== null })
 }
 useLayoutEffect(measure)
 useLayoutEffect(() => {
  const el = root.current
  if (!el) return
  const observer = new ResizeObserver(measure)
  observer.observe(el)
  el.querySelectorAll('button').forEach(button => observer.observe(button))
  return () => observer.disconnect()
 }, [])
 function navigate(e: KeyboardEvent<HTMLButtonElement>, index: number) {
  if (variant !== 'tabs') return
  let next = index
  if (e.key === 'ArrowRight') next = (index + 1) % items.length
  else if (e.key === 'ArrowLeft') next = (index + items.length - 1) % items.length
  else if (e.key === 'Home') next = 0
  else if (e.key === 'End') next = items.length - 1
  else return
  e.preventDefault()
  onChange(items[next].value)
  root.current?.querySelectorAll<HTMLButtonElement>('button')[next]?.focus()
 }
 return <div ref={root} className={`sliding-nav sliding-${variant}`} role={variant === 'tabs' ? 'tablist' : 'navigation'} aria-label={label}>
  <span aria-hidden="true" className="sliding-nav-indicator" style={indicator ? { width: indicator.width, height: indicator.height, transform: `translate3d(${indicator.x}px, ${indicator.y}px, 0)`, transition: indicator.animate ? undefined : 'none' } : { visibility: 'hidden' }} />
  {items.map((item, index) => <button type="button" key={item.value} aria-label={item.label} title={variant === 'sidebar' ? item.label : undefined} className={variant === 'sidebar' ? 'nav-item' : 'sliding-tab'} data-selected={item.value === value} role={variant === 'tabs' ? 'tab' : undefined} aria-selected={variant === 'tabs' ? item.value === value : undefined} aria-current={variant === 'sidebar' && item.value === value ? 'page' : undefined} id={id ? `${id}-tab-${item.value}` : undefined} aria-controls={id ? `${id}-panel-${item.value}` : undefined} tabIndex={variant === 'tabs' && item.value !== value ? -1 : 0} onKeyDown={e => navigate(e, index)} onClick={() => onChange(item.value)}>{item.content}</button>)}
 </div>
}
