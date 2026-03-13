# Changelog

All notable changes to this project will be documented in this file.

## v0.1.2 - 2026-03-13

### Added

- Clean public release history rebuilt from the current sanitized repository state.
- Telegram private chat and forum topic routing to Codex threads.
- Codex `app-server` primary adapter with CLI fallback.
- Topic-aware Telegram commands for thread control, model settings, permissions, limits, and language.
- Attachment handling for images, documents, voice notes, and audio files.
- macOS menu bar app, `launchd` integration, and Linux `systemd --user` support.
- GitHub Actions workflows for tests, release archives, and macOS app artifacts.

### Fixed

- Fixed Telegram `/version` command parsing so it is handled by the bridge instead of being forwarded to Codex as normal text.
- Fixed README repository links so GitHub renders internal documentation links correctly.
- Removed repository test strings and docs references that contained local machine absolute paths.
