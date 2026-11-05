import test from 'node:test'
import assert from 'node:assert/strict'
import { revealTheme } from '../src/theme-transition.ts'

function deferred() { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
function fixture() {
 const doc = new EventTarget(), view = new EventTarget(), motion = new EventTarget()
 const ready = deferred(), finished = deferred(), animationFinished = deferred(), updates = deferred()
 const classes = new Set()
 const state = { commits: 0, skips: 0, cancels: 0, animations: [], callback: null }
 motion.matches = false
 doc.hidden = false
 doc.documentElement = {
  classList: { add: value => classes.add(value), remove: value => classes.delete(value) },
  animate: (frames, options) => { state.animations.push({ frames, options }); return { finished: animationFinished.promise, cancel: () => { state.cancels++; animationFinished.reject(new Error('cancelled')) } } },
 }
 doc.startViewTransition = callback => {
  state.callback = () => { callback(); updates.resolve() }
  state.callback()
  return { ready: ready.promise, finished: finished.promise, updateCallbackDone: updates.promise, skipTransition: () => { state.skips++ } }
 }
 view.matchMedia = () => motion
 view.innerWidth = 390; view.innerHeight = 844
 view.setTimeout = setTimeout; view.clearTimeout = clearTimeout
 view.requestAnimationFrame = callback => setTimeout(callback, 0)
 view.cancelAnimationFrame = clearTimeout
 return { doc, view, motion, ready, finished, animationFinished, classes, state, update: () => { state.commits++ } }
}
const nextTurn = () => new Promise(resolve => setTimeout(resolve, 5))

test('reveal covers the viewport and releases its animation and preparation styles', async () => {
 const f = fixture()
 f.ready.resolve(); f.finished.resolve(); f.animationFinished.resolve()
 await revealTheme(f.update, { x: 370, y: 25 }, f)
 assert.equal(f.state.commits, 1)
 assert.equal(f.classes.size, 0)
 assert.equal(f.state.cancels, 1)
 const { frames, options } = f.state.animations[0]
 assert.equal(options.duration, 420)
 assert.equal(options.pseudoElement, '::view-transition-new(root)')
 assert.equal(frames.clipPath[1], `circle(${Math.ceil(Math.hypot(370, 819))}px at 370px 25px)`)
})

test('rejected capture commits once, even if the browser runs its update callback late', async () => {
 const f = fixture()
 f.doc.startViewTransition = callback => { f.state.callback = callback; return { ready: f.ready.promise, finished: f.finished.promise, updateCallbackDone: f.finished.promise, skipTransition: () => { f.state.skips++ } } }
 const run = revealTheme(f.update, { x: 0, y: 0 }, f)
 await nextTurn(); f.ready.reject(new Error('snapshot failed')); await run
 assert.equal(f.state.commits, 1)
 f.state.callback()
 assert.equal(f.state.commits, 1)
 assert.equal(f.classes.size, 0)
})

test('a stalled capture or finished promise cannot keep the theme locked indefinitely', async () => {
 for (const phase of ['capture', 'finish']) {
  const f = fixture()
  if (phase === 'finish') { f.ready.resolve(); f.animationFinished.resolve() }
  await revealTheme(f.update, { x: 0, y: 0 }, { ...f, timeoutMs: 20 })
  assert.equal(f.state.commits, 1)
  assert.equal(f.classes.size, 0)
  assert.ok(f.state.skips > 0)
 }
})

test('hiding or resizing the page, and enabling reduced motion, cancels pending work', async () => {
 for (const event of ['hide', 'resize', 'motion']) {
  const f = fixture()
  f.ready.resolve()
  const run = revealTheme(f.update, { x: 0, y: 0 }, f)
  await nextTurn()
  if (event === 'hide') { f.doc.hidden = true; f.doc.dispatchEvent(new Event('visibilitychange')) }
  if (event === 'resize') f.view.dispatchEvent(new Event('resize'))
  if (event === 'motion') { f.motion.matches = true; f.motion.dispatchEvent(new Event('change')) }
  await run
  assert.equal(f.state.commits, 1)
  assert.equal(f.classes.size, 0)
  assert.equal(f.state.cancels, 1)
  const skips = f.state.skips
  f.view.dispatchEvent(new Event('resize'))
  assert.equal(f.state.skips, skips, 'interruption listeners should be removed')
 }
})

test('unsupported, hidden and reduced-motion environments switch without taking snapshots', async () => {
 for (const fallback of ['unsupported', 'hidden', 'motion']) {
  const f = fixture()
  if (fallback === 'unsupported') f.doc.startViewTransition = undefined
  if (fallback === 'hidden') f.doc.hidden = true
  if (fallback === 'motion') f.motion.matches = true
  await revealTheme(f.update, { x: 0, y: 0 }, f)
  assert.equal(f.state.commits, 1)
  assert.equal(f.state.animations.length, 0)
  assert.equal(f.classes.size, 0)
 }
})
