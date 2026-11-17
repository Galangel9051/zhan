import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';

test('Pi SDK streams a real tool round trip with configured model limits', { timeout: 90000 }, async (t) => {
  let requests = 0;
  const server = createServer(async (req, res) => {
    let body = ''; for await (const chunk of req) body += chunk;
    const input = JSON.parse(body);
    assert.equal(req.headers.authorization, 'Bearer fixture-key');
    assert.equal(input.model, 'fixture');
    assert.equal(input.max_completion_tokens, 512);
    assert.equal(input.temperature, 0.3);
    assert.deepEqual(input.tools.map(t => t.function.name), ['read_file']);
    assert.equal(input.stream, true);
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    const chunk = (delta, finish = null) => res.write('data: ' + JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', model: 'fixture', choices: [{ index: 0, delta, finish_reason: finish }] }) + '\n\n');
    if (++requests === 1) {
      chunk({ role: 'assistant', tool_calls: [{ index: 0, id: 'read-1', type: 'function', function: { name: 'read_file', arguments: '{"path":"app.py"}' } }] });
      chunk({}, 'tool_calls');
    } else {
      assert.ok(input.messages.some(m => m.role === 'tool' && m.content.includes('fixture-content')));
      chunk({ role: 'assistant', content: 'verified' }); chunk({}, 'stop');
    }
    res.end('data: [DONE]\n\n');
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const child = spawn(process.execPath, [fileURLToPath(new URL('./bridge.mjs', import.meta.url))], { stdio: ['pipe', 'pipe', 'pipe'] });
  t.after(() => { child.kill(); server.closeAllConnections(); server.close(); });
  let stderr = '', pending = '', text = '', done = false, calls = 0;
  child.stderr.on('data', chunk => { stderr += chunk; });
  let protocolError;
  child.stdout.on('data', chunk => {
    pending += chunk;
    let end;
    while ((end = pending.indexOf('\n')) >= 0) {
      const event = JSON.parse(pending.slice(0, end)); pending = pending.slice(end + 1);
      if (event.type === 'tool') { calls++; child.stdin.write(JSON.stringify({ id: event.id, result: { content: 'fixture-content' }, is_error: false }) + '\n'); }
      if (event.type === 'text') text += event.text;
      if (event.type === 'done') done = true;
      if (event.type === 'error') protocolError = event.error;
    }
  });
  const exit = new Promise(resolve => child.on('exit', resolve));
  child.stdin.write(JSON.stringify({ settings: { base_url: `http://127.0.0.1:${server.address().port}/v1`, api_key: 'fixture-key' },
    model: { id: 'fixture', name: 'fixture', context_window: 32768, max_output_tokens: 512, temperature: 0.3, output_parameter: 'max_completion_tokens' },
    tools: [{ function: { name: 'read_file', description: 'Read file', parameters: { type: 'object', properties: { path: { type: 'string' } }, required: ['path'] } } }], prompt: 'Read app.py then report.' }) + '\n');
  try {
    assert.equal(await exit, 0, protocolError || stderr);
    assert.equal(protocolError, undefined); assert.equal(done, true); assert.equal(calls, 1); assert.equal(text, 'verified'); assert.equal(requests, 2);
  } finally { child.kill(); server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
});
