#!/usr/bin/env node
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { createServer } from 'node:http';
import { mkdtemp, mkdir, readFile, writeFile, rm, access, cp } from 'node:fs/promises';
import { dirname, resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const flags = new Map();
for (let i = 2; i < process.argv.length; i += 2) {
  assert(['--pi-bin', '--go-bin', '--gomodcache', '--evidence'].includes(process.argv[i]) && process.argv[i + 1], 'expected --pi-bin PATH, --go-bin PATH, --gomodcache PATH and optional --evidence PATH');
  flags.set(process.argv[i], resolve(process.argv[i + 1]));
}
const pi = flags.get('--pi-bin');
if (!pi) throw new Error('MISSING_RUNTIME: pass --pi-bin pointing to the installed regular Pi executable');
await access(pi).catch(() => { throw new Error(`MISSING_RUNTIME: ${pi}`); });
assert(flags.has('--go-bin') && flags.has('--gomodcache'), 'pass the installed mise Go executable as --go-bin and an existing dependency cache as --gomodcache; downloads are disabled');
const temp = await mkdtemp('/tmp/opencode/regular-pi-smoke-');
const home = join(temp, 'home');
const agent = join(temp, 'agent');
const bin = join(temp, 'bin');
const cwd = join(temp, 'work');
for (const dir of [home, agent, bin, cwd, join(temp, 'tmp')]) await mkdir(dir, { recursive: true, mode: 0o700 });
const skillSource = join(root, '.agents/skills/timesheet');
const skill = join(temp, '.agents/skills/timesheet/SKILL.md');
await mkdir(join(dirname(skill), 'scripts'), { recursive: true, mode: 0o700 });
await cp(join(skillSource, 'SKILL.md'), skill);
await cp(join(skillSource, 'scripts/ts-env'), join(dirname(skill), 'scripts/ts-env'));
assert.equal(await readFile(skill, 'utf8'), await readFile(join(skillSource, 'SKILL.md'), 'utf8'), 'load an unchanged copy of the normal skill');
const config = join(temp, 'config.json');
const envFile = join(temp, 'fixture.env');
const stateFile = join(temp, 'state.json');
const auditFile = join(temp, 'argv.jsonl');
const ts = join(bin, 'timesheet');
const day = '2026-10-20';
const common = ['--config', config, '--env-file', envFile];
const env = {
  HOME: home, XDG_CONFIG_HOME: join(home, '.config'), XDG_CACHE_HOME: join(home, '.cache'),
  XDG_DATA_HOME: join(home, '.local/share'), PI_CODING_AGENT_DIR: agent,
  TMPDIR: join(temp, 'tmp'), PATH: `${bin}:/usr/bin:/bin`, TIMESHEET_ROOT: temp,
  SHELL: '/bin/bash', TERM: 'dumb', NO_COLOR: '1', LANG: 'C.UTF-8',
};
const children = new Set();
const trace = { fixture_backed: true, live_model: false, requests: [], mcp: [], reports: [], toolCalls: [] };
let success = false;
let providerError;
let server;
function run(exe, args, options = {}) {
  return new Promise((resolveRun, reject) => {
    const child = spawn(exe, args, { cwd, env, stdio: ['ignore', 'pipe', 'pipe'], ...options });
    children.add(child);
    let stdout = '', stderr = '';
    const timer = setTimeout(() => { child.kill('SIGKILL'); }, 120_000);
    child.stdout.on('data', chunk => { stdout += chunk; });
    child.stderr.on('data', chunk => { stderr += chunk; });
    child.on('error', reject);
    child.on('close', async code => {
      clearTimeout(timer);
      children.delete(child);
      if (exe === pi) {
        await writeFile(join(temp, 'pi.jsonl'), stdout);
        await writeFile(join(temp, 'pi.stderr'), stderr);
      }
      if (code !== 0) reject(new Error(`${exe} exited ${code}\n${stderr}\n${stdout}`));
      else resolveRun({ stdout, stderr });
    });
  });
}
const quote = value => `'${value.replaceAll("'", "'\\''")}'`;
function bash(argv) {
  assert(Array.isArray(argv) && argv[0] === ts, 'only the isolated audited binary may execute');
  assert(!['approve', 'submit', 'resolve-write'].includes(argv[1]), 'human-only invocation forbidden');
  const code = `require('node:fs').appendFileSync(${JSON.stringify(auditFile)},${JSON.stringify(JSON.stringify(argv.slice(1)) + '\n')});const{spawnSync}=require('node:child_process');const r=spawnSync(${JSON.stringify(argv[0])},${JSON.stringify(argv.slice(1))},{encoding:'utf8'});console.log('__SMOKE_JSON__'+JSON.stringify({stdout:r.stdout,stderr:r.stderr,status:r.status}));if(r.status!==0)process.exit(1)`;
  return { name: 'bash', args: { command: `node -e ${quote(code)}`, timeout: 60 } };
}
const next = () => bash([ts, 'next', ...common, '--date', day]);
let phase = 'start', report, draft, classifications, catalogIndex = 0, status, finalText;
const catalogs = new Map();
const textOf = value => typeof value === 'string' ? value : Array.isArray(value) ? value.map(v => v.text || '').join('\n') : '';
function shellResult(text) {
  const line = text.split('\n').find(v => v.includes('__SMOKE_JSON__'));
  assert(line, `missing subprocess receipt in ${text.slice(0, 1000)}`);
  const result = JSON.parse(line.slice(line.indexOf('__SMOKE_JSON__') + 14));
  assert.equal(result.status, 0, result.stderr);
  return result.stdout;
}
function decide(request) {
  assert(trace.toolCalls.length < 60, 'bounded Pi tool budget exhausted');
  const last = request.messages.at(-1);
  const text = textOf(last.content);
  trace.lastTool = { phase, role: last.role, text: text.slice(0, 4000) };
  if (phase === 'start') {
    assert(JSON.stringify(request.messages).includes(skill), 'normal skill must be advertised with its loaded absolute path');
    phase = 'skill';
    return { name: 'read', args: { path: skill } };
  }
  assert.equal(last.role, 'tool', 'Pi must execute the preceding built-in tool');
  assert(!text.includes('Error:'), text);
  if (phase === 'skill') {
    assert(text.includes('human_gates') && text.includes('draft_digest'), 'Pi must read the normal skill');
    phase = 'setup';
    const helper = join(dirname(skill), 'scripts/ts-env');
    return { name: 'bash', args: { command: `test "$(${quote(helper)})" = ${quote(ts)} && ${quote(ts)} next --help >/dev/null 2>&1 && printf 'isolated binary ready'`, timeout: 30 } };
  }
  if (phase === 'setup' || phase === 'action' || phase === 'answer') {
    if (phase === 'action') shellResult(text);
    if (phase === 'setup') assert(text.includes('isolated binary ready'), text);
    phase = 'next';
    return next();
  }
  if (phase === 'draft') {
    draft = JSON.parse(text.slice(text.indexOf('{'), text.lastIndexOf('}') + 1));
    assert(draft.blocks.length > 0, 'calendar must yield a real draft block');
    const descriptions = Object.fromEntries(draft.blocks.map(b => [b.block_id, 'Reviewed the synthetic implementation topic.']));
    phase = 'answer';
    return { name: 'write', args: { path: report.judgment.answer_file, content: JSON.stringify({ draft_digest: report.judgment.draft_digest, notes: 'Deterministic fixture judgment, not an LLM judgment.', descriptions }) } };
  }
  if (phase === 'catalog') {
    const receipt = JSON.parse(shellResult(text));
    assert(receipt.catalog_id && receipt.options?.length, 'live loopback catalog receipt required');
    catalogs.set(classifications[catalogIndex].description, receipt);
    catalogIndex++;
    if (catalogIndex < classifications.length) return bash([ts, 'classifications', ...common, '--query', classifications[catalogIndex].description]);
    const selections = Object.fromEntries(report.judgment.blocks.map(b => {
      const cat = catalogs.get(b.description);
      return [b.interval_id, { catalog_id: cat.catalog_id, classification_id: cat.options[0].id }];
    }));
    phase = 'answer';
    return { name: 'write', args: { path: report.judgment.answer_file, content: JSON.stringify({ ledger_digest: report.judgment.ledger_digest, selections }) } };
  }
  if (phase === 'status') {
    status = text;
    const receipt = JSON.parse(text.slice(text.indexOf('{'), text.lastIndexOf('}') + 1));
    assert.equal(receipt.date, day);
    assert(receipt.next_action, 'submit-status must report its next safe action');
    assert.deepEqual(receipt.approvals, []);
    assert.deepEqual(receipt.unresolved, []);
    trace.submitStatus = receipt;
    phase = 'done';
    finalText = `Fixture-backed Pi smoke only. No live-model comprehension was tested.\n${text}\n${report.human.approve_command}\n${report.human.submit_command}\nStopped before human-only writes.`;
    return { text: finalText };
  }
  assert.equal(phase, 'next');
  report = JSON.parse(shellResult(text));
  trace.reports.push(report);
  assert.equal(report.day, day);
  if (report.state === 'action') {
    assert(['run', 'record', 'stage', 'classify', 'save', 'plan'].includes(report.action.cmd[1]), 'refuse a non-staging action');
    phase = 'action';
    return bash(report.action.cmd);
  }
  if (report.state === 'judgment') {
    assert(report.judgment.answer_file.startsWith(temp + '/'), 'judgment path must be isolated');
    if (report.judgment.kind === 'block_descriptions') {
      assert(report.judgment.draft_digest);
      phase = 'draft';
      return { name: 'read', args: { path: join(dirname(report.judgment.answer_file), 'draft.json') } };
    }
    assert.equal(report.judgment.kind, 'classifications');
    assert(report.judgment.ledger_digest && report.judgment.blocks.length);
    classifications = [...new Map(report.judgment.blocks.map(b => [b.description, b])).values()];
    catalogIndex = 0;
    phase = 'catalog';
    return bash([ts, 'classifications', ...common, '--query', classifications[0].description]);
  }
  assert.equal(report.state, 'human_gates', report.reason);
  assert.equal(report.human.unresolved_writes, 0);
  assert.equal(report.human.native_unresolved_writes, 0);
  assert(report.human.status_command.includes(' submit-status '));
  assert(report.human.status_command.startsWith(quote(ts)) || report.human.status_command.startsWith(ts + ' '));
  trace.statusCommand = report.human.status_command;
  phase = 'status';
  return { name: 'bash', args: { command: report.human.status_command, timeout: 30 } };
}

try {
  const version = await run(pi, ['--version']);
  assert.equal(version.stdout.trim(), '1.0.0', `UNSUPPORTED_RUNTIME: expected qualified Pi 1.0.0, got ${version.stdout}`);
  trace.piVersion = version.stdout.trim();
  const go = flags.get('--go-bin');
  await run(go, ['build', '-trimpath', '-o', ts, './cmd/timesheet'], {
    cwd: root, env: { ...env, GOTOOLCHAIN: 'local', GOPROXY: 'off', GOSUMDB: 'off', GOCACHE: join(temp, 'gocache'), GOMODCACHE: flags.get('--gomodcache') },
  });
  server = createServer(async (req, res) => {
    try {
      assert(['127.0.0.1', '::ffff:127.0.0.1'].includes(req.socket.remoteAddress));
      if (req.url === '/calendar') {
        trace.requests.push({ method: req.method, path: req.url });
        res.end('BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//fixture//EN\r\nBEGIN:VEVENT\r\nUID:fixture@test.invalid\r\nDTSTAMP:20261020T000000Z\r\nDTSTART:20261020T090000Z\r\nDTEND:20261020T100000Z\r\nSUMMARY:Synthetic implementation review\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n');
        return;
      }
      let raw = '';
      for await (const chunk of req) { raw += chunk; assert(raw.length < 4_000_000); }
      const body = JSON.parse(raw);
      if (req.url === '/mcp') {
        trace.mcp.push(body);
        res.setHeader('Content-Type', 'application/json');
        if (body.id == null) { res.writeHead(202); res.end(); return; }
        let result;
        const names = ['timesheet__get_my_week', 'timesheet__get_classifications'];
        if (body.method === 'initialize') result = { protocolVersion: '2025-06-18', capabilities: {}, serverInfo: { name: 'read-only-smoke', version: '1' } };
        else if (body.method === 'tools/list') result = { tools: names.map(name => ({ name, inputSchema: { type: 'object' } })) };
        else {
          assert.equal(body.method, 'tools/call');
          assert(names.includes(body.params.name), 'remote fixture rejects all writes');
          result = body.params.name === names[0]
            ? { structuredContent: { days: [{ date: day, entries: [] }], weekStart: day, minDate: '2026-01-01', maxDate: '2026-12-31' }, content: [{ type: 'text', text: 'week' }] }
            : { structuredContent: { resources: [{ id: 'fixture-dev', kind: 'classification', label: 'Synthetic development' }] }, content: [{ type: 'text', text: '- Synthetic development (DEV) [Development] · fixture-dev' }] };
        }
        res.end(JSON.stringify({ jsonrpc: '2.0', id: body.id, result }));
        return;
      }
      assert.equal(req.url, '/v1/chat/completions');
      trace.requests.push({ method: req.method, path: req.url });
      const action = decide(body);
      if (action.name) {
        assert(body.tools.some(t => t.function.name === action.name), `missing Pi built-in ${action.name}`);
        trace.toolCalls.push(action);
      }
      res.setHeader('Content-Type', 'text/event-stream');
      const delta = action.text ? { content: action.text } : { tool_calls: [{ index: 0, id: `fixture_${trace.toolCalls.length}`, type: 'function', function: { name: action.name, arguments: JSON.stringify(action.args) } }] };
      for (const chunk of [{ role: 'assistant' }, delta]) res.write(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', created: 1, model: 'smoke', choices: [{ index: 0, delta: chunk, finish_reason: null }] })}\n\n`);
      res.write(`data: ${JSON.stringify({ id: 'fixture', object: 'chat.completion.chunk', created: 1, model: 'smoke', choices: [{ index: 0, delta: {}, finish_reason: action.text ? 'stop' : 'tool_calls' }] })}\n\ndata: [DONE]\n\n`);
      res.end();
    } catch (error) {
      providerError = error;
      trace.error = String(error);
      await writeFile(join(temp, 'trace.json'), JSON.stringify(trace, null, 2));
      res.writeHead(500, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ error: { message: error.message } }));
      for (const child of children) child.kill('SIGKILL');
    }
  });
  await new Promise(resolveListen => server.listen(0, '127.0.0.1', resolveListen));
  const base = `http://127.0.0.1:${server.address().port}`;
  await writeFile(config, JSON.stringify({ timezone: 'UTC', state_path: stateFile, projects: [{ id: 'WORK', name: 'Synthetic work', default_classification: 'DEV' }], sources: { enabled: true, calendar: { enabled: true, user: 'fixture@test.invalid' } }, gradion: { enabled: true, endpoint: `${base}/mcp`, token_env: 'GRADION_MCP_TOKEN' } }), { mode: 0o600 });
  await writeFile(envFile, `GCAL_ICS_URL=${base}/calendar\nGRADION_MCP_TOKEN=fixture-only\n`, { mode: 0o600 });
  await writeFile(join(agent, 'models.json'), JSON.stringify({ providers: { 'smoke-fixture': { baseUrl: `${base}/v1`, api: 'openai-completions', apiKey: 'fixture-only', models: [{ id: 'smoke', name: 'Deterministic smoke fixture', reasoning: false, input: ['text'], contextWindow: 128000, maxTokens: 4096 }] } } }));
  const result = await run(pi, ['--print', '--mode', 'json', '--no-session', '--no-extensions', '--no-context-files', '--offline', '--no-skills', '--skill', skill, '--tools', 'bash,read,write', '--provider', 'smoke-fixture', '--model', 'smoke', '--thinking', 'off', `Use the loaded timesheet skill to log ${day}. This is a deterministic fixture-backed verification. Use only ${ts}, config ${config}, and env-file ${envFile}. Never read the env file. Run next and follow each report. Stop at human_gates after reading submit-status. Do not run approve, submit, or resolve-write.`]);
  await writeFile(join(temp, 'pi.jsonl'), result.stdout);
  await writeFile(join(temp, 'pi.stderr'), result.stderr);
  if (providerError) throw providerError;
  assert.equal(phase, 'done');
  assert(status && result.stdout.includes('Stopped before human-only writes.'));
  const state = JSON.parse(await readFile(stateFile, 'utf8'));
  assert(state.days[day].confirmed.length > 0, 'real saved confirmed ledger required');
  assert(Object.values(state.plans).some(p => p.state === 'ready' && p.date === day), 'real ready plan required');
  for (const key of ['approvals', 'intents', 'executions']) assert.equal((state[key] || []).length, 0, `no ${key}`);
  const argv = (await readFile(auditFile, 'utf8')).trim().split('\n').map(JSON.parse);
  assert(!argv.some(a => ['approve', 'submit', 'resolve-write'].includes(a[0])));
  assert(trace.toolCalls.filter(t => t.name === 'bash').every(t => !/(?:^|[\s"'`])(?:approve|submit|resolve-write)(?=$|[\s"'`])/.test(t.args.command)), 'all emitted Pi bash calls are audited for forbidden executable verbs');
  for (const verb of ['run', 'record', 'stage', 'classifications', 'classify', 'save', 'plan']) assert(argv.some(a => a[0] === verb), `actual ${verb} invocation required`);
  assert(trace.statusCommand && status, 'reported submit-status must execute through Pi bash');
  assert(trace.mcp.some(r => r.params?.name === 'timesheet__get_my_week'));
  assert(trace.mcp.some(r => r.params?.name === 'timesheet__get_classifications'));
  assert(trace.mcp.filter(r => r.method === 'tools/call').every(r => ['timesheet__get_my_week', 'timesheet__get_classifications'].includes(r.params.name)), 'no remote write method');
  const judgments = trace.reports.filter(r => r.state === 'judgment');
  assert.deepEqual(trace.toolCalls.filter(t => t.name === 'read').map(t => t.args.path), [skill, join(dirname(judgments[0].judgment.answer_file), 'draft.json')], 'Pi reads only the normal skill and the reported draft');
  for (const call of trace.toolCalls.filter(t => t.name === 'write')) {
    const bound = judgments.find(r => r.judgment.answer_file === call.args.path)?.judgment;
    assert(bound, 'Pi writes only reported answer files');
    const answer = JSON.parse(call.args.content);
    const binding = bound.kind === 'block_descriptions' ? 'draft_digest' : 'ledger_digest';
    assert.equal(answer[binding], bound[binding], 'judgment binding copied from the actual report');
  }
  trace.assertions = { confirmed: state.days[day].confirmed.length, readyPlans: Object.values(state.plans).filter(p => p.state === 'ready').length, approvals: 0, intents: 0, executions: 0, prohibitedInvocations: 0 };
  trace.argv = argv;
  trace.finalText = finalText;
  success = true;
  console.log('PASS fixture-backed regular Pi runtime smoke. Not a live-model or skill-comprehension test.');
  console.log(JSON.stringify(trace.assertions));
} catch (error) {
  trace.error = String(providerError || error);
  console.error(`FAIL fixture-backed Pi smoke. ${trace.error}\nRetained diagnostics ${temp}`);
  process.exitCode = 1;
} finally {
  for (const child of children) child.kill('SIGKILL');
  if (server) { server.closeAllConnections(); await new Promise(resolveClose => server.close(resolveClose)); }
  await writeFile(join(temp, 'trace.json'), JSON.stringify(trace, null, 2));
  const evidence = flags.get('--evidence');
  if (evidence) {
    await mkdir(evidence, { recursive: true, mode: 0o700 });
    for (const name of ['trace.json', 'pi.jsonl', 'pi.stderr', 'argv.jsonl', 'state.json']) {
      try { await cp(join(temp, name), join(evidence, name)); } catch (error) { if (error.code !== 'ENOENT') throw error; }
    }
    console.log(`Evidence ${evidence}`);
  }
  if (success) await rm(temp, { recursive: true, force: true });
}
