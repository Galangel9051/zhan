import { useEffect, useId, useState, type FormEvent } from 'react'
import { ChevronLeft, ChevronRight } from 'lucide-react'
import { pageNumbers, parsePageJump } from './pagination-utils'

type Props = { page: number; pageSize: number; total: number; onChange: (page: number) => void; disabled?: boolean }

export default function Pagination({ page, pageSize, total, onChange, disabled = false }: Props) {
 const totalPages = Math.max(1, Math.ceil(total / pageSize))
 const [jump, setJump] = useState(String(page)), [error, setError] = useState('')
 const errorID = useId()
 useEffect(() => { setJump(String(page)); setError('') }, [page])
 const locked = disabled || total === 0
 function change(next: number) { setError(''); if (next !== page) onChange(next) }
 function submit(event: FormEvent) {
  event.preventDefault()
  const next = parsePageJump(jump, totalPages)
  if (next === null) { setError(`请输入 1–${totalPages} 的整数页码`); return }
  change(next)
 }
 return <nav className="pagination numbered-pagination" aria-label="部署历史分页">
  <span className="subtle pagination-summary" aria-live="polite">共 {total} 条 · 每页 {pageSize} 条 · 第 {page} / {totalPages} 页</span>
  <div className="pagination-controls">
   <button type="button" className="button compact pagination-nav" aria-label="上一页" disabled={locked || page <= 1} onClick={() => change(page - 1)}><ChevronLeft size={14} /><span>上一页</span></button>
   {pageNumbers(page, totalPages).map(item => typeof item === 'number' ? <button type="button" key={item} className={`button compact pagination-number ${item === page ? 'is-current' : ''}`} aria-label={`第 ${item} 页`} aria-current={item === page ? 'page' : undefined} disabled={locked} onClick={() => change(item)}>{item}</button> : <span className="pagination-gap" aria-hidden="true" key={item}>…</span>)}
   <button type="button" className="button compact pagination-nav" aria-label="下一页" disabled={locked || page >= totalPages} onClick={() => change(page + 1)}><span>下一页</span><ChevronRight size={14} /></button>
  </div>
  <form className="pagination-jump" onSubmit={submit} noValidate>
   <label>前往<input aria-label="跳转页码" aria-describedby={error ? errorID : undefined} aria-invalid={!!error} inputMode="numeric" autoComplete="off" value={jump} onChange={event => { setJump(event.target.value); setError('') }} disabled={locked || totalPages <= 1} />页</label>
   <button className="button compact" disabled={locked || totalPages <= 1}>跳转</button>
   {error && <span id={errorID} className="pagination-error" role="alert">{error}</span>}
  </form>
 </nav>
}
