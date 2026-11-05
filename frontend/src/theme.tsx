import { createContext, useContext, useEffect, useRef, useState, type ReactNode, type MouseEvent } from 'react'
import { flushSync } from 'react-dom'
import { Moon, Sun } from 'lucide-react'
import { revealTheme } from './theme-transition'
type Theme = 'light' | 'dark'
type Origin = { x: number; y: number }
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
 useEffect(() => {
  const system = window.matchMedia('(prefers-color-scheme: dark)')
  const sync = () => { if (active.current) return; const value = savedTheme(); const next = value === 'dark' || value === 'light' ? value : system.matches ? 'dark' : 'light'; apply(next); setTheme(next) }
  const onStorage = (event: StorageEvent) => { if (event.key === storageKey || event.key === null) sync() }
  system.addEventListener('change', sync)
  window.addEventListener('storage', onStorage)
  return () => { system.removeEventListener('change', sync); window.removeEventListener('storage', onStorage) }
 }, [])
 async function toggle(origin: Origin) {
  if (active.current) return
  active.current = true
  setSwitching(true)
  const next = theme === 'dark' ? 'light' : 'dark'
  try {
   await revealTheme(() => { flushSync(() => { apply(next); setTheme(next) }) }, origin)
   // Storage is synchronous; keep it out of the browser's snapshot/update callback.
   try { localStorage.setItem(storageKey, next) } catch { /* appearance still changes without storage */ }
  } finally {
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
 return <button type="button" className="theme-toggle" onClick={click} disabled={context.switching} aria-busy={context.switching} aria-label={label} title={label} aria-pressed={dark}>{dark ? <Sun size={16} /> : <Moon size={16} />}<span>{dark ? '明亮模式' : '夜间模式'}</span></button>
}
