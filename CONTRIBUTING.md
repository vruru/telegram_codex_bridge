# Contributing

## Development flow

- Keep changes focused and easy to review
- Prefer small PRs with a clear goal
- Add or update tests when behavior changes
- Document new environment variables and operational assumptions

## Release flow

- Ship release fixes under a new `v*` version tag instead of patching an existing published release.
- Do not reuse or move an already-published release tag during normal maintenance.
- If CI or packaging breaks after a release, land the fix on `main` and publish the next patch version.

## Suggested first issues

- Telegram attachment and image handling
- Topic deletion and archive handling
- Richer Codex progress streaming
- launchd and menu bar app polish on macOS
- Logging, retries, and metrics
