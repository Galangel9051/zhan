import { useQuery } from '@tanstack/react-query'
import { GitBranch, RefreshCw } from 'lucide-react'
import { date, errorText, get, short, type Stack } from './api'
import { layoutGitGraph } from './git-graph'

type Commit = { hash: string; parents: string[]; author: string; authored_at: string; subject: string }
type History = { commits: Commit[]; local_commit: string; remote_commit: string; remote_source: string; current_branch: string; dirty: boolean; ahead: number | null; behind: number | null; truncated: boolean }
const x = (lane: number) => 12 + lane * 18

export default function GitHistory({ stack }: { stack: Stack }) {
 const query = useQuery({ queryKey: ['git-history', stack.id, stack.branch, stack.remote_commit, stack.local_commit, stack.last_success_commit, stack.last_attempted_commit], queryFn: () => get<History>(`/stacks/${stack.id}/git/history`), enabled: !!stack.branch, staleTime: 10000, refetchInterval: 15000, retry: false })
 const history = query.data
 const markers = [
  { label: '本地 HEAD', hash: history?.local_commit, kind: 'local' },
  { label: history?.remote_source === 'cached' ? '远程 · 本机记录' : '远程 · 最近检测', hash: history?.remote_commit, kind: 'remote' },
  { label: '最近成功部署', hash: stack.last_success_commit, kind: 'deployed' },
  { label: '最近尝试部署', hash: stack.last_attempted_commit, kind: 'attempted' },
 ].filter(marker => marker.hash)
 const graph = layoutGitGraph(history?.commits ?? [])
 const status = history?.ahead == null ? '远程历史尚未获取，暂无法比较版本差异' : history.ahead && history.behind ? `本地与远程已分叉 · 本地独有 ${history.ahead} 个，远程独有 ${history.behind} 个提交` : history.behind ? `本地落后远程 ${history.behind} 个提交` : history.ahead ? `本地领先远程 ${history.ahead} 个提交` : '本地与最近记录的远程版本一致'
 const missing = markers.filter(marker => !history?.commits.some(commit => commit.hash === marker.hash))
 return <section className="panel git-history-panel">
  <div className="section-heading"><h2><GitBranch size={17} />Git 版本追踪</h2>{stack.branch && <button type="button" className="text-button" disabled={query.isFetching} onClick={() => void query.refetch()}><RefreshCw size={13} />刷新提交树</button>}</div>
  <dl className="detail-fields git-history-fields"><dt>仓库</dt><dd className="break-all">{stack.repository || '未绑定'}</dd><dt>绑定分支</dt><dd><code>{stack.branch || '—'}</code></dd></dl>
  {!stack.branch ? <p className="git-history-message">尚未绑定 Git 分支，请在项目设置中绑定后查看提交树。</p> : query.isPending ? <p className="git-history-message" role="status">正在读取提交树…</p> : query.isError ? <div className="git-history-message" role="alert">{errorText(query.error)}<p>可点击“刷新提交树”重试。</p></div> : history && <>
   <div className="git-history-status" role="status"><p>{history.local_commit ? status : '仓库暂无提交'}</p>{history.dirty && <span className="git-version-tag is-warning">工作区有未提交修改</span>}{history.current_branch !== stack.branch && history.local_commit && <span className="git-version-tag is-warning">{history.current_branch ? `当前分支为 ${history.current_branch}，与绑定分支不一致` : '当前处于分离 HEAD'}</span>}{!stack.last_success_commit && <small>暂无成功部署记录</small>}</div>
   {!!history.commits.length && <div className="git-commit-scroll" role="region" aria-label="Git 提交树" tabIndex={0}><ol className="git-commit-list">{history.commits.map((commit, index) => {
    const row = graph.rows[index], labels = markers.filter(marker => marker.hash === commit.hash)
    return <li key={commit.hash} className="git-commit-row" style={{ gridTemplateColumns: `${graph.width}px minmax(200px,1fr)` }}>
     <svg className="git-commit-graph" width={graph.width} height="88" aria-hidden="true">{row.before.map((hash, lane) => (hash !== commit.hash || row.incoming) && <path key={`in:${hash}`} d={`M ${x(lane)} 0 V 30`} />)}{row.edges.map(edge => <path key={`${edge.from}:${edge.parent}`} d={`M ${x(edge.from)} 30 C ${x(edge.from)} 65 ${x(edge.to)} 55 ${x(edge.to)} 88`} />)}<circle cx={x(row.lane)} cy="30" r="5" className={labels.some(label => label.kind === 'deployed') ? 'is-deployed' : labels.some(label => label.kind === 'local') ? 'is-local' : ''} /></svg>
     <div className="git-commit-content"><div className="git-commit-title"><code title={commit.hash}>{short(commit.hash)}</code><strong title={commit.subject}>{commit.subject || '（无提交说明）'}</strong></div><div className="git-commit-meta"><span title={commit.author}>{commit.author}</span><time dateTime={commit.authored_at} title={commit.authored_at}>{date(commit.authored_at)}</time>{commit.parents.length > 1 && <span>合并提交</span>}</div><div className="git-commit-labels">{labels.map(label => <span key={label.kind} className={`git-version-tag is-${label.kind}`}>{label.label}</span>)}</div></div>
    </li>
   })}</ol>{history.truncated && <p className="git-history-footnote">仅显示最近 50 个提交，更早的历史已省略。</p>}</div>}
   {!!missing.length && <div className="git-history-missing">{missing.map(marker => <p key={marker.kind}><span className={`git-version-tag is-${marker.kind}`}>{marker.label}</span><code title={marker.hash}>{short(marker.hash!)}</code><small>不在当前提交树内，可能尚未获取或超出显示范围</small></p>)}</div>}
   <p className="git-history-footnote">连线表示真实父提交关系。远程位置来自最近检查或本机记录；刷新提交树仅读取本地历史。需获取最新代码时，请执行“拉取代码”或“拉取并部署”。</p>
  </>}
  {stack.failure_count > 0 && <div className="retry-info">连续失败 {stack.failure_count} 次 · {stack.retry_at ? `下次重试：${new Date(stack.retry_at * 1000).toLocaleString('zh-CN')}` : '自动重试已暂停，出现新 Commit 后恢复'}</div>}
 </section>
}
