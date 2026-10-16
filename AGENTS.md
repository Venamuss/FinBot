# Working Rules

- Assignment requirements: [test-task-expenses.md](docs/test-task-expenses.md). Project requirements and assumptions are documented separately in [docs/requirements.md](docs/requirements.md).
- Preserve the architecture in [docs/architecture.md](docs/architecture.md): a modular Go monolith, PostgreSQL, React/TypeScript, REST, and Telegram with LLM tool calling.
- Work only on the current stage; do not change application code or dependencies during documentation-only tasks.
- Check `TELEGRAM_ALLOWED_USER_IDS` before calling the LLM, reading stored responses, or executing bot operations. An empty list denies bot access to everyone.
- Derive the owner exclusively from Telegram `message.from.id`. Scope all bot operations to that owner, including totals and clarification queries.
- The allowlist applies only to the bot. The unauthenticated panel is intended for a trusted local network. Do not add a users table.
- Without a conversation focus, select the user's most recently created expense. A mismatch with the stated previous amount requires clarification.
- The LLM does not determine permissions and has no SQL or administrative tools. Validate its arguments on the server.
- Use a single configurable OpenAI-compatible adapter. Live local LLM verification is optional and does not block the MVP; there is no automatic fallback to the cloud.
- Follow the [REST contract](docs/api.md): IDs and amounts are strings, monetary calculations never use floating-point arithmetic, and `spent_at` is a calendar date.
- Keep the architecture minimal. Justify new features and dependencies. Do not present project assumptions as assignment requirements or as user-approved decisions.
- Never write secrets or expense contents to logs or the repository.
- Access checks for allowed users A/B and denied user C are mandatory. Run Go checks, frontend checks, and a Compose smoke test before delivery.
- Record stage results and verification results in [docs/tasks.md](docs/tasks.md). Track mandatory MVP tasks separately from optional checks.

## Go Code Style

- Write idiomatic, readable, maintainable Go. Optimize for clarity, not minimum line count.
- Use conventional multi-line `if`, `for`, `switch`, and error-handling blocks. Do not compress them into single lines.
- Do not combine multiple operations on one line merely to shorten code.
- Prefer explicit control flow, descriptive names, and clear separation of logical steps over clever shortcuts.
- Keep functions focused. Extract helpers when they make the code easier to understand, not simply to reduce line count.
- Avoid unnecessary interfaces, wrappers, generic helpers, architectural layers, and inline closures when ordinary statements are clearer.
- Follow existing project conventions and format Go code with `gofmt`.
- Handle errors explicitly; wrap them with useful context when appropriate. Make intentional error suppression clear.
- Write comments to explain intent, invariants, and non-obvious decisions, not to narrate obvious code.

## Database and Data Integrity

- Use parameterized SQL for data values. SQL identifiers and sort expressions must come from fixed allowlists.
- Keep transactions short and use context-aware database operations.
- Enforce Telegram expense ownership in SQL, not only in application code.
- Never use floating-point arithmetic for monetary amounts.
- Do not silently ignore database errors or alter transaction isolation semantics without justification.
- Preserve atomicity and idempotency guarantees when changing Telegram processing.

## Development Workflow

- Read this file and the relevant specifications before implementation.
- Work only within the requested scope. Do not implement future tasks proactively.
- Prefer small, reviewable changes over large rewrites.
- Preserve documented API contracts and existing behavior unless explicitly instructed otherwise.
- Keep transport-specific parsing in transport adapters; do not introduce HTTP- or Telegram-specific types into shared business logic.
- Explain why a new dependency is necessary before adding it.
- Add or update tests whenever behavior changes or a bug is fixed.
- Run relevant formatting, tests, and static checks after changes. Never claim a check passed unless it actually ran successfully.
- Do not silently modify unrelated files or rewrite existing documentation.
- At completion, summarize changed files, design decisions, checks actually executed, checks not executed, and known limitations.

## Refactoring

- Keep refactoring separate from feature work whenever practical.
- Preserve observable behavior unless the task explicitly requires a change.
- Avoid abstractions introduced solely for stylistic consistency.
- Do not replace straightforward code with complex generic solutions.

## Definition of Done

Before reporting any implementation task as complete:

1. Review the final `git diff` after running `gofmt`; formatting alone does not guarantee readable code.
2. Inspect every added or modified Go function against **Go Code Style**. Fix compressed statements, unclear names, hidden side effects, and unnecessarily complicated control flow introduced by this task.
3. Check architectural boundaries: shared business logic must not depend on HTTP, Telegram, or the LLM adapter, and changes must not introduce unnecessary coupling.
4. Review error handling, SQL safety, ownership constraints, and transaction behavior in the modified code.
5. Run relevant tests and static checks. Report failed or unavailable checks accurately; do not imply unexecuted checks passed.
6. Fix issues introduced by the current task before reporting completion. Do not refactor unrelated code without approval.
7. Summarize what changed, why, what was verified, and what remains outstanding.

**A task is not complete if its newly written code violates these rules, even if all tests pass.**
