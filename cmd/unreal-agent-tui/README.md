# Unreal Agent

A minimal terminal client for Harness. Run it from your workspace:

```sh
go install github.com/unreallabsai/unreal-agent/cmd/unreal-agent-tui@latest
unreal-agent-tui
unreal-agent-tui -provider openai -model MODEL -theme turbo-vision
unreal-agent-tui -help
```

On the first launch without a provider flag or `UNREAL_HARNESS_LLM_PROVIDER`,
Unreal Agent discovers an existing Codex subscription login and asks whether to
use it. The dialog has model and reasoning selectors: Tab or Up/Down selects a
field, Left/Right changes its value, and Enter confirms. The initial defaults
are `gpt-6.1-sol`, high reasoning effort, and the Turbo Vision theme.
The model selector uses the Codex catalog in `settings.json`, including custom models.

Accepting saves the provider, model, and reasoning choice in
`$XDG_CONFIG_HOME/unreal-agent/preferences.json` (default
`~/.config/unreal-agent/preferences.json`). Later launches reuse that choice
without the dialog. Use `unreal-agent-tui -setup` to reopen it and change the choice.
Declining exits and shows example launch commands. If credentials are unavailable
or invalid, it shows sign-in instructions and examples for other providers.

Explicit flags override `UNREAL_HARNESS_LLM_PROVIDER`, `UNREAL_HARNESS_LLM_MODEL`,
and `UNREAL_HARNESS_LLM_REASONING_EFFORT`, which override saved choices.
Overrides apply to the current launch; accepting the setup dialog updates the saved
choice. An explicit provider skips the subscription dialog unless `-setup` is used;
`-provider openai-codex` uses saved model and reasoning settings when available.
Other providers use their registered default model when available and medium
reasoning effort. Providers without a default model require `-model`.

Turbo Vision is the default theme. Use `-theme lite` for the original Lite palette.
[Theme examples](themes/).

Workspace skills are loaded from `.harness/skills/*/SKILL.md`.

Type `@` in a message to search workspace filenames. Matching is case-insensitive
and fuzzy, using [sahilm/fuzzy](https://github.com/sahilm/fuzzy).
Filename matches rank ahead of directory matches;
include `/` to search a path. For example, `@uig` can find
`cmd/unreal-agent-tui/ui.go`. Up/Down selects
a result, Enter or Tab replaces the `@` query with its workspace-relative path,
and Esc dismisses the picker. Paths containing spaces are quoted. The next Enter
sends the message.
The picker scans files when opened, including hidden and untracked files, respects
workspace and nested `.gitignore` files, and skips version-control metadata
directories and directory symlinks. Scanning and searching run in process;
they do not launch Git, a shell, or other subprocesses.
File references insert paths into the message; file contents are not attached.

## ChatGPT subscription

Sign in with Codex using file credentials:

```sh
codex -c 'cli_auth_credentials_store="file"' login
unreal-agent-tui
```

Credentials come from `$CODEX_HOME/auth.json` or `~/.codex/auth.json`;
override with `OPENAI_CODEX_AUTH_FILE`. Alternatively use
`OPENAI_CODEX_ACCESS_TOKEN` and `OPENAI_CODEX_ACCOUNT_ID`.
Renew credentials with Codex, then restart the agent.
