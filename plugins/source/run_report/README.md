# run_report

Emits one entry per traced pipeline run within `window` — the "what happened
and why" feed built on the run inspector's trace store. Each entry's title is
a one-line summary (`tv: 1 accepted, 3 rejected, 1 failed`) and its fields
carry the counts plus the top three rejection reasons, so a `notify` sink
downstream becomes a weekly activity report.

Entries have stable `pipeliner://run/<task>/<run_id>` URLs: put a URL-keyed
[`seen`](../../processor/filter/seen/README.md) filter downstream to report
each run exactly once across report-pipeline runs.

## Config

| Key | Required | Default | Description |
|-----|----------|---------|-------------|
| `window` | no | `168h` | How far back to report |
| `tasks` | no | all traced pipelines | Pipelines to report on |
| `include_dry` | no | false | Include dry-runs |

## Fields set on each entry

| Field | Description |
|-------|-------------|
| `report_task` / `report_run_id` | Which run this entry summarizes |
| `report_accepted` / `report_rejected` / `report_failed` | Final-state counts |
| `report_top_rejects` | Up to three most common rejection reasons ("reason (n); …") |

Traces are bounded (last 20 runs per pipeline, 500 entries per run), so the
report window effectively covers what the inspector covers.
