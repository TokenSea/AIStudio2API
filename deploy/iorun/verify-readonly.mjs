// Linux helper on the private network with the executor volume at /engine:ro.
// No valid generation body is sent and no Google generating RPC is requested.
import { readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import assert from 'node:assert/strict';
const base = 'http://gemini-executor:2048';
const binding = JSON.parse(await readFile('/engine/iorun/binding.json', 'utf8'));
assert.equal(binding.studio_enabled, false);
const headers = { Authorization: `Bearer ${binding.token}`, 'X-IORun-Account-ID': binding.account_id, 'X-IORun-Check-ID': randomUUID() };
async function call(path, options = {}) {
  const r = await fetch(base + path, { headers, signal: AbortSignal.timeout(45000), ...options });
  return { status: r.status, body: await r.json(), cookies: r.headers.getSetCookie().map(s => s.split(';')[0]).join('; ') };
}
const ready = await call('/readiness');
assert.equal(ready.status, 200); assert.equal(ready.body.state, 'ready');
assert.equal(ready.body.account_id, binding.account_id);
assert.equal((await call('/readiness', { headers: { ...headers, 'X-IORun-Account-ID': randomUUID() } })).status, 403);
assert.equal((await call('/v1/images/generations', { method: 'POST', body: '{}' })).status, 403);
assert.equal((await call('/generate/studio', { method: 'POST', body: '{}' })).body.error.code, 'generation_paused');
const env = Object.fromEntries((await readFile('/engine/.env', 'utf8')).trim().split('\n').map(line => { const at = line.indexOf('='); return [line.slice(0, at), line.slice(at + 1)]; }));
const login = await call('/api/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username: env.ADMIN_USERNAME, password: env.ADMIN_PASSWORD }) });
assert.equal(login.status, 200);
const adminHeaders = { Cookie: login.cookies };
const accounts = await call('/api/accounts', { headers: adminHeaders });
const status = await call('/api/status', { headers: adminHeaders });
assert.equal((await call('/api/update', { method: 'POST', headers: adminHeaders })).status, 403);
const studio = await call('/iorun/studio-readiness');
console.log(JSON.stringify({ result: 'PASS', apps_state: ready.body.state, account_id: binding.account_id, account_isolation: true, public_generation_bypass_blocked: true, studio_disabled: true, updater_disabled: true, runtime: status.body.state, studio: studio.body, accounts: (Array.isArray(accounts.body) ? accounts.body : accounts.body.accounts || []).map(a => ({ account_matches: a.id === binding.google_account, enabled: a.enabled, state: a.state, benefit_tier: a.benefit_tier, model_count: a.models?.length || 0 })), google_generation_calls: 0 }));
await call('/api/auth/logout', { method: 'POST', headers: adminHeaders });
