// JSONL bridge. No built-in tools or project discovery: every tool is executed by Go.
import { createAgentSession, ModelRuntime, SessionManager, SettingsManager, createExtensionRuntime } from '@earendil-works/pi-coding-agent';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const pending = new Map();
let configResolve;
const configPromise = new Promise(resolve => { configResolve = resolve; });
let buffer = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', chunk => {
  buffer += chunk;
  let end;
  while ((end = buffer.indexOf('\n')) >= 0) {
    const line = buffer.slice(0, end); buffer = buffer.slice(end + 1);
    try {
      const record = JSON.parse(line);
      if (configResolve) { const resolve = configResolve; configResolve = null; resolve(record); }
      else { pending.get(record.id)?.(record); pending.delete(record.id); }
    } catch { process.exit(1); }
  }
});
const emit = record => process.stdout.write(JSON.stringify(record) + '\n');
let session, dir;
try {
  const config = await configPromise;
  dir = config.runtime_dir || await mkdtemp(join(tmpdir(), 'zhan-pi-'));
  const modelConfig = config.model;
  const modelRuntime = await ModelRuntime.create({ authPath: join(dir, 'auth.json'), modelsPath: join(dir, 'models.json') });
  modelRuntime.registerProvider('zhan', {
    baseUrl: config.settings.base_url, api: 'openai-completions', apiKey: config.settings.api_key || 'unused',
    models: [{ id: modelConfig.id, name: modelConfig.name, reasoning: false, input: ['text'],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: modelConfig.context_window,
      maxTokens: modelConfig.max_output_tokens, compat: { maxTokensField: modelConfig.output_parameter || 'max_completion_tokens', supportsStore: false, supportsDeveloperRole: false } }]
  });
  const model = modelRuntime.getModel('zhan', modelConfig.id);
  if (!model) throw new Error('Pi model configuration unavailable');
  const resourceLoader = {
    getExtensions: () => ({ extensions: [], errors: [], runtime: createExtensionRuntime() }),
    getSkills: () => ({ skills: [], diagnostics: [] }), getPrompts: () => ({ prompts: [], diagnostics: [] }),
    getThemes: () => ({ themes: [], diagnostics: [] }), getAgentsFiles: () => ({ agentsFiles: [] }),
    getSystemPrompt: () => 'You are an autonomous operations agent. Use only the authorized tools. Never claim an operation succeeded without verifying its actual result. Reply in Chinese.',
    getSystemPromptSource: () => undefined, getAppendSystemPrompt: () => [], getAppendSystemPromptSources: () => [],
    extendResources: () => {}, reload: async () => {}
  };
  const customTools = config.tools.map(({ function: f }) => ({
    name: f.name, label: f.name, description: f.description, parameters: f.parameters,
    execute: async (id, args) => {
      const result = await new Promise(resolve => { pending.set(id, resolve); emit({ type: 'tool', id, name: f.name, arguments: args }); });
      return { content: [{ type: 'text', text: JSON.stringify(result.result) }], details: {}, isError: result.is_error };
    }
  }));
  ({ session } = await createAgentSession({ cwd: dir, agentDir: dir, modelRuntime, model,
    thinkingLevel: 'off', tools: customTools.map(t => t.name), customTools, resourceLoader,
    sessionManager: SessionManager.inMemory(dir), settingsManager: SettingsManager.inMemory({
      compaction: { enabled: true }, retry: { enabled: true, maxRetries: 2 },
      defaultMaxTokens: modelConfig.max_output_tokens
    }) }));
  let failure;
  const stream = session.agent.streamFunction;
  session.agent.streamFunction = (model, context, options) => stream(model, context, {
    ...options, maxTokens: modelConfig.max_output_tokens,
    ...(modelConfig.temperature == null ? {} : { temperature: modelConfig.temperature })
  });
  session.subscribe(event => {
    if (event.type === 'message_update' && event.assistantMessageEvent.type === 'text_delta') emit({ type: 'text', text: event.assistantMessageEvent.delta });
    if (event.type === 'message_end' && event.message.role === 'assistant') failure = ['error', 'aborted'].includes(event.message.stopReason) ? event.message.errorMessage || event.message.stopReason : undefined;
  });
  await session.prompt(config.prompt);
  if (failure) throw new Error(failure);
  emit({ type: 'done' });
} catch (error) {
  emit({ type: 'error', error: String(error.message || error).replace(/Bearer\s+\S+/gi, 'Bearer [redacted]').slice(0, 1000) });
  process.exitCode = 1;
} finally {
  session?.dispose();
  if (dir) await rm(dir, { recursive: true, force: true });
  process.stdin.destroy();
}
