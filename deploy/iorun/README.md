# IORun managed Gemini worker

This fork remains based on Mag1cFall/AIStudio2API under its MIT license. Upstream source and attribution are retained.

IORun is the single consumer API, account authority, allowlist, original-request ledger and reconciliation owner. The fork exposes one private worker binding under `/iorun/`:

- `/generate/gemini-pro` and `/readiness` delegate to the existing, isolated Gemini Apps worker. Its profile, receipts and completed originals remain intact.
- `/generate/studio` uses the AIStudio2API protocol runtime after verifying the exact Google account, Pro tier, subscription access mode and native image dimensions. It is disabled until explicitly enabled in the operator binding.
- `/studio-readiness` inspects the dedicated Studio account; `/receipts/:id` returns only original metadata. Neither generates an image.

Consumers keep the existing IORun address and Key. There is no automatic transition between providers or fallback into GPT, Google project APIs, another account or another image size.

Native IORun's worker URL remains a root URL (`http://gemini-executor:2048`). Authenticated `/generate/…` and `/readiness` aliases use the same handler and account lock as `/iorun/…`; they do not create a second worker or API identity.

## Deployment

Build `Dockerfile.iorun` on isolated Linux, then use this compose file on ciorun03. The named volume contains the dedicated Gemini state only. Do not mount a host home, Docker socket, GPT credentials or a personal browser profile. The management port is host loopback only and should be reached through SSH. Do not publish the upstream management/API port.

For an incremental test build, `build.sh` uses the same release flags with an isolated prior builder's compiler cache. Package its executable with `Dockerfile.layer`, passing the exact previously built runtime image ID as `RUNTIME_BASE`; record the binary SHA-256 and final image ID. A clean `Dockerfile.iorun` build remains the reproducible full-build entry.

Before startup, an operator prepares `/data/.env` and `/data/iorun/binding.json` with mode 0600. The binding fields are `token` (existing IORun worker token), `account_id` (existing IORun UUID), `google_account` (exact dedicated Google account), `legacy_url` (private existing worker), `studio_enabled` (initially false), and `receipt_dir` (`/data/iorun/receipts`). Never commit these files.

`provision.mjs` verifies the existing native/browser binding before creating those files. The paired session export/import scripts transfer only Google cookies from the dedicated, identity-checked browser directly into this private volume; their stream is secret and must never be logged. They do not navigate, generate, export GPT cookies or replace the original profile. `adopt-worker.mjs` checks readiness and changes only the native worker URL after originals have drained. Gracefully restart only the native platform; retain its worker/account UUID, Token, allowlist, database, all old receipts and browser container identity. Do not run an initializer or seed script.

Use one account and one slot. Configure `UPSTREAM_CHANNELS=playground`, `PER_ACCOUNT_CONCURRENCY=1`, `WARM_WORKER_LIMIT=1`, `MAX_ACTIVE_WORKERS=1`, `MAX_ACCOUNT_SWITCHES=0`, `FIRST_EVENT_HEDGE=off`, `FAKE_STREAM=false`, `NONSTREAM_KEEPALIVE=false`, `BUILD_NATIVE_NONSTREAM=false`, `DISCOVER_HIDDEN_MODELS=false`, `REQUEST_BODY_LOG=false`, `LOG_FILE=false`, `MEDIA_OUTPUT=inline`, and authenticated management. The worker rejects generation on ordinary public protocol routes in managed mode.

The managed dispatch permit is checked immediately before the upstream send. It persists the dispatch receipt before allowing a single attempt and rejects authentication replay/continuation/account changes. A prepared, dispatched or unconfirmed receipt blocks new Studio requests after restart. Never delete a receipt to unlock the account. Old IDs return 409; receipt queries do not return images or prompts. Disconnects keep the original bounded execution running.

Missing, estimated or derived usage is omitted from the worker response and remains unconfirmed in IORun. Native upstream usage is forwarded only with modality details; IORun performs its existing consistency validation and accepted-price reconciliation. No new billing system is introduced.

The managed runtime blocks automatic AcceptTerms and alternate generating RPCs. Google identity verification, Terms and additional Drive authorization are not silently accepted. Model availability and account entitlement must be verified in the target environment; a running container alone is not successful image generation.

## Offline checks

Build `deploy/iorun/Dockerfile.test` after the dependency stage, then run `sh deploy/iorun/test.sh` with network disabled and the source mounted read-only. This uses the GNU native libraries used by upstream's Linux tests. The Zig/musl test binary crashed in C `setenv` during `testing.T.Setenv` on this host; it is not counted as a passing test. The deployed release binary requires separate health/readiness validation. Tests cover 1K/2K/4K fixture images, zero images, estimated usage, subscription/model/account rejection, sync response, duplicate requests, disconnect, restart with unknown execution, metadata queries, and the existing Apps adapter. They never contact Google.
