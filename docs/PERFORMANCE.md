# Local performance record

Source: **9256df8e32d5452abbd90802beb5873a9b06fc3afe94d710d1aabfd8464ef7d7**; recorded2026-10-12T12:25:42Z. go1.27.2, linux/amd64, SQLite3.53.4.
Reported logical CPUs:9, GOMAXPROCS:8, cgroup CPU max:800000 100000, memory max:8589934592.

Dataset:1000 fictional tickets and100 public KB articles. Pre-created authenticated session, real net/http/application/SQLite in one isolated temporary process. Closed-loop mix:70% list,20% independent priority writes including revision-refresh GET,10% dashboard. Idempotency-cache retention is omitted in sustained writes and is covered separately by correctness tests. Random seed20261011.

Each level uses 10s warmup; measurement windows: [30, 30, 30]seconds. Requests stop at the window boundary; already-started requests drain for up to5s. Raw statuses including network/timeout0 are retained. RPS below counts HTTP requests, not logical user operations.

| Clients | HTTP requests | Requests/s | p50 ms | p95 ms | p99 ms | Status counts | Timeouts | Drain s |
| ---: | ---: | ---: | ---: | ---: | ---: | --- | ---: | ---: |
| 1 | 6831 | 227.7 | 1.666 | 7.055 | 18.253 | {'200': 6831} | 0 | 0.187 |
| 10 | 17487 | 582.9 | 13.217 | 40.012 | 67.290 | {'200': 17487} | 0 | 0.007 |
| 25 | 25925 | 864.2 | 24.967 | 63.534 | 89.871 | {'200': 25925} | 0 | 0.018 |

PBKDF2-HMAC-SHA256600000 iterations, ten sequential measured derivations after warmup: median234.01ms. This is neither login throughput nor a hash-concurrency test.
Sampled shared-process peaks: heap11901832bytes, Linux RSS40148992bytes, goroutines145; both client and server are included.

## Artificial queue phase

Two workers,50-active cap, local provider250ms artificial delay. Submitted60; enqueue statuses{'202': 50, '429': 10}; accepted50; terminal states{'succeeded': 50}; recorded calls50; elapsed6.909s; queue empty after drain:True.

These are local closed-loop measurements on this environment, without open-loop arrivals, TLS proxy overhead or a real provider. They do not establish production capacity, simultaneous-user promises, real model speed or commercial benefit.

Reproduce from source: build frontend, then go run -buildvcs=false ./cmd/flowdesk-bench --warmup 10s --duration 30s --concurrency 1,10,25 --out new-bench.json. Raw per-request samples are in the separate verification evidence archive; the source package keeps the summary.
