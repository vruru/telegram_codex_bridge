# Changelog

All notable changes to this project will be documented in this file.

## v0.1.3 - 2026-03-13

### Added

- Added Gemini CLI as an alternate topic-level provider with a new `/provider [codex|gemini]` Telegram command.
- Added provider-aware routing persistence so archived threads, resets, and quota checks continue using the correct backend.
- Added SQLite regression coverage for topic preference persistence including the new provider column.

### Fixed

- Fixed `/provider` command parsing and Telegram command registration so it is handled by the bridge instead of being forwarded to Codex.
- Fixed topic preference persistence for provider switches by correcting the SQLite insert column/value mismatch.
- Fixed provider fallback behavior so resume failures no longer silently replace an existing Codex thread with a fresh Gemini thread.
- Fixed runtime/app bundle drift where an older macOS menu bar app could overwrite the newer bridge binary in Application Support.

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
