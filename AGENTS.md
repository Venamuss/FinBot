# Working Rules

- Assignment requirements: [test-task-expenses.md](docs/test-task-expenses.md); project requirements and assumptions are documented separately in [docs/requirements.md](docs/requirements.md).
- Preserve the architecture in [docs/architecture.md](docs/architecture.md): a modular Go monolith, PostgreSQL, React/TypeScript, REST, and Telegram with LLM tool calling.
- Work only on the current stage; do not change application code or dependencies during documentation work.
- Check `TELEGRAM_ALLOWED_USER_IDS` before calling the LLM, reading stored responses, or executing bot operations. An empty list denies bot access to everyone.
- Derive the owner exclusively from Telegram `message.from.id`; scope all bot operations to that owner, including totals and clarification queries.
- The allowlist applies only to the bot; the panel requires no authentication and is intended for a trusted local network. Do not add a users table.
- Without a conversation focus, select the user's most recently created expense; a mismatch with the stated previous amount requires clarification.
- The LLM does not determine permissions and has no SQL or administrative tools; validate its arguments on the server.
- Use a single configurable OpenAI-compatible adapter. Live local LLM verification is optional and does not block the MVP; there is no automatic fallback to the cloud.
- Follow the [REST contract](docs/api.md): IDs and amounts are strings; do not use `float` for money; `spent_at` is a calendar date.
- Keep the architecture minimal; justify new features and dependencies. Do not present project assumptions as assignment requirements or user approval.
- Do not write secrets or expense contents to logs or the repository.
- Access checks for allowed users A/B and denied user C are mandatory; run Go checks, frontend checks, and a Compose smoke test before delivery.
- Record stage results and verification results in [docs/tasks.md](docs/tasks.md); track the mandatory MVP and additional checks separately.

## Go Code Style

- Write idiomatic, readable, maintainable Go. Optimize for clarity, not minimum line count.
- Use conventional multi-line control flow and error handling. Avoid compressed one-line statements.
- Prefer explicit logic and descriptive names over clever abstractions.
- Keep functions focused. Extract helpers when they improve readability, not merely to reduce function length.
- Avoid unnecessary interfaces, wrappers, generic helpers, and architectural layers.
- Follow existing project conventions and use gofmt.
- Handle errors explicitly. Preserve useful error context with wrapping where appropriate.
- Keep comments focused on intent, invariants, and non-obvious decisions rather than restating the code.

## Database and Data Integrity

- Use parameterized SQL for all data values. SQL identifiers must come from fixed allowlists.
- Keep transactions short and use context-aware database operations.
- Enforce Telegram expense ownership in SQL, not only in application code.
- Never use floating-point arithmetic for monetary amounts.
- Do not silently ignore database errors or change transaction isolation semantics.
- Preserve atomicity and idempotency guarantees when changing Telegram processing.

## Development Workflow

- Read AGENTS.md and the relevant specification files before implementation.
- Work only within the requested scope. Do not implement future tasks proactively.
- Prefer small, reviewable changes over large rewrites.
- Preserve documented API contracts and existing behavior unless explicitly instructed otherwise.
- Do not introduce dependencies without explaining why they are needed.
- Add or update tests when changing behavior or fixing bugs.
- Run relevant formatting, tests, and static checks after changes.
- Never claim a check passed unless it actually ran successfully.
- Do not silently modify unrelated files or rewrite existing documentation.
- At completion, summarize changed files, design decisions, tests executed, and known limitations.

## Refactoring

- Keep refactoring separate from feature implementation whenever practical.
- Preserve observable behavior unless the task explicitly requires a change.
- Avoid unnecessary abstractions introduced solely for stylistic consistency.
- Do not replace straightforward code with complex generic solutions.
