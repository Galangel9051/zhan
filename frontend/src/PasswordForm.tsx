import { useState, type FormEvent } from 'react'
import { KeyRound } from 'lucide-react'
import { errorText, post } from './api'
import { ErrorBox } from './App'

export default function PasswordForm({ changed }: { changed: () => void }) {
 const [current, setCurrent] = useState(''), [next, setNext] = useState(''), [confirm, setConfirm] = useState('')
 const [busy, setBusy] = useState(false), [error, setError] = useState('')
 async function save(e: FormEvent) {
  e.preventDefault(); setError('')
  if (Array.from(next).length < 6 || new TextEncoder().encode(next).length > 128) { setError('新密码至少 6 个字符，最多 128 字节'); return }
  if (next !== confirm) { setError('两次输入的新密码不一致'); return }
  if (next === current) { setError('新密码不能与当前密码相同'); return }
  setBusy(true)
  try { await post('/auth/password', { current_password: current, new_password: next, confirm_password: confirm }); setCurrent(''); setNext(''); setConfirm(''); changed() }
  catch (e) { setError(errorText(e)); setBusy(false) }
 }
 return <form className="panel settings-form" onSubmit={save}><div className="section-heading"><h2><KeyRound size={17} />修改密码</h2></div><p className="muted">修改当前登录账户的密码。保存成功后，所有已登录会话失效，请使用新密码重新登录。</p><ErrorBox error={error} /><fieldset disabled={busy} className="form-grid"><label className="full">当前密码<input type="password" autoComplete="current-password" required maxLength={128} value={current} onChange={e => setCurrent(e.target.value)} /></label><label>新密码<input type="password" autoComplete="new-password" required minLength={6} maxLength={128} value={next} onChange={e => setNext(e.target.value)} /><small>至少 6 个字符，最多 128 字节，不能与当前密码相同。</small></label><label>确认新密码<input type="password" autoComplete="new-password" required minLength={6} maxLength={128} value={confirm} onChange={e => setConfirm(e.target.value)} /></label></fieldset><div className="settings-footer"><button className="button primary" disabled={busy}>{busy ? '正在修改…' : '修改密码'}</button></div></form>
}
