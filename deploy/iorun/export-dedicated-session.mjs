// Run inside the existing IORun dedicated Gemini browser container only.
// Stdout is sensitive: pipe directly into import-dedicated-session.mjs in the
// new private volume. Do not run interactively, capture, or log its output.
import { readFile } from 'node:fs/promises';
import { randomUUID } from 'node:crypto';
import { chromium } from '/opt/studio/node_modules/playwright-core/index.mjs';
const binding = JSON.parse(await readFile('/state/iorun-manual-worker/binding.json', 'utf8'));
const response = await fetch('http://127.0.0.1:8091/readiness', { headers: {
  Authorization: `Bearer ${binding.token}`, 'X-IORun-Account-ID': binding.account, 'X-IORun-Check-ID': randomUUID(),
}, signal: AbortSignal.timeout(20000) });
const readiness = await response.json();
if (!response.ok || readiness.state !== 'ready' || readiness.account_id !== binding.account) throw Error('dedicated_account_not_ready');
const browser = await chromium.connectOverCDP('http://127.0.0.1:9222');
const contexts = browser.contexts();
if (contexts.length !== 1) throw Error('ambiguous_browser_context');
const cookies = (await contexts[0].cookies()).filter(c => {
  const domain = c.domain.replace(/^\./, '').toLowerCase();
  return domain === 'google.com' || domain.endsWith('.google.com');
});
if (!cookies.some(c => c.name === '__Secure-1PSID')) throw Error('google_session_missing');
process.stdout.write(JSON.stringify({ account_id: binding.account, email: binding.expectedEmail.toLowerCase(), state: { cookies, origins: [] } }));
await browser.close(); // Disconnects the CDP client; does not close the browser.
