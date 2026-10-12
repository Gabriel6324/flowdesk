#!/usr/bin/env python3
"""Offline scoring. Gold is never imported by the model runner. No LLM judge."""
import argparse,csv,hashlib,json
from pathlib import Path
p=argparse.ArgumentParser()
p.add_argument("results",type=Path);p.add_argument("--data",type=Path,default=Path("evaluation"));p.add_argument("--reviews",type=Path);p.add_argument("--out",type=Path,required=True)
a=p.parse_args();r=json.loads(a.results.read_text())
manifest=json.loads((a.data/"manifest.json").read_text())
goldfile=a.data/"gold/criteria.json"
actual=hashlib.sha256(goldfile.read_bytes()).hexdigest()
if actual!=manifest["sha256"]["gold/criteria.json"] or actual!=r["sha256"]["gold/criteria.json"]:raise SystemExit("Gold hash differs from frozen run.")
gold=json.loads(goldfile.read_text())["cases"]
reviewdoc=json.loads(a.reviews.read_text()) if a.reviews else {}
reviews=reviewdoc.get("trials",{})
rubric=json.loads((a.data/"rubric.json").read_text())
if reviews and reviewdoc.get("rubric_version")!=rubric["version"]:raise SystemExit("Review rubric version mismatch.")
fields=rubric["human_fields"]
rows=[];template={"rubric_version":rubric["version"],"trials":{}}
for t in r["trials"]:
 g=gold[t["case_id"]];o=t.get("output") or {};human=reviews.get(t["id"],{})
 state=human.get("verdicts",{})
 valid=bool(human.get("reviewer") and human.get("reason")) and all(state.get(f) in ("pass","fail") for f in fields)
 if not valid:state={f:"unreviewed" for f in fields}
 attempted=t["attempted"];execution=t["status"]=="succeeded"
 category=execution and o.get("suggested_category") in g["allowed_categories"]
 priority=execution and o.get("suggested_priority") in g["allowed_priorities"]
 clarification=execution and o.get("needs_more_information")==g["needs_more_information"]
 allowed={s["evidence_id"] for s in t.get("sources",[])};identities=execution and all(x in allowed for x in o.get("evidence_ids",[]))
 overall=("not_attempted" if not attempted else "fail" if not (execution and category and priority and clarification and identities) else "unreviewed" if not valid else "pass" if all(state[f]=="pass" for f in fields) else "fail")
 rows.append(dict(trial_id=t["id"],case_id=t["case_id"],slice=t["slice"],strategy=t["strategy"],repeat=t["repeat"],attempted=attempted,execution=execution,category=category,priority=priority,clarification=clarification,citation_identity=identities,overall=overall,error_code=t.get("error_code"),latency_ms=t.get("duration_ms"),actual_requests=len(t.get("calls",[])),estimated_cost=t.get("estimated_cost"),**state))
 template["trials"][t["id"]]={"reviewer":"","reason":"","verdicts":{f:"unreviewed" for f in fields}}
if a.out.exists():raise SystemExit("Scoring output must be a new directory.")
a.out.mkdir(parents=True,mode=0o700)
(a.out/"human-review-template.json").write_text(json.dumps(template,ensure_ascii=False,indent=2)+"\n")
with (a.out/"summary.csv").open("w",newline="") as f:
 writer=csv.DictWriter(f,fieldnames=list(rows[0]));writer.writeheader();writer.writerows(rows)
groups={}
for row in rows:
 key=(row["slice"],row["strategy"]);groups.setdefault(key,[]).append(row)
lines=["# FlowDesk evaluation record","",f"Mode: **{r['mode']}**. Plan: {r['plan']}. Distinct cases: {r['distinct_cases']}.",f"Planned trials: {len(rows)}; attempted: {sum(x['attempted'] for x in rows)}; not attempted: {sum(not x['attempted'] for x in rows)}.",f"Actual provider requests: {r['actual_requests']}. Stop reason: {r['stop_reason'] or 'none'}.","", "Mock/demo outputs measure execution only. This record does not demonstrate real model quality. Human grounding, required facts, forbidden claims and scope require a named review.","","| Slice | Strategy | Planned | Attempted | Execution | Label | Clarification | Human reviewed | Overall pass |","| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |"]
for (sl,st),rr in sorted(groups.items()):
 n=len(rr);attempted=sum(x["attempted"] for x in rr);num=lambda fn:sum(fn(x) for x in rr)
 lines.append(f"| {sl} | {st} | {n} | {num(lambda x:x['attempted'])}/{n} | {num(lambda x:x['execution'])}/{attempted} | {num(lambda x:x['category'] and x['priority'])}/{attempted} | {num(lambda x:x['clarification'])}/{attempted} | {num(lambda x:x['grounded']!='unreviewed')}/{attempted} | {num(lambda x:x['overall']=='pass')}/{attempted} |")
lines+=["","Case-level repeat reliability requires **both** repeated trials to pass; no best-of-two selection.",f"Frozen source: {r['source_hash']}. Dataset: {r['dataset_version']}. Prompt: {r['prompt_version']}. Retrieval: {r['retrieval_policy']}.","Missing usage/cost stays null. Pricing estimates are not invoices. Synthetic distribution has no production/generalisation claim."]
(a.out/"REPORT.md").write_text("\n".join(lines)+"\n")
(a.out/"scores.json").write_text(json.dumps({"mode":r["mode"],"planned":len(rows),"rows":rows},ensure_ascii=False,indent=2)+"\n")
print(f"Offline scoring saved to {a.out}. Human verdicts were not inferred from text.")

