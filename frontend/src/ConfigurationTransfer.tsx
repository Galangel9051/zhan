import { useRef, useState, type ChangeEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { CheckCircle2, Download, FileJson, Upload } from 'lucide-react'
import { ErrorBox } from './App'
import { errorText, request } from './api'

type Preview = {
 revision: string
 created_projects: number
 updated_projects: number
 projects: string[]
 proxy_hosts: number
 log_retention_days: number
 smtp_enabled: boolean
 warnings: string[]
}

const maxFileBytes = 4 * 1024 * 1024

export default function ConfigurationTransfer() {
 const qc = useQueryClient()
 const picker = useRef<HTMLInputElement>(null)
 const [busy, setBusy] = useState<'export' | 'preview' | 'import' | null>(null)
 const [file, setFile] = useState<{ name: string; content: unknown } | null>(null)
 const [preview, setPreview] = useState<Preview | null>(null)
 const [error, setError] = useState('')
 const [message, setMessage] = useState('')

 async function exportConfig() {
  setBusy('export'); setError(''); setMessage('')
  try {
   const backup = await request<unknown>('/settings/config/export')
   const url = URL.createObjectURL(new Blob([JSON.stringify(backup, null, 2)], { type: 'application/json' }))
   const link = document.createElement('a')
   link.href = url; link.download = `zhan-config-${new Date().toLocaleDateString('sv-SE')}.json`
   document.body.appendChild(link); link.click(); link.remove()
   window.setTimeout(() => URL.revokeObjectURL(url), 1000)
   setMessage('配置文件已导出。')
  } catch (e) { setError(errorText(e)) }
  finally { setBusy(null) }
 }

 async function chooseFile(event: ChangeEvent<HTMLInputElement>) {
  const selected = event.target.files?.[0]
  event.target.value = ''
  if (!selected) return
  setFile(null); setPreview(null); setError(''); setMessage('')
  if (selected.size > maxFileBytes) { setError('配置文件不能超过 4 MiB'); return }
  setBusy('preview')
  try {
   const text = await selected.text()
   let content: unknown
   try { content = JSON.parse(text) } catch { throw new Error('请选择有效的 JSON 配置文件') }
   setFile({ name: selected.name, content })
   const result = await request<{ data: Preview }>('/settings/config/import/preview', { method: 'POST', body: text })
   setPreview(result.data)
  } catch (e) { setError(errorText(e)) }
  finally { setBusy(null) }
 }

 async function importConfig() {
  if (!file || !preview) return
  setBusy('import'); setError(''); setMessage('')
  try {
   const result = await request<{ data: Preview }>('/settings/config/import', { method: 'POST', body: JSON.stringify({ backup: file.content, revision: preview.revision }) })
   setMessage(`配置已导入：新增 ${result.data.created_projects} 个项目，更新 ${result.data.updated_projects} 个项目。网关需手动应用，项目定时检查与自动部署已暂停。`)
   setPreview(null); setFile(null)
   await Promise.all(['settings', 'stacks', 'proxy', 'audit'].map(key => qc.invalidateQueries({ queryKey: [key] })))
  } catch (e) { setError(errorText(e)); setPreview(null) }
  finally { setBusy(null) }
 }

 async function recheckFile() {
  if (!file) return
  setBusy('preview'); setError(''); setMessage(''); setPreview(null)
  try {
   const result = await request<{ data: Preview }>('/settings/config/import/preview', { method: 'POST', body: JSON.stringify(file.content) })
   setPreview(result.data)
  } catch (e) { setError(errorText(e)) }
  finally { setBusy(null) }
 }

 return <section className="panel configuration-transfer">
  <div className="section-heading"><h2><FileJson size={17} />配置导入与导出</h2><span className="subtle">JSON 配置文件</span></div>
  <div className="configuration-body">
   <p className="muted">包含项目与 Git 绑定、邮件设置、网关与代理规则，以及日志保留天数。</p>
   <div className="info-box"><FileJson size={17} /><div>配置文件不包含密码、Token、证书、账户、会话和历史记录。<small>服务器监听地址、数据目录与目录白名单仍由服务器配置文件管理。同一 SMTP 连接与 DuckDNS 主域名可沿用本机已保存的凭据；迁移到新服务器后需重新填写。</small></div></div>
   <div className="configuration-actions">
    <div className="configuration-action"><h3>导出当前配置</h3><p>下载一份配置文件，用于备份或迁移。</p><button className="button" disabled={!!busy} onClick={() => void exportConfig()}><Download size={15} />{busy === 'export' ? '正在导出…' : '导出配置'}</button></div>
    <div className="configuration-action"><h3>从文件导入</h3><p>选择文件后先校验，确认变更范围后再写入。</p><input ref={picker} hidden type="file" accept=".json,application/json" aria-label="配置文件" disabled={!!busy} onChange={event => void chooseFile(event)} /><button className="button" disabled={!!busy} onClick={() => picker.current?.click()}><Upload size={15} />{busy === 'preview' ? '正在校验…' : file ? '重新选择文件' : '选择配置文件'}</button></div>
   </div>
   <div aria-live="polite">{message && <div className="info-box"><CheckCircle2 size={17} />{message}</div>}<ErrorBox error={error} /></div>
   {file && <div className="configuration-filename"><FileJson size={15} /><span>{file.name}</span>{!preview && <button className="text-button" disabled={!!busy} onClick={() => void recheckFile()}>重新校验</button>}</div>}
   {preview && <div className="configuration-preview motion-enter">
    <h3>导入预览</h3>
    <dl className="detail-fields"><dt>新增项目</dt><dd>{preview.created_projects} 个</dd><dt>更新同名项目</dt><dd>{preview.updated_projects} 个</dd><dt>替换代理规则</dt><dd>{preview.proxy_hosts} 条</dd><dt>邮件通知</dt><dd>{preview.smtp_enabled ? '已配置' : '关闭'}</dd><dt>日志保留</dt><dd>{preview.log_retention_days} 天</dd></dl>
    {!!preview.projects.length && <details><summary>涉及的项目（{preview.projects.length}）</summary><ul>{preview.projects.map(project => <li key={project}><code>{project}</code></li>)}</ul></details>}
    <p className="muted">文件中的设置将覆盖对应配置，其他项目及任务历史保留。所有变更会一起保存，校验失败时不会部分导入。项目目录和 Compose 文件必须已存在于当前服务器。</p>
    <ul className="configuration-notes">{preview.warnings.map(warning => <li key={warning}>{warning}</li>)}</ul>
    <div className="settings-footer"><button className="button" disabled={!!busy} onClick={() => { setFile(null); setPreview(null); setError('') }}>取消</button><button className="button primary" disabled={!!busy} onClick={() => void importConfig()}><Upload size={15} />{busy === 'import' ? '正在导入…' : '确认导入'}</button></div>
   </div>}
  </div>
 </section>
}
