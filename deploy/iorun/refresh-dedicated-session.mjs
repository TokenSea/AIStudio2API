// Pipe only export-dedicated-session.mjs stdout here, on the private network.
// Verify the Google identity through the native import API before replacing
// its session. Never mutate runtime entitlement/authentication flags by hand.
import { readFile } from 'node:fs/promises';
const chunks = []; for await (const chunk of process.stdin) chunks.push(chunk);
const incoming = JSON.parse(Buffer.concat(chunks));
const binding = JSON.parse(await readFile('/engine/iorun/binding.json', 'utf8'));
if (binding.studio_enabled || incoming.email !== binding.google_account || incoming.account_id !== binding.account_id) throw Error('session_binding_mismatch');
const env = Object.fromEntries((await readFile('/engine/.env', 'utf8')).trim().split('\n').map(line => { const at = line.indexOf('='); return [line.slice(0, at), line.slice(at + 1)]; }));
let cookie = '';
async function call(path, body) {
  const r = await fetch('http://gemini-executor:2048' + path, { method: body ? 'POST' : 'GET', headers: { 'Content-Type': 'application/json', Cookie: cookie }, body: body ? JSON.stringify(body) : undefined, signal: AbortSignal.timeout(45000) });
  const cookies = r.headers.getSetCookie(); if (cookies.length) cookie = cookies.map(c => c.split(';')[0]).join('; ');
  const text = await r.text(); const data = text ? JSON.parse(text) : null;
  if (!r.ok) throw Error(`account_operation_http_${r.status}`);
  return data;
}
await call('/api/auth/login', { username: env.ADMIN_USERNAME, password: env.ADMIN_PASSWORD });
try {
  const cookies = JSON.stringify(incoming.state);
  const discovery = await call('/api/accounts/import/cookies/discover', { cookies });
  const matches = discovery.accounts.filter(a => a.email?.toLowerCase() === binding.google_account && !a.signed_out && !a.restricted && !a.error);
  if (matches.length !== 1) throw Error('google_identity_unconfirmed');
  const task = await call('/api/accounts/import/cookies', { items: [{ email: binding.google_account, auth_user: matches[0].auth_user || 0, cookies }], locale: 'en-US', timezone: 'America/Los_Angeles' });
  const id = task.id || task.task?.id;
  if (!id) throw Error('import_task_missing');
  const deadline = Date.now() + 120000;
  let result;
  while (Date.now() < deadline) {
    result = (await call('/api/accounts/tasks')).tasks.find(t => t.id === id);
    if (result?.state === 'done' || result?.state === 'failed') break;
    await new Promise(resolve => setTimeout(resolve, 1000));
  }
  if (result?.state !== 'done') throw Error('session_import_unconfirmed');
  console.log(JSON.stringify({ refreshed: true, account_id: binding.account_id, identity_verified: true, studio_enabled: false, google_generation_calls: 0 }));
} finally { await call('/api/auth/logout', {}); }
