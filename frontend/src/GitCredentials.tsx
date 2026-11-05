import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { AlertTriangle, Check, CheckCircle2, Copy, ExternalLink, KeyRound, RefreshCw, ShieldCheck, Terminal, Trash2 } from 'lucide-react'
import { ErrorBox } from './App'
import { date, errorText, get, post, type GitKeyInfo, type GitTestResult } from './api'

const platforms = [
 { host: 'github.com', label: 'GitHub', keys: 'https://github.com/settings/keys', deploy: '仓库 → Settings → Deploy keys' },
 { host: 'gitlab.com', label: 'GitLab', keys: 'https://gitlab.com/-/profile/keys', deploy: '仓库 → Settings → Repository → Deploy keys' },
 { host: 'gitee.com', label: 'Gitee', keys: 'https://gitee.com/profile/sshkeys', deploy: '仓库 → 管理 → 部署公钥' },
]

export default function GitCredentials() {
 const query = useQuery({ queryKey: ['git-credentials'], queryFn: () => get<GitKeyInfo>('/settings/git') })
 const [copied, setCopied] = useState(false)
 const [busy, setBusy] = useState<'test' | 'regenerate' | 'reset' | null>(null)
 const [error, setError] = useState('')
 const [message, setMessage] = useState('')
 const [host, setHost] = useState('github.com')
 const [test, setTest] = useState<GitTestResult | null>(null)
 const [confirmRegenerate, setConfirmRegenerate] = useState(false)
 const [confirmReset, setConfirmReset] = useState(false)

 async function copyKey() {
  const value = query.data?.public_key
  if (!value) return
  try {
   if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(value)
   } else {
    const area = document.createElement('textarea')
    area.value = value
    area.setAttribute('readonly', '')
    area.style.position = 'fixed'
    area.style.opacity = '0'
    document.body.appendChild(area)
    area.select()
    const ok = document.execCommand('copy')
    area.remove()
    if (!ok) throw new Error('copy failed')
   }
   setCopied(true)
   setError('')
   window.setTimeout(() => setCopied(false), 2000)
  } catch {
   setError('复制失败，请手动选中公钥文本复制。')
  }
 }

 async function runTest() {
  setBusy('test'); setError(''); setMessage(''); setTest(null)
  try { setTest(await post<GitTestResult>('/settings/git/test', { host })) }
  catch (e) { setError(errorText(e)) }
  finally { setBusy(null) }
 }

 async function regenerate() {
  setBusy('regenerate'); setError(''); setMessage(''); setTest(null)
  try {
   await post<GitKeyInfo>('/settings/git/regenerate', { confirm: true })
   await query.refetch()
   setConfirmRegenerate(false)
   setMessage('新的 SSH 密钥已生成。请把上面的新公钥添加到 Git 平台，并删除旧公钥。')
  } catch (e) { setError(errorText(e)) }
  finally { setBusy(null) }
 }

 async function resetHosts() {
  setBusy('reset'); setError(''); setMessage(''); setTest(null)
  try {
   await post('/settings/git/known-hosts/reset', { confirm: true })
   await query.refetch()
   setConfirmReset(false)
   setMessage('已清除已信任主机。下次连接会重新记录主机指纹。')
  } catch (e) { setError(errorText(e)) }
  finally { setBusy(null) }
 }

 if (query.isPending) return <div className="loading">正在读取 Git 凭据…</div>
 return <>
  {message && <div className="info-box"><CheckCircle2 size={16} />{message}</div>}
  <ErrorBox error={error || query.error} />
  {query.data && <>
   <div className="info-box"><ShieldCheck size={16} /><div>Zhan 使用一把专用 SSH 密钥访问 Git 仓库。私钥只保存在服务器 <code>{query.data.key_path}</code>，不会通过接口返回。<small>公开的 HTTPS 仓库无需配置；私有仓库或 SSH 地址需要把下面的公钥添加到 Git 平台。</small></div></div>
   <div className="git-credentials-grid">
    <section className="panel">
     <div className="section-heading"><h2><KeyRound size={17} />SSH 公钥</h2><span className="subtle">{date(query.data.created_at)} 生成</span></div>
     <div className="git-credential-body">
      <textarea className="git-key-value" readOnly rows={4} value={query.data.public_key} onFocus={e => e.currentTarget.select()} aria-label="SSH 公钥" />
      <div className="git-key-meta"><code>{query.data.fingerprint}</code><span className="subtle">已记录 {query.data.known_hosts_count} 个主机指纹</span></div>
      <div className="git-key-actions">
       <button type="button" className="button primary" onClick={() => void copyKey()}>{copied ? <><Check size={14} />已复制</> : <><Copy size={14} />复制公钥</>}</button>
       <span className="subtle">添加时选择只读权限即可；需要推送时再勾选写权限。</span>
      </div>
     </div>
    </section>
    <section className="panel">
     <div className="section-heading"><h2><ShieldCheck size={17} />三步完成</h2></div>
     <div className="git-credential-body">
      <ol className="git-steps">
       <li><b>1</b><div><strong>复制公钥</strong><span>点左侧“复制公钥”。</span></div></li>
       <li><b>2</b><div><strong>添加到 Git 平台</strong><span>推荐使用仓库的 Deploy Key（只读），也可以加到账号 SSH Keys。</span><div className="git-links">{platforms.map(item => <a key={item.host} href={item.keys} target="_blank" rel="noreferrer">{item.label} SSH Keys<ExternalLink size={11} /></a>)}</div></div></li>
       <li><b>3</b><div><strong>测试连接</strong><span>在下方选择平台并测试；成功后即可导入或检查仓库。</span></div></li>
      </ol>
      <div className="git-platform-note"><AlertTriangle size={15} /><div><strong>提示</strong><span>如果提示 <code>Permission denied (publickey)</code>，说明公钥还没有加到对应仓库或账号；如果提示主机指纹变化，请先核对平台公告，再清除已信任主机。</span></div></div>
     </div>
    </section>
   </div>
   <section className="panel">
    <div className="section-heading"><h2><Terminal size={17} />测试连接</h2><span className="subtle">只验证 SSH 认证，不改动仓库</span></div>
    <div className="git-credential-body">
     <p className="muted">选择代码托管平台，Zhan 会用内置密钥执行一次只读 SSH 握手。</p>
     <div className="git-test-row">
      <select value={host} onChange={e => setHost(e.target.value)} aria-label="Git 托管平台">{platforms.map(item => <option key={item.host} value={item.host}>{item.label}（{item.host}）</option>)}</select>
      <button type="button" className="button primary" disabled={busy !== null} onClick={() => void runTest()}>{busy === 'test' ? '正在测试…' : '测试连接'}</button>
     </div>
     {test && <div className={`git-test-result ${test.ok ? 'success' : 'failed'}`}>
      <div className="git-test-title">{test.ok ? <CheckCircle2 size={17} /> : <AlertTriangle size={17} />}<strong>{test.message}</strong><span className="subtle">{test.host}</span></div>
      {test.hint && <p>{test.hint}</p>}
      {test.output && <details><summary>查看原始 SSH 输出</summary><pre>{test.output}</pre></details>}
     </div>}
    </div>
   </section>
   <section className="panel">
    <div className="section-heading"><h2><RefreshCw size={17} />密钥维护</h2><span className="subtle">谨慎操作</span></div>
    <div className="git-credential-body git-danger-body">
     <div className="git-danger-row">
      <div><strong>重新生成密钥</strong><p>旧公钥会立即失效，需要到所有 Git 平台删除旧公钥并添加新公钥。</p></div>
      {confirmRegenerate ? <div className="git-confirm"><span>确认重新生成？</span><button type="button" className="button" disabled={busy !== null} onClick={() => setConfirmRegenerate(false)}>取消</button><button type="button" className="button primary" disabled={busy !== null} onClick={() => void regenerate()}>{busy === 'regenerate' ? '正在生成…' : '确认生成'}</button></div> : <button type="button" className="button" disabled={busy !== null} onClick={() => setConfirmRegenerate(true)}><RefreshCw size={14} />重新生成</button>}
     </div>
     <div className="git-danger-row">
      <div><strong>清除已信任主机</strong><p>当前记录 {query.data.known_hosts_count} 条指纹。只有在平台更换密钥或指纹确认无误后才使用。</p></div>
      {confirmReset ? <div className="git-confirm"><span>确认清除？</span><button type="button" className="button" disabled={busy !== null} onClick={() => setConfirmReset(false)}>取消</button><button type="button" className="button primary" disabled={busy !== null} onClick={() => void resetHosts()}>{busy === 'reset' ? '正在清除…' : '确认清除'}</button></div> : <button type="button" className="button" disabled={busy !== null} onClick={() => setConfirmReset(true)}><Trash2 size={14} />清除主机</button>}
     </div>
    </div>
   </section>
   <div className="git-help">
    <h3>为什么需要这一步？</h3>
    <p>私有仓库必须证明你有访问权限。Zhan 不保存你的账号密码，而是使用一把专用密钥；公钥可以随时在 Git 平台撤销，私钥始终留在服务器数据目录。</p>
    <p>如果仓库地址是 <code>https://</code> 且仓库公开，通常可以直接拉取；私有 HTTPS 仓库目前请改用 SSH 地址，或使用 Git 平台提供的访问令牌配置凭据助手。</p>
   </div>
  </>}
 </>
}
