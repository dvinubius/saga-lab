# Documentation routing

This index helps readers and agents find the smallest useful set of
documents. Files directly under `docs/` describe the system as built.

## Start here

1. The [repository README](../README.md) for what Saga Lab demonstrates and a
   quick local run.
2. [Architecture](architecture.md) for the services, broker and databases.
3. The task-specific document from the table below.

## Route by task

| Task or question | Read |
| --- | --- |
| Services, messages, outbox, inbox, relay, where state lives | [Architecture](architecture.md) |
| What each scenario does, the invariants, expected histories and outcomes | [Scenarios and invariants](scenarios.md) |
| Traces, wait spans, span events, the Trace dashboard and link, retention | [Observability](observability.md) |
| Routes, JSON fields, status codes, the visitor cookie | [HTTP API](http-api.md) |
| Running locally, configuration, the demo reset, tests, the smoke run | [Development](development.md) |
| Visitor token, Grafana exposure, telemetry rules, rate limits, expiry, secrets | [Security](security.md) |
| Design limitations and known gaps | [Limitations](limitations.md) |
| Deploying, one-time setup, operating the VPS stack, rollback | [Deployment runbook](deployment-runbook.md) |
| UI changes (`pages.html`, `static/`) | [Design system](frontend/design-system.md) |
| Domain terms | [Glossary](../GLOSSARY.md) |
| Why a durable technical decision was made | [Architecture decision records](adr/) |
| Intended behavior, milestone scope, open requirements | [`docs/prep/`](prep/) |
| Issue tracker conventions for agents | [Issue tracker](agents/issue-tracker.md) |

## Source-of-truth order

When documents disagree, investigate in this order:

1. The code and its tests establish actual behavior.
2. Documents directly under `docs/` explain that behavior.
3. [`docs/prep/`](prep/) describes intended behavior: the requirements,
   technical plan, observability plan, milestones and each milestone's agreed
   scope. It is normative when refining the plan or a milestone, but may run
   ahead of the code.

The [glossary](../GLOSSARY.md), the [ADRs](adr/) and the
[design system](frontend/design-system.md) are normative too: follow their
terms, decisions and rules.

Resolve a mismatch instead of silently choosing one: update stale
documentation when behavior is settled, or raise a genuine requirement gap
before changing behavior. [AGENTS.md](../AGENTS.md) has the rules for agents
working in this repository.
