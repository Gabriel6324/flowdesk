# FlowDesk 0.1.2 verification

Source input SHA-256: **9256df8e32d5452abbd90802beb5873a9b06fc3afe94d710d1aabfd8464ef7d7**.
Build: 2026-10-12T12:24:15Z; go version go1.27.2 linux/amd64; Nodev24.19.0. Actual SQLite3.53.4.

Go vet and race tests passed: **60 top-level tests, 84 leaf cases** across 10 tested packages. Parent/subtest pass events total93; these counting methods must not be added together. Frontend: **32 tests across 11 files** passed in Vitest/jsdom, with strict TypeScript/Vite production build. 

| Area | Actual evidence |
| --- | --- |
| Business/permissions | requester scope, internal-note filtering, CAS race, exact reply/cycle history, last administrator, revoked epochs |
| Durable mutations | concurrent same-key replay; applied/not-applied/unreadable commit fault injections; no duplicate creation |
| AI/worker | production engine against local HTTP mock, pre-HTTP durable intent and frozen sources, persisted usage/cleared secret, read-only tool bounds and malformed groups |
| Failures | body/header timeout, refusal with available usage, invalid usage, unsafe endpoint/redirect, SQL-only pending-save retry, canceled late output, restart without paid replay |
| Contract | 37 route operations, request/query strictness, real response schemas and generated TypeScript drift check |
| Frontend behavior | new history above older blocks, preserving manual edit, fresh review acknowledgements, exact preview submission, prominent publish success, atomic terminal-job refresh for admin/staff, personal-login isolation, Unicode text import with explicit save, reminder details/X, modal focus, disabled reason, old-session response/export suppression |
| Backup/recovery | canonical Windows file URI, interrupted empty/schema-only initialization, no reseed after lost metadata, read-only diagnosis, WAL snapshot, schema/profile rejection without modifying DB/key, OS lock, three recovery crash stages and missing-temp stop |
| Native Linux process | 41 executable/HTTP checks, including embedded assets, seed/login/review, session/history after restart, backup, offline password/key recovery, server bootstrap/cookie attributes, startup diagnostics and 0.1.0 upgrade; additional personal-workspace account/model/tool/failure/restart checks and genuine 0.1.1 upgrade |

The process smoke used an extracted Linux package and direct HTTP requests, **not a real browser**. Server TLS termination/secure-cookie browser behavior remains a container/platform gate.

## AI evidence

Frozen synthetic dataset:64 distinct cases (16 development/48 holdout). Mock pilot:16 trials and 24 local protocol requests; every trial, failure and unattempted slot is retained. Human semantic fields remain unreviewed. No paid provider call or true-model quality result is claimed.

## Pending gates

- Headless Chromium: 15 actual browser checks passed; see browser-smoke.json.
- Native Windows first launch, Unicode path, console input, Ctrl+C and persistence.
- Docker images/digests, clean-room TLS, volume permissions, restart and recovery.
- Real-model pilot/full holdout and independent human review.
- Public hosting and remote GitHub Actions execution.

Linux execution, cross-compilation, jsdom and mocks do not replace these gates. This is a usable technical first version with the listed limits; successful tests do not establish zero bugs.

Raw safe evidence: [verification-summary.json](evidence/verification-summary.json), [Go race events](evidence/go-race.jsonl), [process smoke](evidence/process-smoke.json), [personal workspace](evidence/personal-workspace-smoke.json), [mock pilot](evidence/mock-pilot.json), [performance summary](evidence/performance-summary.json).
