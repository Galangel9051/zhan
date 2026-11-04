import { createContext, useContext, useEffect, useRef, useState, type ReactNode, type MouseEvent } from 'react'
import { flushSync } from 'react-dom'
import { Moon, Sun } from 'lucide-react'
type Theme = 'light' | 'dark'
type Origin = { x: number; y: number }
type Transition = { ready: Promise<void>; finished: Promise<void>; updateCallbackDone: Promise<void>; skipTransition: () => void }
const storageKey = 'compose-manager-theme'
const ThemeContext = createContext<{ theme: Theme; switching: boolean; toggle: (origin: Origin) => void } | null>(null)
function savedTheme() { try { return localStorage.getItem(storageKey) } catch { return null } }
function apply(theme: Theme) {
 document.documentElement.dataset.theme = theme
 document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'dark' ? '#101827' : '#f6f8fb')
}
export function ThemeProvider({ children }: { children: ReactNode }) {
 const [theme, setTheme] = useState<Theme>(() => document.documentElement.dataset.theme === 'dark' ? 'dark' : 'light')
 const [switching, setSwitching] = useState(false)
 const active = useRef(false)
 useEffect(() => { apply(theme) }, [theme])
 useEffect(() => {
  const system = window.matchMedia('(prefers-color-scheme: dark)')
  const sync = () => { const value = savedTheme(); setTheme(value === 'dark' || value === 'light' ? value : system.matches ? 'dark' : 'light') }
  const onStorage = (event: StorageEvent) => { if (event.key === storageKey || event.key === null) sync() }
  system.addEventListener('change', sync)
  window.addEventListener('storage', onStorage)
  return () => { system.removeEventListener('change', sync); window.removeEventListener('storage', onStorage) }
 }, [])
 async function toggle(origin: Origin) {
  if (active.current) return
  const next = theme === 'dark' ? 'light' : 'dark'
  let committed = false
  const commit = () => {
   try { localStorage.setItem(storageKey, next) } catch { /* appearance still changes when storage is unavailable */ }
   flushSync(() => { apply(next); setTheme(next) })
   committed = true
  }
  const start = (document as Document & { startViewTransition?: (update: () => void) => Transition }).startViewTransition
  if (!start || window.matchMedia('(prefers-reduced-motion: reduce)').matches) { commit(); return }
  const x = Math.max(0, Math.min(window.innerWidth, origin.x))
  const y = Math.max(0, Math.min(window.innerHeight, origin.y))
  const radius = Math.hypot(Math.max(x, window.innerWidth - x), Math.max(y, window.innerHeight - y))
  active.current = true
  setSwitching(true)
  document.documentElement.classList.add('theme-transition')
  let transition: Transition | undefined
  try {
   transition = start.call(document, commit)
   await transition.ready
   await document.documentElement.animate(
    { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`] },
    { duration: 600, easing: 'cubic-bezier(0.22, 1, 0.36, 1)', pseudoElement: '::view-transition-new(root)', fill: 'both' },
   ).finished
  } catch {
   transition?.skipTransition()
   await transition?.updateCallbackDone.catch(() => {})
   if (!committed) commit()
  } finally {
   await transition?.finished.catch(() => {})
   document.documentElement.classList.remove('theme-transition')
   active.current = false
   setSwitching(false)
  }
 }
 return <ThemeContext.Provider value={{ theme, switching, toggle }}>{children}</ThemeContext.Provider>
}
export function ThemeToggle() {
 const context = useContext(ThemeContext)
 if (!context) throw new Error('ThemeToggle requires ThemeProvider')
 const dark = context.theme === 'dark'
 const label = dark ? '切换到明亮模式' : '切换到夜间模式'
 function click(event: MouseEvent<HTMLButtonElement>) {
  const rect = event.currentTarget.getBoundingClientRect()
  const origin = event.detail === 0 ? { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2 } : { x: event.clientX, y: event.clientY }
  context!.toggle(origin)
 }
 return <button type="button" className="theme-toggle" onClick={click} disabled={context.switching} aria-label={label} title={label} aria-pressed={dark}>{dark ? <Sun size={16} /> : <Moon size={16} />}<span>{dark ? '明亮模式' : '夜间模式'}</span></button>
}
