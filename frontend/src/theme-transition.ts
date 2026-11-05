type Origin = { x: number; y: number }
type Transition = { ready: Promise<void>; finished: Promise<void>; updateCallbackDone: Promise<void>; skipTransition: () => void }
type Options = { doc?: Document; view?: Window; timeoutMs?: number }

// Keep snapshot setup, animation and cleanup in one bounded lifecycle. The browser
// may skip a transition, suspend a hidden tab or reject ready before the update runs.
export async function revealTheme(update: () => void, origin: Origin, { doc = document, view = window, timeoutMs = 1400 }: Options = {}) {
 const start = (doc as Document & { startViewTransition?: (update: () => void) => Transition }).startViewTransition
 const motion = view.matchMedia('(prefers-reduced-motion: reduce)')
 if (!start || motion.matches || doc.hidden) { update(); return }
 let committed = false
 let stopped = false
 let transition: Transition | undefined
 let animation: Animation | undefined
 let frame: number | undefined
 let interrupt!: () => void
 const interrupted = new Promise<void>(resolve => { interrupt = resolve })
 const commit = () => { if (!committed) { committed = true; update() } }
 const cancelAnimation = () => { const current = animation; animation = undefined; current?.cancel() }
 const stop = () => {
  if (stopped) return
  stopped = true
  transition?.skipTransition()
  cancelAnimation()
  interrupt()
 }
 const visibility = () => { if (doc.hidden) stop() }
 const preference = () => { if (motion.matches) stop() }
 const timer = view.setTimeout(stop, timeoutMs)
 doc.addEventListener('visibilitychange', visibility)
 view.addEventListener('resize', stop)
 motion.addEventListener('change', preference)
 doc.documentElement.classList.add('theme-transition')
 try {
  const run = async () => {
   // Let the preparation styles/button state settle before capturing the old view.
   await new Promise<void>(resolve => { frame = view.requestAnimationFrame(() => resolve()) })
   if (stopped) return
   const x = Math.max(0, Math.min(view.innerWidth, origin.x))
   const y = Math.max(0, Math.min(view.innerHeight, origin.y))
   const radius = Math.ceil(Math.hypot(Math.max(x, view.innerWidth - x), Math.max(y, view.innerHeight - y)))
   transition = start.call(doc, commit)
   // Attach handlers immediately: skipTransition can reject ready while setup is pending.
   void transition.finished.catch(() => {})
   void transition.updateCallbackDone.catch(() => {})
   await transition.ready
   if (stopped) return
   animation = doc.documentElement.animate(
    { clipPath: [`circle(0px at ${x}px ${y}px)`, `circle(${radius}px at ${x}px ${y}px)`] },
    { duration: 420, easing: 'cubic-bezier(.4, 0, .2, 1)', pseudoElement: '::view-transition-new(root)', fill: 'both' },
   )
   await animation.finished
   await transition.finished
  }
  await Promise.race([run(), interrupted])
 } catch {
  stop()
 } finally {
  stopped = true
  view.clearTimeout(timer)
  if (frame !== undefined) view.cancelAnimationFrame(frame)
  doc.removeEventListener('visibilitychange', visibility)
  view.removeEventListener('resize', stop)
  motion.removeEventListener('change', preference)
  transition?.skipTransition()
  cancelAnimation()
  try { commit() } finally { doc.documentElement.classList.remove('theme-transition') }
 }
}
