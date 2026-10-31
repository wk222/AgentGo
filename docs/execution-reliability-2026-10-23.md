# Execution reliability improvements (2026-10-23)

This iteration strengthens process ownership and truthful task outcomes. It retains the preceding native Eino Session, structured recovery memory, source-revision invalidation and official PlanTask integration.

## Process ownership

Both the official background Shell adapter and legacy job_monitor now start commands through shellcmd.StartTree. The previous Prepare/KillTree API and taskkill implementation were removed so production call sites cannot accidentally select the weaker path.

On Windows, the root starts suspended, joins a Job Object configured with kill-on-close, then resumes. CommandContext cancellation terminates the owned Job rather than only its root. A bounded output-drain delay prevents descendants from keeping Wait blocked forever. Job handles are released on completion and startup failure. Unix uses a dedicated process group; descendants are cleaned up when the owned root finishes.

Regression coverage includes explicit stop, context cancellation, cancellation before startup, and a root that exits while a descendant retains its output pipe. These checks use only processes started by the tests.

## Truthful outcomes and bounded output

job_monitor reports cancelling after a stop request. Only the worker records cancelled and FinishedAt after Wait returns. A running job's exit code starts at -1 instead of implying successful exit.

The monitor drains output into a bounded tail buffer (at most 6000 retained bytes) and returns the final 1500 Unicode characters. Large logs no longer accumulate in an unbounded CombinedOutput buffer. UTF-8 characters split across writes remain intact in the final tail; execution failures append a diagnostic without replacing the process exit code.

Cancellation and completion in the official Shell adapter now publish their decision under the same lock. An accepted cancellation cannot be overwritten by a racing completed outcome.

## Recovery without implicit replay

Within one running Shell adapter, a task ID identifies exactly one command execution, including failed and cancelled outcomes. Repeating StartCommand returns that original execution instead of silently retrying a command that may already have produced side effects. An intentional retry requires a new task ID.

RecoverCommand rejects a mismatched command, and already-cancelled requests cannot start an execution. Unknown executions after restart still fail closed: process records are not durable and an unknown command is never automatically rerun.

This is process-local idempotency, not durable exactly-once execution. PlanTask completion remains a planning assertion rather than proof of successful tests or tool effects. Commands that launch independent remote work are outside local process-tree ownership.

## Validation

Passed:
- Complete shellcmd, processutil, promptbudget, sessions, memory, core and arch suites.
- Focused agent, bridge and tools regression covering context/memory/Session integration and background execution, cancellation, monitoring and shutdown.
- A failed command writes a marker and exits 7; a repeated task ID preserves the original outcome and does not write a second marker.
- A 500-iteration cancellation/completion consistency test.
- Multi-megabyte monitor output preserves the final Chinese log and actual exit code.
- Frontend typecheck, all 14 frontend tests, and production Vite build.

The existing large Monaco bundle warning remains. These passing suites do not claim the entire project suite is green. Desktop launch and a real-provider long-running conversation were not verified in this round.

Linux shellcmd tests also cross-compiled successfully; they were not executed on Linux.

## Build artifact

bin/agentgo-execution.exe

Bytes: 62515712

SHA256: D0F8F325D0C33534EB21807EE7507D3B34456C4A151EFE86EDA79F465553FC40

PE headers verified: amd64, Windows GUI subsystem. Temporary Wails resource removed after build.
