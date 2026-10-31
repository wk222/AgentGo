# Conversation workspaces and self extension

The desktop host keeps Eino, native execution policy, IPC and application loading stable.
Generated functionality uses Python tools, inner apps (UI pages or agent profiles), workflow definitions and workspace skills.

## User flow

- The initial workspace is the conversation. One sidebar holds history, workspaces and pinned app pages.
- Code opens alongside conversation. Workflow editing opens independently; its optional assistant docks on the right.
- Creation Center turns a description into a chat request with the existing creator tools and explicit verification instructions.
- UI apps can be pinned to navigation. The native host renders them through the existing InnerAppHost bridge.

## Agent tools

- extension_catalog: inspect reusable capabilities.
- save_workflow_definition: validate IDs, references and reachability, preserve explicit branches, snapshot an existing definition, save and refresh workflow tools.
- manage_extension_versions: snapshot, list, validate and restore generated content.
- Existing create_tool, register_inner_app, scaffold_inner_app, update_inner_app_file, verify_inner_app and evolve_skill implement the artifacts.

Snapshot selectors are workflows (IDs or names), tools (dynamic names), innerapps (app names), and skills (workspace/skills directory names).
Archives use the existing AgentPack format plus skill files. Skills include supporting scripts and assets.
A snapshot is structural validation, not evidence that the generated implementation ran successfully.
Restore backs up currently existing selected content. Apps and skills replace their complete file sets; removed files do not remain active.
A failed or partial import is reported as failure with recovery information. This is not a cross-store ACID transaction.
Local versions persist across restart. Backups are retained.

Native tool names cannot be replaced by compiled dynamic tools, including after restart.
New management tools declare their mutation traits and use the existing mode and execution policy.

## Limits

Creation Center initiates real agent work; it does not fabricate generation progress or success.
Functional tests must be run by the agent and reported honestly.
Python tools require an available Python runtime and their own dependencies.
Versioning currently covers tools, app bundles/profiles, workflows and workspace skills. Native Go binaries, Eino, credentials and global host settings are outside the generated artifact format.
New native functionality still requires editing source and rebuilding the executable.

## Verification

Frontend: typecheck, node test suite, tests/layout-spaces.browser.html (served by Vite).
Backend: internal/agentpack tests cover version recovery, complete app replacement, skill scripts, branch preservation, malformed workflows, path rejection and native tool collisions.
