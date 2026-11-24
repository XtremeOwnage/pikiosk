# pikiosk

## Writing

Applies to code comments, UI text, docs, commit messages, PR bodies and release notes.

- No narration: no history ("now", "was", "previously"), no storytelling, no explaining what is obvious from the code or the control.
- Code comments: one short line for a non-obvious constraint, or none.
- UI help text: one short line or none. Confirm dialogs: a short question ("Turn off Wi-Fi?"). Toasts: a few words.
- Docs: reference style, tables and short lists over paragraphs.
- Commits and PRs: a terse subject, then a few short bullets of what changed.

## Git

- `main` only takes pull requests (ruleset "main: pull requests only"). Work on a branch, push it, open a PR.

## Checks

- `go vet ./...` and `go test ./...`
- `shellcheck -S warning deploy/raspios/*.sh deploy/raspios/files/*.sh`
- `nix build .#checks.x86_64-linux.vm` (stage new files with `git add` first; the flake only sees tracked files)
- Docs: `netdocs build` in `docs-site/`
- New API endpoints and MQTT commands need entries in `internal/web/api.md` and `docs-site/docs/reference/mqtt.md`.
