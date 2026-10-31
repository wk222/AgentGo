# Structured context and atomic memory (2026-10-23)

## Architecture

The runtime keeps three different representations:

1. **Recovery state**: an original goal anchor, retained user constraint/correction quotes, recent user requests, recorded tool observations and unverified assistant reports. This is a typed `TaskState`, persisted in native Session message metadata. Only its compact rendering enters the prompt.
2. **Atomic memory**: one durable statement per record, with kind, originating scope, source journal IDs, parent summary ID and verification state. Kinds are constraint, preference, decision, fact, lesson and unresolved. Existing FTS/vector retrieval is reused.
3. **Original archive**: native Session events and UI transcripts stay available through the existing conversation-bound `read_session_history` tool. Note fingerprints identify recovery notes; they are not fabricated archive record IDs.

The old ProjectionMemory fields remain readable. Structured state is rebuilt from source notes rather than parsing or repeatedly summarizing the rendered checkpoint. No destructive database migration is required.

## Prompt budgets

| Component | Initial estimated-token cap | Policy |
| --- | ---: | --- |
| Recovery state | 1,400 | Goal first; explicit constraint/correction quotes have reserved space; recent requirements before older observations/reports |
| Recalled memories | 1,000 | Scoped and relevant global candidates; deduplicate; skip inactive/superseded/contradicted records and summary containers |
| Optional semantic summary | 500 | Latest bounded source summary; no raw model JSON is injected |
| Original records | On demand | Search short literal identifiers, then paginate exact records |

The shared multilingual estimator matches the existing agent context inspector. These are estimates, not exact tokenizer guarantees. The final model-window guard still applies. The budgets do not include system rules, tool schemas, recent conversation, media or workspace context.

Explicit-requirement retention uses conservative textual cues (for example must/do not/constraint and corresponding Chinese terms), preserving the user's quote. It does not infer that a historical request is still current or automatically resolve semantic contradictions. Later user corrections take precedence. Finite budgets cannot retain every instruction verbatim; original records remain necessary.

## Atomic distillation

- The model receives a bounded source list with real journal IDs and returns at most 12 atomic statements.
- Only known source IDs, allowed kinds and bounded statement lengths are accepted. Source-ID validation is provenance checking, not proof that the statement follows logically from the source.
- Generated statements are `derived_unverified`, low trust and not canonical. A journal or assistant statement is never promoted into a verified test/build result.
- Atom IDs are deterministic within scope. Repeated distillation does not duplicate the same statement, erase feedback or reactivate archived atoms.
- The raw structured response stays in its parent container. Only accepted, active atoms can form the episodic summary. Unsupported claims cannot leak back through the parent JSON.
- Old plain-text distiller output remains compatible as an unverified reference summary.

## Retrieval consistency

SQLite is authoritative. Hybrid recall refreshes every candidate from SQLite before returning it, rejecting deleted, inactive, superseded, contradicted and foreign-scope candidates. A stale vector copy cannot restore old content or bypass the current scope.

All context producers share the memory packing budget. Task-aware enrichment does not append unrelated recent journals after packing. Relevant global candidates can be included alongside scoped candidates; they are no longer considered only when scoped recall has no result.

Reflection-generated insights remain in their originating scope rather than automatically entering global preferences. Journal IDs use nanosecond IDs, persistence errors are propagated, and journal text containing the Chinese word for inability is no longer mistaken for an empty journal. Memory upserts also update metadata, preserving current provenance.

## Validation

Passed full suites: promptbudget, sessions, memory, core and arch.

Passed targeted agent/bridge suites matching Context, Compaction, Projection, Recall, Memory, Migration, HistoryLookup, HistoryTool, Distillation, Episode and Finalize.

New regression cases cover repeated compression with JSON serialization/recovery (60 rounds), early constraint retention, evidence labels, multilingual budgets, deduplication, invalid source IDs, parent-container leakage, provenance updates, scope isolation, authoritative retrieval and preventing archived-atom resurrection.

Frontend type checking and production build passed. The existing Monaco chunk-size warning remains.

This is not a claim that the full project suite is green. Earlier loopback-network tests and legacy Shell KillTree failures are outside this change and remain open. The EXE was compiled; GUI startup and live-provider end-to-end validation were not performed in this round.

## Build artifact

`bin/agentgo-memory-v2.exe`

Bytes: 62471680

SHA256: 5528AAA66982BB3160630007DB2FFB9B917EA78C8A7FE1C5E8FDF29DAF9FA281
