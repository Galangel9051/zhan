import { useEffect, useRef, useState, type ReactNode } from 'react'

// Retain the outgoing dialog briefly so close/cancel has an exit transition too.
export function Presence({ children }: { children: ReactNode }) {
 const open = Boolean(children)
 const [present, setPresent] = useState(open)
 const retained = useRef(children)
 const wasOpen = useRef(open)
 const generation = useRef(0)
 // Reopening during the exit animation starts a fresh form/dialog instance.
 if (open && !wasOpen.current) generation.current += 1
 wasOpen.current = open
 if (children) retained.current = children
 useEffect(() => {
  if (children) { setPresent(true); return }
  const delay = window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 160
  const timer = window.setTimeout(() => { setPresent(false); retained.current = null }, delay)
  return () => window.clearTimeout(timer)
 }, [open])
 if (!children && !present) return null
 return <div key={generation.current} className={`presence ${children ? 'is-present' : 'is-leaving'}`} inert={!children}>{children || retained.current}</div>
}

// CSS grid animates intrinsic content height in both directions, without fixed
// height limits or measuring the service list on every Docker refresh.
export function Collapse({ open, children }: { open: boolean; children: ReactNode }) {
 return <div className={`collapse ${open ? 'is-open' : ''}`} aria-hidden={!open} inert={!open}><div className="collapse-inner">{children}</div></div>
}

// Keep the background still while dialogs are open; nested file pickers share the lock.
let dialogLocks = 0
let previousOverflow = ''
export function useDialogScrollLock() {
 useEffect(() => {
  if (dialogLocks++ === 0) { previousOverflow = document.body.style.overflow; document.body.style.overflow = 'hidden' }
  return () => { if (--dialogLocks === 0) document.body.style.overflow = previousOverflow }
 }, [])
}
