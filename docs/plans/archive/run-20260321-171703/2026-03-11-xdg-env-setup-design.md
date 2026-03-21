# Tracker XDG Env Setup Design

**Date:** 2026-03-11

## Goal

Let `tracker` load provider API keys from an XDG config `.env` file and add a `tracker setup` command that uses Bubble Tea to create and update that file.

## Approved Decisions

- Load XDG config env first, then load the project-local `.env`.
- Preserve normal shell environment precedence by continuing to use non-overwriting env loads.
- Store machine-level config in `os.UserConfigDir()/tracker/.env`.
- Implement `tracker setup` as a Bubble Tea form, not a line-oriented prompt loop.
- Support setup for `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, and `GEMINI_API_KEY`.
- Leave project `.env` files untouched; `tracker setup` only manages the XDG config file.

## Scope

This change covers:

- CLI command routing in `cmd/tracker`.
- Environment file loading for normal `tracker` execution.
- A Bubble Tea setup UI for entering provider API keys.
- Reading, merging, and writing the XDG config `.env` file.
- Tests for command parsing, env precedence, and setup persistence behavior.

This change does not cover:

- Managing model defaults or non-key provider settings.
- Editing the project-local `.env`.
- Adding a general-purpose config file format beyond `.env`.

## Approach

Split the work into two small pieces that share one config path resolver:

1. Add a config env loader that resolves `os.UserConfigDir()/tracker/.env`, loads it if present, then loads the current working directory `.env`.
2. Add a `setup` subcommand that opens a Bubble Tea form, collects provider keys, merges them into the XDG env file, and writes the updated file safely.

This keeps startup behavior simple and makes the new command testable without changing LLM client construction.

## CLI Design

The top-level CLI gains a subcommand:

- `tracker setup`

All existing run behavior remains available through:

- `tracker <pipeline.dot> [flags]`

Parsing should treat `setup` as a reserved command before normal run-flag parsing. Help text should document both modes.

## Env Loading Rules

At process startup, before building the LLM client:

1. Resolve the XDG config env path with `os.UserConfigDir()`, then append `tracker/.env`.
2. Load that file if it exists.
3. Load the project-local `.env` in the current working directory if it exists.

Because `godotenv.Load` does not overwrite existing process variables, the final precedence is:

- Shell environment
- Project-local `.env`
- XDG config `.env`

Missing env files are not errors. Invalid env file content should return a clear startup error that identifies which file could not be parsed.

## Setup UI

`tracker setup` should use Bubble Tea with a compact form:

- Three masked `textinput` fields, one each for OpenAI, Anthropic, and Gemini.
- Clear labels and short helper text.
- Save and cancel actions.
- A confirmation view after successful write.

For keys already present in the XDG env file, the UI should indicate that a value exists without revealing it. The simplest safe behavior is:

- Start each field empty.
- Show a status hint like `configured` next to fields with an existing value.
- Only replace a key when the user types a new value.
- Leave a configured key unchanged when the field is submitted empty.

This avoids echoing secrets while still allowing selective updates.

## Persistence Rules

The setup command should:

- Create the parent config directory if it does not exist.
- Read the existing XDG `.env` file if present.
- Preserve unrelated keys already stored there.
- Update only the provider key entries the user changed.
- Write the file with restrictive permissions.

If no keys are entered and no existing file changes are needed, the command should exit successfully with a no-op message rather than writing an empty file.

## Error Handling

- If `os.UserConfigDir()` fails, `tracker setup` and normal env loading should return a direct error.
- If the config directory cannot be created or the file cannot be written, `tracker setup` should fail with a clear filesystem error.
- Canceling the Bubble Tea form should exit cleanly without modifying files.
- Setup should allow partial provider configuration; users do not need to enter all three keys.

## Testing

Verification should cover:

- CLI parsing for `tracker setup` versus normal pipeline execution.
- XDG path resolution and env load precedence.
- Loading both XDG and local `.env` files without overwriting shell vars.
- Setup persistence behavior for create, update, preserve-existing, preserve-unrelated, and cancel/no-op flows.
- Bubble Tea model behavior at the unit level where practical, with file writes tested separately from rendering details.

## Risks

- CLI parsing is currently single-mode, so adding a subcommand can easily break `tracker <pipeline.dot>` if argument routing is not kept explicit.
- Secret-handling UX must avoid leaking existing key values in the setup UI, tests, or logs.
- File-permission behavior can vary by platform, so tests should focus on requested mode bits where the OS honors them and avoid brittle assumptions where it does not.
