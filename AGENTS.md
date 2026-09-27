# Agent instructions

## Trunk flow (every agent: Claude, Codex, subagents)

- The trunk of this repository is `feat/uniffi-sdk`. All finished work flows into it.
- Start each task on a new branch from the trunk. One task per branch.
- When the work is finished and its tests pass, stop. Report the branch name to the owner. The integrator (one Claude session) lands it into the trunk; do not merge into the trunk yourself.
- Do not make new long-lived branches or integration lines.
- Commit before you stop. Never leave uncommitted changes in a working tree; a WIP commit on your branch is fine.
- Mark finished work: the last commit message starts with `READY:` and names the test command and its result, for example `READY: redact debug output; tests: cargo test -p arachne-sdk -> 41 passed`.
- Do not push, and do not move submodule or SDK pins, unless the owner says so.
- The `core` submodule pins Arachne Core. Move it only to a commit on the Core trunk (`integrate/wave1` in ~/arachne-core).

