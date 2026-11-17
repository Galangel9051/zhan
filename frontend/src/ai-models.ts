export type ModelConfig = { id: string; name: string; enabled: boolean; context_window: number; max_output_tokens: number; temperature?: number; output_parameter: string }
export type Provider = { enabled: boolean; base_url: string; model: string; models: ModelConfig[]; api_key?: string; key_set: boolean }
export type AvailableModel = { id: string; name: string; owned_by?: string; context_window?: number; max_output_tokens?: number }
export function newModel(id: string, upstream?: AvailableModel): ModelConfig {
 const context = upstream?.context_window || 32768
 return { id, name: upstream?.name || id, enabled: false, context_window: context, max_output_tokens: Math.min(upstream?.max_output_tokens || 4096, Math.floor(context / 4)), output_parameter: 'max_completion_tokens' }
}
export function mergeModels(current: ModelConfig[], upstream: AvailableModel[]): ModelConfig[] {
 const models = new Map(current.map(m => [m.id, m]))
 for (const m of upstream) if (!models.has(m.id)) models.set(m.id, newModel(m.id, m))
 return [...models.values()]
}
export function updateModels(form: Provider, models: ModelConfig[]): Provider {
 return { ...form, models, model: models.some(m => m.enabled && m.id === form.model) ? form.model : models.find(m => m.enabled)?.id || '' }
}
