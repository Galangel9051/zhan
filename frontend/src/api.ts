export interface User { id: number; username: string; csrf_token: string; must_change_password: boolean }
export interface Container { id: string; name: string; service: string; state: string; health: string; image: string; ports: string; created_at?: string; started_at?: string; restart_count?: number }
export interface ContainerStats { cpu_percent: number | null; memory_bytes: number | null; memory_limit: number | null; memory_percent: number | null; network_rx: number | null; network_tx: number | null; block_read: number | null; block_write: number | null; pids: number | null }
export interface ResourceSnapshot { sampled_at: string; containers: Record<string, ContainerStats>; unavailable_count: number }
export interface Service { name: string; image: string; build: boolean; dependencies: string[]; status: string; containers: Container[] }
export interface Task { id: number; stack_id: number; stack_name: string; task_type: string; trigger_type: string; service: string; container_id: string; start_after_deploy: boolean; commit_sha: string; status: string; stage: string; exit_code: number; error_message: string; created_at: string; started_at: string; finished_at: string; retry_of: number }
export interface TaskPage { data: Task[]; pagination: { page: number; page_size: number; total: number; total_pages: number } }
export interface Stack {
 id: number; name: string; project_name: string; project_directory: string; compose_files: string[]; desired_state: string; created_at: string;
 repository: string; branch: string; polling_enabled: boolean; poll_interval: number; auto_deploy: boolean; notify_on_failure: boolean; no_cache: boolean; build_pull: boolean; pull_images: boolean;
 remote_commit: string; local_commit: string; last_attempted_commit: string; last_success_commit: string; failure_count: number; failed_commit: string; retry_at: number; retry_type: string; last_failed_task: number; next_poll_at: number;
 runtime_status: string; services: Service[]; last_task?: Task; runtime_error?: string;
}
export interface Discovery { name: string; status: string; config_files: string[] }
export interface SMTP { enabled?: boolean; host: string; port: number; username: string; password?: string; password_set: boolean; tls_mode: string; from: string; recipients: string[]; stages: string[]; cooldown_minutes: number }
export interface Settings { smtp: SMTP; allowed_roots: string[]; listen: string; workers: number; log_retention_days: number; max_log_mb: number; retry_delays_seconds: number[]; retry_base_seconds: number; retry_max_seconds: number; retry_max_attempts: number; secure_cookies: boolean }
export interface GitKeyInfo { public_key: string; fingerprint: string; key_path: string; known_hosts_path: string; created_at: string; known_hosts_count: number }
export interface GitTestResult { ok: boolean; host: string; message: string; hint: string; output: string }
export interface Audit { id: number; user: string; action: string; target: string; result: string; created_at: string }
export interface Notification { id: number; task_id: number; recipient: string; notification_type: string; status: string; sent_at: number; error_message: string }
let csrf = ''
export function setCSRF(value: string) { csrf = value }
export class APIError extends Error { status: number; constructor(status: number, message: string) { super(message); this.status = status } }
export async function request<T>(path: string, options?: RequestInit): Promise<T> {
 const response = await fetch(`/api/v1${path}`, { ...options, credentials: 'same-origin', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf, ...options?.headers } })
 if (!response.ok) {
  if (response.status === 401 && !path.startsWith('/auth/')) window.dispatchEvent(new Event('cm-unauthorized'))
  const body = await response.json().catch(() => null) as { error?: string } | null
  throw new APIError(response.status, body?.error ?? `请求失败（${response.status}）`)
 }
 if (response.status === 204) return undefined as T
 return response.json() as Promise<T>
}
export async function get<T>(path: string) { return (await request<{ data: T }>(path)).data }
export async function post<T>(path: string, body: unknown = {}) { return (await request<{ data: T } | undefined>(path, { method: 'POST', body: JSON.stringify(body) }))?.data as T }
export async function put<T>(path: string, body: unknown) { return (await request<{ data: T } | undefined>(path, { method: 'PUT', body: JSON.stringify(body) }))?.data as T }
export const taskLabels: Record<string, string> = { git_check: '检查更新', git_pull: '拉取代码', deploy: '拉取并部署', rebuild: '重新构建', start: '启动', stop: '停止', restart: '重启' }
export const statusLabels: Record<string, string> = { running: '运行中', stopped: '已停止', partial: '部分运行', unhealthy: '不健康', unknown: '未知', pending: '排队中', success: '成功', failed: '失败', cancelled: '已取消', exited: '已退出', created: '已创建', restarting: '重启中', healthy: '健康', starting: '启动中' }
export function date(value: string) { return value ? new Date(value).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' }) : '—' }
export function short(value: string) { return value ? value.slice(0, 8) : '—' }
export function errorText(error: unknown): string {
 if (typeof error === 'string' && error.trim()) return error
 if (error instanceof Error && error.message.trim()) return error.message
 return '操作失败，请稍后重试'
}
