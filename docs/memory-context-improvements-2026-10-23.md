# Memory and context reliability, 2026-10-23

This update completes the pending memory/archive work and bounds background journaling. Native Eino Session events remain the runtime source of truth; UI transcripts remain searchable reference material.

## Behavior

- Automatic recall uses the latest real user requests. Host snapshots and assistant speculation do not become retrieval queries. The automatic path skips an extra model request to rewrite the query.
- Recall cache entries include the query, expire promptly and are invalidated after successful memory writes. Failed refreshes do not resurrect expired facts; revision checks prevent a late retrieval from refilling an invalidated cache.
- Recall records carry source identifiers and evidence labels. Assistant self-reports are explicitly unverified. Historical summaries are reference material and current corrections take priority.
- Recall workers return owned results through a buffered channel. Retrieval and SQLite fallback respect parent cancellation; storage errors are propagated instead of silently becoming empty success.
- read_session_history searches archived runtime messages and older UI text within the current conversation. Exact records can be read with Unicode-safe character pagination. Runtime records are additionally workspace-scoped; callers cannot select another session. Historical tool calls do not prove execution.
- First native-session migration uses a bounded recovery ledger built from up to 500 prior transcript messages, preserving earlier goals and corrections outside the ordinary recent-message window. Existing native sessions are not imported again; read errors do not create a false empty migration.
- Background journaling selects whole messages, caps each excerpt at 4,000 Unicode characters and the aggregate at 16,000. Excerpts preserve the beginning and end with an explicit omission marker. Recent messages take priority. This bounds background prompt size; it is not a promise of a particular response time.

## Limits

Old UI transcripts cannot recreate tool protocol messages that were never stored. Migration examines up to 500 prior messages; older text remains available through transcript lookup. Main chat has archive lookup; specialized orchestrators are not all migrated to the native Session path. Summary quality still depends on the model and deterministic retention rules.

## Validation

- Memory/recall, migration, archive lookup and journaling focused tests passed in agent, memory, core and sessions.
- Full suites passed: sessions, memory, core, checkpoint, engine, arch and processutil.
- Frontend type checking, all 14 frontend tests and production build passed. Vite retains the existing large Monaco chunk warning.
- Full regression is NOT green. Responses/run-meter tests could not connect to loopback test servers (Windows socket access forbidden). Three bridge connection-probe tests failed in the same run. The legacy shell KillTree test reproducibly reported a surviving child process; this remains an open issue, separate from the Job Object path used by background tasks.
- Production Windows executable compiled successfully. Isolated executable startup/health validation was rejected by automatic approval policy because sandbox execution approval is disabled. No startup success is claimed.

## Test executable

`bin/agentgo-memory.exe`, 62,443,008 bytes.

SHA256: `E39249482705586EA02B255EB173DD18FA58607EFB3DD206A0CE9FD7A1E53B67`

The previous executables are retained. No upstream commit was made in this round.