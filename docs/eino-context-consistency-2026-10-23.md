# Eino context consistency integration, 2026-10-23

## Runtime ownership

Eino remains responsible for native Session replay, checkpoint-based interruption/resume, the main Summarization operation and official PlanTask tools. AgentGo supplies SQLite persistence, task projections, memory replacement rules, budgets and recovery behavior. No Eino dependency version or upstream source change was made in this round.

## Completed changes

### Official plans feed recovery state

PlanTask tools and the session spine now resolve the same per-conversation storage directory. The workspace path is retained, with a hashed session subdirectory; the transport-only agentic prefix does not change the namespace. Relative roots are normalized to absolute paths.

Before each model call, the spine reads a fresh bounded task snapshot and projects in-progress/pending tasks, unresolved blockers and a next eligible task into TaskState. Completed planning items are explicitly reported state, not evidence that tests or commands succeeded. Fresh storage is authoritative; old Session snapshots cannot restore tasks removed by PlanTask.

The official middleware clears its list when all tasks complete. The integration preserves that behavior; the Session event archive retains the tool conversation. Backend writes use a flushed temporary file followed by replacement, so readers do not see partial JSON. Unreadable optional plan data produces recovery guidance rather than blocking the conversation.

Compatibility: old unscoped files directly under .tasks are left untouched. They are not automatically assigned to an arbitrary conversation, because their ownership cannot be established. The current snapshot reads at most 128 recent records; TaskList/TaskGet remain available for further inspection.

### Corrections and invalidation

SQLite maintains persistent per-scope memory revisions. New facts, changes to existing evidence and removals advance the version; recall counters and ordinary episode inserts do not. A conversation's dependency includes its own scope and global memory.

Superseding records now validates authoritative stored records and commits archival, replacement links, canonical selection and revision changes in one transaction. Merge insertion and replacement are also atomic. Cross-scope resolutions, obsolete replacement targets and partial failed merges are rejected. A canonical resolution is not a test/build verification claim; newly merged wording is marked as derived and unverified.

Recall cache entries depend on these revisions. A correction invalidates a live cache even within its ordinary TTL, and retrieval results that observe a changed version are discarded.

Saved summaries and recalled projections carry their source revision. Before another model call, outdated derived summaries/excerpts are removed from the working view, while original user/tool history, source notes and immutable archive records remain. The official summary finalizer checks the captured version again after model generation, rejecting a result if its memory sources changed. Background distillation checks the version around its model call as well.

This is conservative scope-wide invalidation, not a per-fact dependency graph or a linearizable transaction spanning a remote model request. Existing confirmed memory conflict-resolution entry points perform replacements; free-form user corrections are not all automatically converted into database edits.

### One main compression path

Under normal budget conditions the spine preserves active history. Official token-triggered Summarization owns semantic history replacement. Deterministic history/tool-output reduction remains the preflight fallback when the budget is under pressure; original requests and system/workspace rules remain protected.

Background episodic work can still extract indexed atoms, but its returned handoff text is no longer appended as a second summary in the main prompt. This avoids two summaries of the same task accumulating independently.

Malformed optional projection metadata is removed from the derived view with guidance to recover original history. Real messages, rules and archived source objects are not mutated.

### Stable multilingual memory references

Atomic statements and their structured parent containers bypass generic byte-based chunking. A 500-character Chinese statement can exceed a 1,200-byte chunk threshold; splitting it previously made its canonical ID unavailable and could leave unaddressable source references. Atom IDs and parent IDs now remain directly readable and retractable through the hybrid engine path.

## Verification

Full passing suites: promptbudget, sessions, memory, core and arch. Targeted passing agent/bridge suites cover spine/context/compaction/projection, memory/recall, migration/history lookup, official PlanTask, runtime Session replay and run control.

New coverage includes invoking the real official TaskCreate/TaskUpdate tools, all-completed cleanup, conversation isolation, planning-versus-verification labels, early history preservation under a small token budget, revision persistence across reopening SQLite, rollback of failed corrections/merges, immediate cache invalidation, stale summary rejection, damaged-projection recovery and canonical IDs/source parents for long Chinese atoms.

Frontend type checking, all 14 frontend tests and Vite production build passed. The existing large Monaco bundle warning remains.

The full project suite is not claimed green: earlier loopback-network tests and legacy Shell process-tree cleanup failures remain outside this change. GUI startup and a real-provider long-task test were not performed in this round. Session rollback does not undo real-world tool side effects or automatically reverse confirmed memory edits.

## Build artifact

`bin/agentgo-eino-context.exe`

Bytes: 62512128

SHA256: 863093C00F8F4FD859184EFAC9FA7CB3BE1E0B865D51A2102F28BE4D43A174B3
