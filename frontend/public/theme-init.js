// Apply the saved appearance before the first paint; this is external for CSP.
(() => {
  let preference
  try { preference = localStorage.getItem('zhan-theme') } catch { /* storage can be unavailable */ }
  const dark = preference === 'dark' || (preference !== 'light' && window.matchMedia('(prefers-color-scheme: dark)').matches)
  document.documentElement.dataset.theme = dark ? 'dark' : 'light'
  const meta = document.querySelector('meta[name="theme-color"]')
  if (meta) meta.content = dark ? '#212121' : '#ffffff'
})()
