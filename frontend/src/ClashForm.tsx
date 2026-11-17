import { useState, type FormEvent } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { get, put, errorText } from './api'
import { ErrorBox } from './App'
type Config = { base_url: string; secret?: string; secret_set: boolean; groups: string[] | null }
export default function ClashForm() {
 const query = useQuery({ queryKey: ['ai', 'clash'], queryFn: () => get<Config>('/ai/clash') })
 return <section className="panel"><details><summary>Clash 控制接口</summary>{query.data && <Editor key={JSON.stringify(query.data)} initial={query.data} />}<ErrorBox error={query.error} /></details></section>
}
function Editor({ initial }: { initial: Config }) {
 const [url, setURL] = useState(initial.base_url), [secret, setSecret] = useState(''), [groups, setGroups] = useState((initial.groups || []).join(', '))
 const [error, setError] = useState(''), [busy, setBusy] = useState(false), [saved, setSaved] = useState(false)
 const qc = useQueryClient()
 async function save(e: FormEvent) {
  e.preventDefault(); setBusy(true); setError(''); setSaved(false)
  try { await put('/ai/clash', { base_url: url, secret, groups: groups.split(',').map(s => s.trim()).filter(Boolean) }); setSecret(''); setSaved(true); await qc.invalidateQueries({ queryKey: ['ai', 'clash'] }) } catch (e) { setError(errorText(e)) } finally { setBusy(false) }
 }
 return <form className="form-grid" onSubmit={save}><ErrorBox error={error} /><label className="full">控制地址<input type="url" value={url} onChange={e => setURL(e.target.value)} placeholder="http://host.docker.internal:9090" /><small>填写 Clash external-controller 地址。任务还需单独授权 Clash 操作。</small></label><label>控制密钥<input type="password" autoComplete="new-password" value={secret} onChange={e => setSecret(e.target.value)} placeholder={initial.secret_set ? '已加密保存，留空保留' : '未设置'} /></label><label>允许切换的策略组<input value={groups} onChange={e => setGroups(e.target.value)} placeholder="PROXY, 自动选择" /><small>多个名称用英文逗号分隔；留空仅允许查看和测速。</small></label><div className="settings-footer full">{saved && <span>已保存</span>}<button className="button" disabled={busy}>保存 Clash 配置</button></div></form>
}
