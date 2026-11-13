// Linux helper on the private network. Mount the native private volume at
// /private and this executor's volume read-only at /engine. Native originals
// must be drained before running, then gracefully restart only the platform.
import { readFile, open, rename } from 'node:fs/promises';
import { randomUUID, createHash } from 'node:crypto';
const binding = JSON.parse(await readFile('/engine/iorun/binding.json', 'utf8'));
const original = await readFile('/private/config.yaml', 'utf8');
const config = JSON.parse(original);
const account = JSON.parse(await readFile('/private/worker-account2.json', 'utf8'));
if (config.tokensea.gemini_web.studio_models_enabled !== false || binding.studio_enabled !== false) throw Error('studio_must_remain_closed');
const worker = config.tokensea.gemini_web.workers.find(w => w.id === account.id);
if (!worker || worker.account_id !== binding.account_id || worker.token !== binding.token || account.token !== binding.token || account.account !== binding.account_id) throw Error('binding_mismatch');
const target = 'http://gemini-executor:2048';
if (worker.url !== 'http://account2-relay:8090' && worker.url !== target) throw Error('unexpected_current_worker');
const check = randomUUID();
const response = await fetch(target + '/readiness', { headers: { Authorization: `Bearer ${binding.token}`, 'X-IORun-Account-ID': binding.account_id, 'X-IORun-Check-ID': check }, signal: AbortSignal.timeout(20000) });
const readiness = await response.json();
if (!response.ok || readiness.version !== 1 || readiness.check_id !== check || readiness.account_id !== binding.account_id || readiness.state !== 'ready' || readiness.source !== 'google-gemini-web') throw Error('executor_not_ready');
worker.url = target;
account.url = target;
async function atomic(path, value) {
  const file = await open(path + '.tmp', 'wx', 0o600);
  try { await file.writeFile(JSON.stringify(value) + '\n'); await file.sync(); } finally { await file.close(); }
  await rename(path + '.tmp', path);
}
await atomic('/private/config.yaml', config);
await atomic('/private/worker-account2.json', account);
const directory = await open('/private'); await directory.sync(); await directory.close();
console.log(JSON.stringify({ adopted: true, worker_id: account.id, account_id: binding.account_id, url: target, previous_config_sha256: createHash('sha256').update(original).digest('hex'), studio_enabled: false }));
