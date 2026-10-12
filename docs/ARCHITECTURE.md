# Architecture

FlowDesk uses a modular Go application, not independently deployed microservices. React assets are embedded in the same executable; browser requests and API responses share one origin. SQLite stores the workspace, sessions, business entities, job state, source snapshots, model calls and audits.

## Boundaries

| Module | Responsibility |
| --- | --- |
| cmd/flowdesk | profiles, instance lock, offline commands, shutdown and HTTP lifecycle |
| internal/httpapi | same-origin routing, strict JSON/contracts, safe envelopes and exports |
| internal/service | authorization, CAS/idempotency and short business transactions |
| internal/repository | checked schema, bound SQL, WAL, snapshots and crash recovery |
| internal/worker | two workers, SQL claims, cancellation and bounded SQL-only pending saves |
| internal/ai | deterministic retrieval, provider validation and bounded read-only strategies |
| web/src | role-specific work, notices, source review and explicit publishing |
| cmd/flowdesk-eval / flowdesk-bench | independent synthetic evidence; never the user's workspace |

## Ticket and review correctness

Ticket content, public conversation and processing cycle form the AI input version tuple. An internal note does not change that tuple; public information does. Reopening starts a new cycle and preserves previously published replies.

A general ticket revision protects edits by compare-and-swap. A draft freezes the input version, model configuration and supplied knowledge excerpts. Its review token hashes the current source versions and required acknowledgements. Changing a source invalidates the old confirmation token without rewriting the old snapshot. Publishing checks authorization, ticket revision, input tuple, draft identity, absence of an active job and fresh review context in one transaction. Published text remains exactly the confirmed text.

## Task and failure correctness

Jobs persist as queued/running/succeeded/failed/canceled/stale. Workers claim from SQL; an in-memory wake signal is only a hint. Active jobs are limited globally and per ticket. A call intent and its source snapshot commit before sending provider HTTP. Every subsequent request and finalization rechecks actor permission and input version.

Network and database cannot guarantee exactly-once paid execution. There are no automatic provider retries. On restart, previously running jobs become interrupted failures; queued jobs remain eligible. A cancellation's committed state takes precedence over late output.

When a valid result cannot commit, up to two pending results stay in memory and new paid work pauses. 「重试保存」 retries SQL only. If the process dies before saving, memory output is lost, and restart records an interruption without reissuing the provider request. Commit acknowledgement failures trigger an independent durable-state check; unreadable outcomes remain unknown.

Mutation idempotency is scoped to actor + method/concrete resource path + hashed client key, with a canonical typed body hash. The safe response commits in the same transaction. Authorization/resource scope is rechecked before replay. Entries last24 hours, with500 retained operations per actor; an explicit limit prevents unbounded growth.

## AI and security

Retrieval policy `keyword-bigram-v1` scores exact normalized words/Chinese bigrams, weights title/tags3 and body1, and orders ties by articleID. No match yields no forced source. This is deterministic lexical retrieval, not vector or BM25.

workflow supplies up to3 relevant excerpts in one request. agent can call only `get_current_ticket` and `search_knowledge`, with at most4 HTTP requests and6 tools. The entire tool group is validated before any tool runs. Tool messages remain paired, and exhausted tools are omitted from the next request. Inputs stop before dispatch if above20,000 Unicode scalars or128KiB serialized bytes. Output must match the strict schema; evidence IDs must be from the actual source union.

Sessions contain an opaque cookie token; only its hash is stored. Credentials and authorization epochs invalidate old sessions and close login races. PBKDF2-HMAC-SHA256 uses600,000 iterations; online hashing has bounded concurrency and wait. Email identifiers use a documented ASCII dot-atom subset and are trimmed/lowercased.

Keys use AES-GCM with a separate private master-key file. Desktop listens only on IPv4 loopback. Server uses a fixed HTTPS origin, explicit trusted proxy CIDRs and an explicit HTTPS model host:port allowlist. Validated DNS IPs are checked for private/local/metadata addresses and pinned for the request; redirects and environment proxies are disabled. Logs contain safe route/status/timing identifiers, never provider raw bodies, model headers or user text.

OS account access remains the local boundary: someone controlling the workspace files can read business data and perform offline recovery. No claim of resistance to the local administrator is made.


Startup uses canonical local file URIs across Windows/Linux and read-only schema inspection before writes. A pristine interrupted first launch can resume; a workspace with missing metadata and existing business rows cannot be reseeded. Schema creation and initial seed commit in one transaction. The diagnose command never initializes business data. Schema1 remains unchanged between0.1.0 and0.1.1.
