// Run in a Linux helper container. Read the existing dedicated Gemini browser
// binding on stdin; mount the native worker private volume read-only at /old.
// No Google credentials or IORun secrets are printed or placed in source.
import { readFile, writeFile, mkdir, chown, chmod, access } from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const browser = JSON.parse(Buffer.concat(chunks));
const native = JSON.parse(await readFile('/old/worker-account2.json', 'utf8'));
if (!native.token || native.token !== browser.token || native.account !== browser.account || !browser.expectedEmail) throw Error('dedicated_binding_mismatch');
for (const path of ['/data/.env', '/data/iorun/binding.json']) {
  try { await access(path); throw Error('already_provisioned'); } catch (e) { if (e.code !== 'ENOENT') throw e; }
}
for (const path of ['/data', '/data/iorun', '/data/iorun/receipts', '/data/auth']) {
  await mkdir(path, { recursive: true, mode: 0o700 }); await chown(path, 1001, 1001); await chmod(path, 0o700);
}
const binding = { token: native.token, account_id: native.account, google_account: browser.expectedEmail.toLowerCase(), legacy_url: 'http://account2-browser:8091', studio_enabled: false, receipt_dir: '/data/iorun/receipts' };
const env = {
  LISTEN_ADDR: '0.0.0.0:2048', PREVIEW_LISTEN_ADDR: '127.0.0.1:2050',
  AISTUDIO_AUTH_STATES: 'auth', PROXY_API_KEY: randomBytes(32).toString('hex'),
  ADMIN_AUTH_ENABLED: 'true', ADMIN_USERNAME: 'iorun-admin', ADMIN_PASSWORD: randomBytes(24).toString('hex'),
  AUTO_START: 'true', UPSTREAM_CHANNELS: 'playground', PER_ACCOUNT_CONCURRENCY: '1',
  WARM_WORKER_LIMIT: '1', MAX_ACTIVE_WORKERS: '1', WARM_STARTUP_CONCURRENCY: '1',
  MAX_ACCOUNT_SWITCHES: '0', FIRST_EVENT_HEDGE: 'off', FAKE_STREAM: 'false',
  NONSTREAM_KEEPALIVE: 'false', BUILD_NATIVE_NONSTREAM: 'false', DISCOVER_HIDDEN_MODELS: 'false',
  REQUEST_BODY_LOG: 'false', LOG_FILE: 'false', MEDIA_OUTPUT: 'inline', REQUEST_TIMEOUT: '5m',
  WAA_BACKEND: 'spidermonkey', GOOGLE_SEARCH: 'false', YOUTUBE_LINKS: 'false',
};
for (const [path, data] of [['/data/.env', Object.entries(env).map(([k,v]) => `${k}=${v}`).join('\n') + '\n'], ['/data/iorun/binding.json', JSON.stringify(binding) + '\n']]) {
  await writeFile(path, data, { flag: 'wx', mode: 0o600 }); await chown(path, 1001, 1001);
}
console.log(JSON.stringify({ provisioned: true, studio_enabled: false, account_id: binding.account_id }));
