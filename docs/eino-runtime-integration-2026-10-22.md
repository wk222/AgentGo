# Eino runtime integration

## Dependency and upstream contribution

AgentGo keeps the Eino module requirement at alpha.35 and pins a replacement to the public fork commit a21954c09a22caa48b3455cab4bbfa52ee443256. No sibling checkout or Go workspace is required to build.

PR: https://github.com/cloudwego/eino/pull/1401 (base alpha/10). Three commits backport existing main fixes #1232, #1235 and #1297 with original authors and cherry-pick attribution. A separate new commit fixes portable virtual filesystem and AGENTS.md paths on Windows. The PR remains subject to maintainer review and CLA verification.

## Ownership

- Eino Session owns the main chat Runner's durable model history and framework summarization events.
- AgentGo implements the SessionEventStore contract over the existing SQLite database. Ordered batches commit atomically; duplicate IDs roll back the complete batch. Cursor, kind filtering and reverse pagination follow the official contract.
- AgentGo retains UI transcripts, approvals, workspace assembly, task context and compression memory. Legacy history is imported once. Existing checkpoints without a native event log retain the legacy resume configuration.
- Workspace paths scope native event logs. The agentic: transport prefix is normalized for deletion with the UI session.
- Host projection changes emit MessagesReplaced events. Framework-generated system prompts are regenerated; caller-supplied system messages and compression memory survive replay. Current-turn input is appended only once.

The patchtoolcalls middleware repairs incoming interrupted history before summarization. The host's causal normalization remains the guard after context cropping, with unknown tool execution explicitly described as unverified rather than successful. These run at different boundaries.

## Limits and follow-up

This migration covers the main ADK chat and event-stream runners. Specialized deep/admin/matrix/app-builder orchestrators retain their separate history contracts; they must receive isolated runtime scopes before migration. This is not automatic recovery of arbitrary external side effects, a distributed session lease, or a claim that all background tasks are durable.

Full replacement events increase event-log size over long sessions. Snapshot retention/compaction needs an explicit replay/checkpoint compatibility policy before deleting old events. The current implementation retains the log.

The in-house strengths worth extracting for future upstream proposals are compression memory retaining tasks and verification state, failure backoff for summarization, and bounded tool-output offloading. Each needs a standalone API and framework-level regression tests; the application/UI layer is not included in the upstream PR.

## Validation

- Official event-store contract: 14 cases, plus SQLite transaction rollback, import-once, reopen and actual Runner + SessionSpine cross-restart replay tests.
- Standalone upstream tests: adk, adk/filesystem, adk/middlewares/agentsmd passed; targeted filesystem middleware query tests passed.
- Full upstream filesystem middleware tests also exposed two Windows shell/background failures outside the query-order patch. These are disclosed in the PR.
- Fixed public dependency with GOWORK=off: sessions, agent, bridge, core, engine, memory, arch and cmd/agentgo all passed.
- Production Windows GUI build succeeded; isolated --serve startup returned HTTP 200 from /health. No real private credentials or live model calls were used.
- Executable: bin/agentgo-runtime.exe, 62,353,408 bytes, SHA256 ad14ca0f1256fbc0f391b9d9dd74684c2ef8f4668adca7c091907d50f820c1b0.

## 2026-10-29: execution context entry points

App Builder, Matrix start/resume and the legacy Matrix capability-event entry now use the existing `BindRunContext`: agent session ID, governance scope and run evidence registry agree. Parent cancellation, deadline, context values and subagent depth remain inherited. `WithSessionID` remains only for non-executing fork-directory lookups.

Matrix resume now accepts the original session ID explicitly instead of replacing it with `app_matrix_coordinator`; bridge approval continuation passes the pending run's session ID. The global capability coordinator still explicitly uses its own global session. App Builder clears its registered cancellation handle on completion/failure.

Local HTTP protocol + real Eino tests invoke the public entry points: App Builder checks scope and inherited context at tool execution, exercises parent cancellation and verifies handle cleanup. Matrix pauses inside a nested workflow tool, closes/reopens the actual SQLite checkpoint database, resumes a newly constructed Runner in the original session, and executes the approved action once. These are controlled protocol tests; no upstream model, live desktop interaction or real user approval was involved.

The accompanying evaluation changes are documented in [eval-baseline.md](eval-baseline.md): v3 adds artifact checks and rejects incompatible baselines before model execution. Existing v2 model measurements remain archived.

## 2026-10-29: shared tool execution contract

Chat, App Builder/Matrix specialists, Deep and PlanExecute now build the same synchronous tool-call chain in internal/agent/tool_execution.go: safe error conversion outside argument repair, governance, subagent limits and tool events. Auditors and observers receive the original execution error; the model receives the readable error afterward. Governance uses the current session/canvas policy. Explicit specialist tool lists remain fixed.

Removed tool_mw_mount.go and its per-entry GovernanceInHandlers/HealInHandlers switches. Deep's pinned API supports ADK Handlers and now uses them directly. PlanExecute's pinned executor API only exposes ToolsConfig, so one adapter applies the same ADK endpoints there; it does not construct a second governance implementation. Core's standalone workflow ToolsNode retains its existing Compose helper.

Real Eino controlled-model tests cover all four constructions: repaired arguments, original hook errors, one execution/event pair/count, policy denial before execution, and SQLite-checkpoint approval pause/resume with exactly one execution. Public App Builder tests additionally exercise actual model-protocol entry, scoped hooks, observer/count parity and cancellation; nested Matrix checkpoint reopen/resume remains covered. These checks do not establish live-model quality, arbitrary side-effect recovery, or streaming/enhanced tool-interface parity.

This removes a policy/observer assembly fork. It does not yet merge Gateway/desktop turn lifecycles, move chat ownership out of AppService, or share governance budget/tracker state across agent constructions. See the bounded follow-up in [deep-fusion-plan.md](deep-fusion-plan.md).

Validation: go test ./internal/agent ./internal/bridge ./internal/core ./internal/governance ./internal/arch -count=1 passed (agent 127.188s, bridge 36.657s). Formatting and targeted diff whitespace checks passed. No desktop package or upstream-model run was produced in this slice.

## 2026-10-29: Runtime owns native chat turns

Moved native chat execution and prompt preparation to Runtime. Runtime also owns the single engine router; the Eino plugin starts with Runtime instead of waiting for AppService. Desktop methods retain their public signatures. The legacy brain service remains a desktop settings compatibility surface; Eino no longer injects it. Live code-toolbox lookup now has one Runtime-owned reference, so context expansion does not need a UI object.

Gateway streaming, desktop engine runs and the legacy synchronous/streaming chat endpoints use Runtime.runEngineTurn for history, evidence recording, final transcript and durable run commits. Gateway no longer directly calls GenerateStream or constructs AppService to register the initial pause. Its SSE chunk/interrupt/done protocol is retained; interrupt metadata now also carries arguments and prompt. Pending notices and completion follow persistence. Final results are authoritative: legacy stream fragments cannot replace a fallback warning.

This adds the existing slash-command expansion, @file/terminal/diagnostics context, prompt/Stop hooks and turn-memory handling to Gateway chat. Cancellation reaches both the router and legacy/resumed ADK segments. A failed transport write cancels its native run. Cancelled/deadline runs do not start a tool-less fallback. Fallbacks retain their notice and degraded flag through engine results/events, Gateway, durable commit and evaluation; evaluation and coding-engine consumers reject them as verified task completion. Rapid turns receive unique memory IDs instead of overwriting another turn created in the same second.

Controlled local HTTP/SSE + real Eino tests exercise four chat entry points, actual tool execution, same-turn evidence persistence, command/file/hook context and one submit/Stop pair per successful turn. Other tests cover prompt refusal without model calls, headless Gateway approval pause/resume/replay rejection, native cancellation from both surfaces, transport failure cancellation, fallback marking and evaluation rejection. This is infrastructure validation with a scripted model peer, not model-quality or native desktop interaction evidence.

Remaining scope: specialized orchestrators retain their separate history contracts; ACP/IDE low-level callers use the shared native operation without adopting the entire chat boundary. Approval continuation still uses its existing AppService compatibility adapter. Some extension plugins are still initialized by the desktop. Governance budget/tracker ownership remains the next migration. Gateway questions are now persisted and surfaced as pending questions; answering arbitrary questions through HTTP is not added here, so those still require the existing desktop question interface. No upstream-model benchmark or desktop package is claimed.

Validation passed: agent, bridge, core, governance, engine, gateway, evaluation, arch and cmd/agentgo-eval package tests; bridge's final full run took 67.703s. The web-tagged cmd/agentgo tests passed. Targeted race tests for the new shared-turn, approval, cancellation, transport, fallback and memory-ID cases passed (14.752s). Targeted diff whitespace checks passed. The migration initially exposed a nil-Runtime adapter check and the old runtime-plugin count fixture; both were repaired before the final passing runs.

## 2026-10-29: execution accounting belongs to the existing SQLite owner

Governance middleware now constructs policy only. Removed RuntimeBudget and ToolCallTracker rather than caching middleware instances. ApprovalQueue's existing database owns two additive tables: governance_daily_budget and governance_tool_starts. No new service, database, worker or product setting is introduced.

Daily credits are shared across agents, workspaces and root runs using the same database, and survive reopen. Credits retain the previous tariff: critical=1, low=0.01, medium/high=0; they are stored as integer hundredths. A bare Policy's zero budget uses the existing default of 100; negative/non-finite limits fail closed. Low-risk calls now honor budget failure rather than silently ignoring it. These credits are policy units, not provider/token billing.

Native turns reuse the existing engine run_id as their governance root. Nested agents retain it across session IDs; specialized/direct agent entries without a host ID create an execution scope at BindRunContext. Approval checkpoints and request metadata carry the root identity; resume restores it into the fresh context before evaluating limits. Different new turns do not share call windows. Unscoped legacy workflow calls retain a workspace/session fallback. The existing ten-minute window remains; this is not a new lifetime call quota.

Before hooks run before atomic admission. Approval consumption, budget debit and call-slot insertion commit together under SQLite's writer lock, so concurrent queue handles cannot overspend or bypass the cap. Budget/limit failure leaves an approval unconsumed; a hook veto does not debit. Actual failed/cancelled endpoint attempts count to prevent failure loops. Framework interruption, or cancellation between admission and endpoint entry, releases only that reservation's credits and slot; consumed authorizations never become reusable. Cancellation is rechecked after admission. After hooks only observe admitted endpoint entries.

New approvals are additionally bound to the root execution, preventing a later turn with the same session/tool-call ID from borrowing one. Legacy approval rows lacking a root remain compatible with the previous session/workspace/call-ID checks. Existing checkpoints without a root do not fabricate past call counts. The new ledger does not backfill costs from old audit history.

Canvas limits now tighten positive host caps rather than increasing them. Fresh middleware cannot reset counts, a child session cannot reset its parent's scope, and switching to a deeper canvas cannot relax strict host limits.

Validation covers database reopen, two queue handles racing on daily credits/call caps, failed attempts, hook veto, admission-time cancellation, approval replay/cross-root isolation, window expiry, interrupted reservations and other-day budget isolation. Real Eino chat/specialist/Deep/PlanExecute tests rebuild both agents and contexts before resume with a one-action daily budget, execute once, and verify the original root's cap afterward. Gateway additionally checks that approval metadata's root equals the durable run ID.

Limits: accounting is conservative at admission. A process crash after commit does not automatically refund or retry external actions. This does not prove exactly-once external side effects, recover pre-upgrade spending, migrate specialized history contracts, add ledger retention/compaction, or meter streaming/enhanced tool interfaces.

Checks passed: full governance/agent/core/bridge/engine/arch regression (agent 138.536s, bridge 51.315s); after removing the old tracker API, targeted agent approval/canvas, core workflow, bridge transport and architecture checks passed again. Full governance tests and race tests passed after the cleanup (race 17.357s); web-tagged cmd/agentgo tests passed. The final native run-ID linkage passed Gateway/desktop approval, cancellation, fallback and completion tests (8.005s). Formatting and targeted whitespace checks passed. No upstream-model quality run or desktop package was produced.
