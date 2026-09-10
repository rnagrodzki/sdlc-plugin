# Changelog

## [0.0.2] - 2026-09-10

## Added
- Next-step guidance to ship prepare and verify side-effect outputs

## Changed
- Moved release-intent ownership from version step into pr step
- Consolidated version diagnostics and schema handling into pr step

## Removed
- Version MCP tools and CLI registration
- Standalone version skill (functionality absorbed into pr step)

## Fixed
- MCP stdio startup: migrated off broken mark3labs/mcp-go to the official SDK
- GoReleaser build dispatch timing after release-on-main tags

## [0.0.1] - 2026-09-10

- Added a release-intent gate to `pr_apply`, requiring an explicit `releaseLevel` or an acknowledged `skipReleaseCheck`, keyed by `releaseSource` (`user`/`config`/`pipeline`) so unattended runs can't claim a human decided.
- Standardized a `Next` field across pr and commit tool outputs.
- Added `jsonschema_description` and `enum` tags across MCP tool structs for stricter, self-documenting contracts.
- Added MCP contract review dimensions, guardrails, and developer helper agents for auditing tool contracts.

## [0.0.1-rc3] - 2026-09-09

Fixed error report target repository configuration

## [0.0.1-rc2] - 2026-09-09

### Added
- RC release note aggregation and CHANGELOG collapse when promoting release candidates to final release
- Automatic deduplication of identical notes across RC versions

### Changed
- Improved error handling and logging in release scripts

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.0.0]

- Initial Go port of the sdlc plugin.
