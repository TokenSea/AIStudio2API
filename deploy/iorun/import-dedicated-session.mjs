// Linux helper: stream the dedicated Google session into the private volume.
import { readFile, writeFile, mkdir, chown } from 'node:fs/promises';
const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const imported = JSON.parse(Buffer.concat(chunks));
const binding = JSON.parse(await readFile('/data/iorun/binding.json', 'utf8'));
if (imported.email !== binding.google_account || imported.account_id !== binding.account_id || !/^[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}$/.test(imported.email)) throw Error('session_account_mismatch');
const dir = `/data/auth/${imported.email}`;
await mkdir(dir, { mode: 0o700 }); await chown(dir, 1001, 1001);
const config = { label: imported.email, enabled: true, proxy: '', locale: 'en-US', timezone: 'America/Los_Angeles', auth_user: 0 };
for (const [file, value] of [['account.json', config], ['storage-state.json', imported.state]]) {
  await writeFile(`${dir}/${file}`, JSON.stringify(value), { mode: 0o600, flag: 'wx' }); await chown(`${dir}/${file}`, 1001, 1001);
}
console.log(JSON.stringify({ session_imported: true, account_id: binding.account_id, studio_enabled: false }));
