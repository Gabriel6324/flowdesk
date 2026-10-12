# Independent model comparison

The dataset contains64 distinct fictional cases, eight per slice. Each slice has2 development and6 holdout cases:16/48 total. Inputs/knowledge, gold criteria and human rubric are separate files, frozen by manifest SHA-256. The model runner parses only input files; it hashes gold files for provenance but never puts their content in messages/tools.

Pilot selects one development case per slice and both strategies:16 trials, maximum40 requests. Full selects48 holdout cases, both strategies and two repeats:192 trials, maximum480 requests. Fixed-seed ordering is interleaved, one trial at a time. Repeats do not create more distinct cases and are not best-of-two selection.

## Safe preparation and protocol checks

```bash
go run -buildvcs=false ./cmd/flowdesk-eval --mode plan --plan pilot
go run -buildvcs=false ./cmd/flowdesk-eval --mode plan --plan full
go run -buildvcs=false ./cmd/flowdesk-eval --mode mock --plan pilot --out ./mock-pilot-new
python tools/score_eval.py mock-pilot-new/results.json --out ./mock-score-new
```

plan is the default and never sends requests. mock uses a local artificial protocol provider, deliberately bland answers, and no gold. demo is a separate local rule baseline. Neither can establish real model quality, semantic grounding or commercial savings.

Output directories must be new and private. Results checkpoint each trial, include failed and not-attempted trials, preserve null usage/cost, request counts, strategy, source/config/dataset/prompt hashes and human-review status. Gold is read only by the separate offline scorer.

## Explicit live execution

Live is intentionally absent from normal CI/application startup. It requires all of:

- `--mode live --acknowledge-cost`;
- a private `--config` AIConfig file, actual baseURL/model, output cap256–4096, deadline5–120s;
- `FLOWDESK_EVAL_API_KEY` from the operator's protected environment, never an argument or report;
- positive `--max-requests` and `--max-estimated-cost`, declared per-million pricing/currency;
- a new `--out` directory.

The live endpoint must be HTTPS/public and follows the application's network policy. Request bounds are enforced in the harness and at the shared provider dispatch boundary. workflow and agent use the same model, KB, prompt, output cap and job deadline, while agent may spend up to four requests. Cost is cumulative usage × declared rates; it is not an invoice and can exceed the estimate threshold with an in-flight request. Missing usage or quota failure stops subsequent live trials.

No live run or independent human grounding review has been conducted for this version.

## Scoring and human evidence

The scorer reports execution, category/priority agreement, clarification flag and citation identity. Broad allowed category sets in genuinely ambiguous slices are deliberate and should not be presented as precise classification accuracy. Priority uses a low/normal policy for these non-emergency synthetic cases; this does not establish production triage quality.

Grounding, required facts/questions, forbidden claims and scope require explicit pass/fail entries with reviewer, reason and rubric version. The tool generates an unreviewed template, does not guess semantic truth from keywords, and does not use the model to grade itself. Overall pass requires every prescribed field; unreviewed semantics cannot pass. Raw n/N and not-attempted coverage stay visible.

Review both repeats when reporting case-level reliability. Seeing holdout results and then tuning the system contaminates the frozen comparison; create and record a new holdout before describing it as independent validation. The generator is for initial dataset preparation, not silently regenerating a failed final dataset.

Synthetic tests do not establish user adoption, customer satisfaction, HR screening probabilities or real operational efficiency.

