# FlowDesk evaluation record

Mode: **mock**. Plan: pilot. Distinct cases: 8.
Planned trials: 16; attempted: 16; not attempted: 0.
Actual provider requests: 24. Stop reason: none.

Mock/demo outputs measure execution only. This record does not demonstrate real model quality. Human grounding, required facts, forbidden claims and scope require a named review.

| Slice | Strategy | Planned | Attempted | Execution | Label | Clarification | Human reviewed | Overall pass |
| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| absent_knowledge | agent | 1 | 1/1 | 1/1 | 1/1 | 0/1 | 0/1 | 0/1 |
| absent_knowledge | workflow | 1 | 1/1 | 1/1 | 1/1 | 0/1 | 0/1 | 0/1 |
| account | agent | 1 | 1/1 | 1/1 | 0/1 | 0/1 | 0/1 | 0/1 |
| account | workflow | 1 | 1/1 | 1/1 | 0/1 | 0/1 | 0/1 | 0/1 |
| billing | agent | 1 | 1/1 | 1/1 | 0/1 | 0/1 | 0/1 | 0/1 |
| billing | workflow | 1 | 1/1 | 1/1 | 0/1 | 0/1 | 0/1 | 0/1 |
| conflicting_sources | agent | 1 | 1/1 | 1/1 | 0/1 | 1/1 | 0/1 | 0/1 |
| conflicting_sources | workflow | 1 | 1/1 | 1/1 | 0/1 | 1/1 | 0/1 | 0/1 |
| injection_scope | agent | 1 | 1/1 | 1/1 | 1/1 | 0/1 | 0/1 | 0/1 |
| injection_scope | workflow | 1 | 1/1 | 1/1 | 1/1 | 0/1 | 0/1 | 0/1 |
| insufficient_info | agent | 1 | 1/1 | 1/1 | 1/1 | 1/1 | 0/1 | 0/1 |
| insufficient_info | workflow | 1 | 1/1 | 1/1 | 1/1 | 1/1 | 0/1 | 0/1 |
| multi_issue | agent | 1 | 1/1 | 1/1 | 1/1 | 1/1 | 0/1 | 0/1 |
| multi_issue | workflow | 1 | 1/1 | 1/1 | 1/1 | 1/1 | 0/1 | 0/1 |
| technical | agent | 1 | 1/1 | 1/1 | 0/1 | 0/1 | 0/1 | 0/1 |
| technical | workflow | 1 | 1/1 | 1/1 | 0/1 | 0/1 | 0/1 | 0/1 |

Case-level repeat reliability requires **both** repeated trials to pass; no best-of-two selection.
Frozen source: 9256df8e32d5452abbd90802beb5873a9b06fc3afe94d710d1aabfd8464ef7d7. Dataset: synthetic-support-v1. Prompt: support-v1. Retrieval: keyword-bigram-v1.
Missing usage/cost stays null. Pricing estimates are not invoices. Synthetic distribution has no production/generalisation claim.
