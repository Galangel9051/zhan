import { useState, type FormEvent } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { Download, Plus, Search } from 'lucide-react'
import { errorText, post, put } from './api'
import { ErrorBox, Modal } from './App'
import { mergeModels, newModel, updateModels, type AvailableModel, type ModelConfig, type Provider } from './ai-models'

export default function AIProviderForm({ initial, close }: { initial: Provider; close: () => void }) {
 const [form, setForm] = useState<Provider>({ ...initial, models: initial.models ?? [], api_key: '' })
 const [busy, setBusy] = useState(false), [fetching, setFetching] = useState(false), [error, setError] = useState('')
 const [search, setSearch] = useState(''), [manual, setManual] = useState(''), [limit, setLimit] = useState(100)
 const [available, setAvailable] = useState<AvailableModel[] | null>(null)
 const qc = useQueryClient(), enabled = form.models.filter(m => m.enabled)
 const filtered = form.models.filter(m => `${m.id} ${m.name}`.toLowerCase().includes(search.toLowerCase()))
 const shown = filtered.slice(0, limit)
 function edit(id: string, change: Partial<ModelConfig>) { setForm(f => updateModels(f, f.models.map(m => m.id === id ? { ...m, ...change } : m))) }
 async function discover() {
  setFetching(true); setError('')
  try {
   const list = await post<AvailableModel[]>('/ai/models/discover', { base_url: form.base_url, api_key: form.api_key })
   setAvailable(list); setForm(f => ({ ...f, models: mergeModels(f.models, list) })); setLimit(100)
  } catch (e) { setError(errorText(e)) } finally { setFetching(false) }
 }
 function addManual() {
  const id = manual.trim(); if (!id) return
  if (form.models.some(m => m.id === id)) { setError('该模型已在列表中，可以搜索并勾选。'); setSearch(id); return }
  setForm(f => updateModels(f, [...f.models, { ...newModel(id), enabled: true }]))
  setManual(''); setSearch(id); setError('')
 }
 async function save(e: FormEvent) {
  e.preventDefault(); setBusy(true); setError('')
  try {
   // Discovered, unchecked candidates are not persistent configuration.
   const previous = new Set(initial.models?.map(m => m.id))
   await put('/ai/settings', { ...form, models: form.models.filter(m => m.enabled || previous.has(m.id)) })
   await qc.invalidateQueries({ queryKey: ['ai', 'settings'] }); close()
  } catch (e) { setError(errorText(e)) } finally { setBusy(false) }
 }
 return <Modal title="AI 接口与模型" subtitle="拉取上游模型、勾选启用，并为每个模型设置运行参数。" close={close} wide>
  <form onSubmit={save}>
   <div className="modal-body form-grid ai-provider-form">
    <ErrorBox error={error} />
    <label className="full">API 基础地址<input type="url" required value={form.base_url} onChange={e => { setForm({ ...form, base_url: e.target.value }); setAvailable(null) }} placeholder="https://api.openai.com/v1" /><small>例如 https://api.openai.com/v1，拉取模型使用 /models，执行任务使用 /chat/completions。</small></label>
    <label className="full">API Key<input type="password" autoComplete="new-password" value={form.api_key} onChange={e => setForm({ ...form, api_key: e.target.value })} placeholder={initial.key_set ? '已保存；地址不变时留空保留原密钥' : '本地模型可留空'} /><small>密钥加密保存。更换地址后请重新输入密钥。任务读取的文件和日志会发送到该接口。</small></label>
    <div className="ai-model-heading full"><div><strong>可用模型</strong><small>已勾选 {enabled.length} 个 · 仅勾选的模型可用于任务</small></div><button type="button" className="button" disabled={fetching || busy || !form.base_url} onClick={() => void discover()}><Download size={14} />{fetching ? '拉取中…' : '拉取上游模型'}</button></div>
    <p className="muted full">请选支持工具调用的模型。上下文是本地预算，不会改变上游模型的实际容量；未提供参数时先填入 32768 / 4096，请按服务商说明确认。温度留空时使用上游默认值。</p>
    {available !== null && <p className="success-text full" role="status">已从上游拉取 {available.length} 个模型；保留已有选择和参数，未自动启用新模型。{available.length === 0 && ' 可手动添加模型 ID。'}</p>}
    <div className="ai-model-search full"><Search size={15} /><input aria-label="搜索模型" placeholder="搜索模型 ID 或名称" value={search} onChange={e => { setSearch(e.target.value); setLimit(100) }} /></div>
    <div className="ai-model-list full">
     {!shown.length && <p className="muted">暂无匹配模型，先拉取列表或手动添加。</p>}
     {shown.map(model => <div className={`ai-model-card ${model.enabled ? 'selected' : ''}`} key={model.id}>
      <label className="checkbox-label"><input type="checkbox" checked={model.enabled} onChange={e => edit(model.id, { enabled: e.target.checked })} /><strong>{model.id}</strong>{model.id === form.model && <span className="subtle">默认</span>}</label>
      {model.enabled && <div className="form-grid ai-model-fields">
       <label>显示名称<input value={model.name} maxLength={200} onChange={e => edit(model.id, { name: e.target.value })} /></label>
       <label>上下文窗口（tokens）<input type="number" required min={1024} max={4000000} value={model.context_window} onChange={e => edit(model.id, { context_window: Number(e.target.value) })} /></label>
       <label>最大输出（tokens）<input type="number" required min={1} max={Math.max(1, model.context_window - 1)} value={model.max_output_tokens} onChange={e => edit(model.id, { max_output_tokens: Number(e.target.value) })} /></label>
       <label>温度<input type="number" min={0} max={2} step="0.1" placeholder="上游默认" value={model.temperature ?? ''} onChange={e => edit(model.id, { temperature: e.target.value === '' ? undefined : Number(e.target.value) })} /></label>
       <label className="full">输出限制参数<select value={model.output_parameter} onChange={e => edit(model.id, { output_parameter: e.target.value })}><option value="max_completion_tokens">max_completion_tokens</option><option value="max_tokens">max_tokens（兼容旧上游）</option></select></label>
       <small className="full subtle">{available?.find(m => m.id === model.id)?.context_window ? `上游报告上下文：${available.find(m => m.id === model.id)?.context_window} tokens` : '上游上下文容量未知，请手动确认。'} 执行前按消息与工具的 UTF-8 字节数保守估算输入，并预留最大输出，预算不足时停止并提示。</small>
      </div>}
     </div>)}
     {filtered.length > limit && <button type="button" className="button compact" onClick={() => setLimit(n => n + 100)}>加载更多（还有 {filtered.length - limit} 个）</button>}
    </div>
    <div className="ai-model-manual full"><input aria-label="手动模型 ID" maxLength={200} value={manual} onChange={e => setManual(e.target.value)} placeholder="上游不支持列表时，手动填写模型 ID" /><button type="button" className="button" onClick={addManual} disabled={!manual.trim()}><Plus size={14} />添加模型</button></div>
    <label className="full">默认模型<select value={form.model} onChange={e => setForm({ ...form, model: e.target.value })} required={form.enabled}><option value="">请选择已启用的模型</option>{enabled.map(m => <option key={m.id} value={m.id}>{m.name || m.id} · {m.id}</option>)}</select><small>未单独选择模型的任务使用此模型。</small></label>
    <label className="checkbox-label full"><input type="checkbox" checked={form.enabled} onChange={e => setForm({ ...form, enabled: e.target.checked })} />启用 AI 接口与任务执行</label>
   </div>
   <div className="modal-footer"><button type="button" className="button" onClick={close}>取消</button><button className="button primary" disabled={busy || fetching || (form.enabled && !enabled.length)}>{busy ? '保存中…' : '保存接口与模型'}</button></div>
  </form>
 </Modal>
}
