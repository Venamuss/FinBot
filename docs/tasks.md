# FinBot Implementation Stages

Basis: [assignment requirements and project decisions](requirements.md), [architecture](architecture.md), and [REST contract](api.md). The application implements administrative REST CRUD, the React panel, member display names, owner-scoped Telegram conversations, a configurable LLM adapter, PostgreSQL persistence/idempotency, and CI configuration. Slice 05 completed local automated, browser and Docker QA and submission documentation; final real-Telegram A/B/C acceptance remains pending. The current requirement-by-requirement verdict is in [submission-readiness.md](submission-readiness.md). Earlier sections below are historical stage records, not current claims of missing components.

R01–R14 identify requirements from the original assignment, P01–P12 identify project decisions, and T01–T13 identify implementation and verification tasks. Contracts are documented; the actual status of the current increment is recorded below. Mark task completion together with the result and checks performed.

## First Vertical Slice — 16.10.2026

Implemented the Go entrypoint, minimal configuration, a bounded wait for database readiness, graceful shutdown, the domain model, and the application service. Expense creation, retrieval by ID, and listing are available: `POST /api/expenses`, `GET /api/expenses/{id}`, `GET /api/expenses`. The repository uses pgx and parameterized SQL; migrations are embedded in the binary and applied transactionally before HTTP starts. Compose has two services; PostgreSQL does not publish a port, HTTP binds to `127.0.0.1` by default, and data is stored in a named volume. The Docker build does not require a frontend.

| Task | Status in this increment | Remaining work to complete the full task |
|---|---|---|
| T01 | Incomplete: the contract has been read and verified for the implemented portion | A direct run against a configured LLM endpoint with tool calling |
| T02 | Partial: exact amounts, categories, IDs, calendar dates, and filter intervals have been checked | Relative bot periods and calculations based on the message/time zone |
| T03 | **Partial**: migration, Create/GetByID/List, filters, sorting, pagination, and database constraints | Update/Delete, totals, the user's last expense, and atomic constraints on bot operations |
| T04 | Partial: Go configuration, build, Compose, migrations, and a persistent volume | Telegram/allowlist/LLM configuration and the future frontend build |
| T05 | **Partial**: POST and two GET operations, JSON, errors, filters, sorting, and pagination | PATCH, DELETE, and `/api/members` |
| T06–T12 | Not implemented | Telegram, LLM, conversation, access checks, React, and full end-to-end acceptance |

Checks for the current increment:

| Check | Result |
|---|---|
| Documents and links | Read AGENTS.md and all five documents in docs/. Checked 27 internal links before implementation; all 34 after adding the instructions. Original assignment: [test-task-expenses.md](test-task-expenses.md) |
| `gofmt -w cmd internal migrations` | Passed from `backend/` |
| `go test ./...` | Passed; repository integration tests are explicitly skipped without `TEST_DATABASE_URL` |
| `go vet ./...` | Passed |
| `TEST_DATABASE_URL=… go test ./internal/postgres -run Integration -count=1 -v` | Passed against a real PostgreSQL 17 instance in Docker; separate random schemas with cleanup limited to their own test data |
| Domain/service unit tests | Verified int64 ID and amount ranges, `0.10 + 0.20 = 0.30`, rejection of rounding, categories, calendar boundaries, required fields, filters, and validation before the repository |
| HTTP tests | Passed checks for formats, nullable/empty comment, unknown/system fields, invalid JSON types, errors 400/403/404/405/415/500, `Allow`, Origin, filters, and pagination |
| PostgreSQL integration tests | Passed checks for Create/GetByID/List, owners A/B, AND filters and inclusive date boundaries, numeric sorting and secondary id, total and empty pages, an SQL-like comment, CHECK/NOT NULL, ranges, and reapplying migrations |
| Docker build without frontend | Passed with `docker build --network host -t finbot-slice-check-app .`; the initial default build network could not connect to the Go proxy (timeout). A clean build using this environment's default Docker network has not been verified |
| Compose HTTP smoke | Passed in the isolated project `finbot-slice-check`, `127.0.0.1:18080`, using a prebuilt image: real POST/GET, amount normalization, filters, sorting, pagination, and errors |
| Compose networking and storage | Inspection confirmed loopback HTTP, no published database port, and a persistent volume |
| Restart and migrations | Confirmed `docker compose restart`, `down`/`up` without deleting the volume, and retrieval of previous records over HTTP; normal process shutdown is recorded in the operational log |
| Frontend, Telegram A/B and denied C, live LLM | **Not checked**: outside the current scope and not yet implemented. REST filtering by A/B does not verify bot authorization or isolation |

Two defects found during verification were fixed: sorting by the text representation of an amount instead of the numeric column, and an application startup race when PostgreSQL was restarted at the same time. Repeated checks passed after the fixes. The application does not log expense contents or driver errors; PostgreSQL is configured to omit SQL/DETAIL from constraint error messages.

Reproducible startup and verification commands are in [README.md](../README.md); the HTTP smoke script is `scripts/smoke.py`. Further operations and stages are not implemented in this increment. The MVP is not ready; T03/T05 are not fully complete. Additional task T13 has not been performed.

## Readability Refactoring — 16.10.2026

Refactored the existing Go implementation without changing business rules, REST responses, SQL semantics, dependencies, or stage completion status. T03/T05 remain partial.

- Repository: separated WHERE construction and allowlisted ORDER BY selection from transaction execution; expanded SQL, row scanning, and query arguments; renamed filter/result variables. Read-only REPEATABLE READ, parameter positions, AND filters, numeric sorting, secondary id, and count/page consistency are preserved.
- Domain/service and HTTP: clarified names, expanded validation and JSON decoding, reduced nesting, and moved decimal pagination parsing into a focused private function.
- Startup/configuration and migrations: expanded configuration and SQL, clarified context/channel names, and documented intentional cleanup-error suppression. JSON response write failures are handled explicitly without changing an already-started response or logging expense data. Existing unit test setup now checks parsing errors explicitly.
- Verification: `gofmt -w cmd internal migrations`, `go test ./...`, and `go vet ./...` passed from `backend/`. Repository integration tests passed against real PostgreSQL 17 with `TEST_DATABASE_URL`; the existing Compose HTTP smoke passed on `127.0.0.1:18080` with a rebuilt image. Docker build used `--network host` due to the previously recorded build-network limitation. Frontend and bot access checks remain unavailable because those components are not implemented.
-

Refactored `handler.go`, `handler_test.go`, `migration_test.go`, `repository.go`, `repository_test.go`, and `model_test.go`. HTTP dispatch now delegates detail retrieval to a focused handler and uses named status constants and clearer request checks. SQL formatting and argument layout are expanded without changing parameter positions, filters, sorting, or transaction isolation. Tests use descriptive variables, keyed case tables, named subtests, smaller assertions, and separate precision/default/error scenarios. Expected repository calls are explicit test data; the HTTP representation assertion now checks all eight fields. Existing scenarios are retained.

## Goose Migration Runner — 16.10.2026

Replaced the custom migration runner with Goose v3.27.3 Provider using embedded SQL and the existing pgx pool through its database/sql adapter. Goose requires pgx v5.10.0; no second database driver, ORM, CLI service, or application layer was introduced. Automatic Up still runs before HTTP startup; each migration is transactional, concurrent startups use the previous advisory lock key, and Goose logging is disabled to keep SQL and driver details out of application logs.

The initial migration now has Goose Up/Down sections and idempotent CREATE statements. A database created by the original runner retains its expenses, index, IDs, identity sequence, and old `schema_migrations` table. Goose records its own history in `goose_db_version`; compatibility assumes the unchanged original FinBot schema. Down is supported in the SQL file but is never called automatically.

Verification passed: `gofmt`, `go test ./...`, `go vet ./...`, and all Go tests with `TEST_DATABASE_URL` against PostgreSQL 17. Integration coverage includes clean/repeated application, simultaneous migration calls, legacy-schema upgrade with data and sequence preservation, and Down/Up in a random test schema. Existing repository and database-constraint tests pass with the Goose runner.

Docker build with `--network host` and Compose HTTP smoke passed. The existing Compose volume was upgraded from the old history without losing its 16 prior synthetic expenses; old records remained available through HTTP. After a simultaneous app/database restart, HTTP persistence verification passed and Goose version 1 remained registered exactly once. Test containers were stopped and the volume retained. Frontend and Telegram checks remain unavailable; T03/T05 are still partial.

| Check | Result |
|---|---|
| `gofmt`, `go test ./...`, `go vet ./...` | Passed. Go checks use `GOCACHE=/tmp/finbot-readability-go-cache` because the default cache is read-only in the sandbox |
| All Go tests with `TEST_DATABASE_URL` and `-count=1 -v` | Passed against an isolated PostgreSQL 17 Compose database, including repository, database constraints, migration reapplication, legacy upgrade, concurrent migration, and Down/Up scenarios |
| Docker build and Compose HTTP smoke | Passed with a rebuilt image, `docker build --network host`, and the existing `scripts/smoke.py` against isolated project `finbot-readability-check` on `127.0.0.1:18081`; test credentials and smoke state are outside the repository |
| Frontend and Telegram allowed A/B / denied C | Not run: these components are not implemented. Repository owner filters do not verify bot authorization; these mandatory MVP checks remain pending |

Additional check T13 (live local LLM) remains not performed. This refactoring does not complete the MVP.

## Focused Backend Review and Refactoring — 16.10.2026

Addressed the three requested findings within the existing Create/GetByID/List increment. No dependencies, architectural layers, or interfaces were added; existing REST responses and validation messages are preserved except for the documented oversized-body response. T03/T05 remain partial.

- `httpapi/handler.go`: POST JSON bodies are bounded by `http.MaxBytesReader` to 1 MiB (1,048,576 bytes), including whitespace. The original read error reaches the handler so `http.MaxBytesError` returns `413`, code `request_too_large`, and `fields: {}`. Origin, media-type, routing, and query checks retain their precedence. The limit and error are documented in [api.md](api.md).
- `expense/service.go` and `httpapi/filter.go`: GetByID accepts an `int64`; List accepts an `expense.Filter`. Path ID parsing and query parsing, validation, and defaults now belong to HTTP transport. Query-filter tests moved from `expense/model_test.go` to `httpapi/filter_test.go`; invalid filters and IDs are also checked through the handler to ensure they never reach the repository.
- `postgres/repository.go`: deferred rollback uses a separate one-second timeout, independent of request cancellation. The original operation error and read-only REPEATABLE READ snapshot are preserved. Cleanup failure is intentionally suppressed; pgx discards the failed connection.
- Regression tests: `httpapi/handler_test.go` covers bodies below, exactly at, and over the limit, malformed oversized JSON, UTF-8 byte counting, unknown body lengths, the 413 envelope, and absence of repository writes. `postgres/rollback_test.go` uses an in-memory PostgreSQL protocol peer that fails the count query and stops answering rollback; real pgx cleanup returns within the bound, retains the original query error, and releases the pool connection.

| Check | Result |
|---|---|
| `gofmt -w internal/httpapi internal/expense internal/postgres` | Completed from `backend/` |
| `go test ./...`, `go vet ./...` | Passed with `GOCACHE=/tmp/finbot-go-cache`; integration tests skip explicitly when `TEST_DATABASE_URL` is absent |
| All Go tests with `TEST_DATABASE_URL`, `go test ./... -count=1 -v` | Passed against isolated PostgreSQL 17 in Compose, including repository filters/sorting/pages, database constraints, legacy migration upgrade, concurrent migrations, Down/Up, and the stalled rollback regression |
| Docker build and Compose HTTP smoke | Passed with a rebuilt image using `docker build --network host`, isolated project `finbot-focused-check` on `127.0.0.1:18082`, and the existing `scripts/smoke.py` |
| Real HTTP body-limit smoke | Passed for Content-Length and chunked transfer: exact 1 MiB returned 201; one byte over returned the documented JSON 413; rejected requests created no expenses |
| Frontend and Telegram allowed A/B / denied C | Not run: these components are not implemented and are outside this refactoring. These mandatory MVP checks remain pending |

Test credentials and smoke state stayed outside the repository. The isolated test containers, network, and disposable volume were removed after verification; the running application and pre-existing `compose.yaml` edits were left untouched. PATCH, DELETE, Telegram, LLM, and frontend work were not implemented. Additional check T13 remains not performed; the full MVP is not complete.

## Readability Follow-up for Backend Review — 16.10.2026

Reviewed the recent Go changes against the updated AGENTS.md readability criteria and inspected the actual diff after gofmt. In `httpapi/filter.go`, limit and offset now have separate parsing branches. `httpapi/handler.go` separates transport validation from service calls. In `httpapi/handler_test.go` and `httpapi/filter_test.go`, request setup, expected repository calls, and response assertions are explicit; the 413 envelope check is a focused test helper. In `postgres/rollback_test.go`, the protocol peer follows startup, BEGIN, failed count, and unanswered rollback in order, with a helper limited to receiving and checking query messages. Existing scenarios and observable behavior, including the 1 MiB limit, 413 contract, one-second rollback timeout, and transaction isolation, are preserved. No dependencies or interfaces were added.

Verification passed: gofmt on all five edited Go files, `go test ./...`, `go vet ./...`, `git diff --check`, all Go tests with `TEST_DATABASE_URL` and `-count=1 -v` against isolated PostgreSQL 17, and the existing Compose HTTP smoke against a rebuilt image on `127.0.0.1:18082`. The Docker build used `--network host`. The isolated test containers, network, and disposable volume were removed after verification. Existing AGENTS.md and Compose edits were preserved.

Frontend and Telegram A/B/C checks remain unavailable because those components are not implemented. T03/T05 remain partial; additional check T13 remains not performed.

## Vertical Slice 02 — Complete Expense CRUD — 16.10.2026

Implemented `PATCH /api/expenses/{id}`, `DELETE /api/expenses/{id}`, and `GET /api/members`. T05 is complete for the documented REST API. T03 now includes administrative CRUD and member lookup, but remains partial: totals, the user's most recent expense, and bot ownership/idempotency operations belong to later stages.

- `expense/update.go` and `expense/service.go`: transport-independent partial-update input and validated changes, plus Update/Delete/ListMembers methods. Omitted fields remain unchanged; explicit `comment: null` clears a comment, while an empty string remains an empty string. Validation completes before any repository call.
- `postgres/repository.go`: one parameterized `UPDATE ... RETURNING` applies all changes and advances `updated_at`, preserving `id` and `created_at`. It updates the current row directly to avoid overwriting concurrent changes to omitted fields. One `DELETE` statement physically removes the record; zero affected rows returns not found. Member lookup selects distinct existing owners in numeric order. REST owner reassignment has no allowlist or registration restriction.
- `httpapi/handler.go`: the new routes preserve the existing JSON/error formats and keep path/query/body parsing in HTTP. POST and PATCH share Origin, media-type, JSON-type, unknown/system-field, and 1 MiB body checks; [api.md](api.md) now explicitly documents the same limit for PATCH. DELETE checks Origin and returns a bodyless 204. Unsupported methods return the applicable `Allow` header.
- Tests: added `expense/update_test.go`, `httpapi/crud_test.go`, and `postgres/crud_test.go`; extended existing repository doubles and HTTP error cases. Extended `scripts/smoke.py` for complete CRUD, comment states, failed-update preservation, owner reassignment, and member removal. No dependencies, schema migrations, Telegram, LLM, or frontend implementation were added.

| Check | Verified result |
|---|---|
| `gofmt`, final diff/readability review, `git diff --check` | Passed; reviewed every added/modified Go function, transport boundaries, parameterized SQL, and atomic statement behavior |
| `go test ./...` and `go vet ./...` from `backend/` | Passed with `GOCACHE=/tmp/finbot-go-cache`; the run without `TEST_DATABASE_URL` explicitly skips PostgreSQL integration tests |
| All Go tests with `TEST_DATABASE_URL`, `go test ./... -count=1` | Passed against isolated PostgreSQL 17. New coverage verifies partial/full updates, exact amounts, timestamps, comment states, owner reassignment and filters, numeric unique members, missing/repeated deletion, concurrent disjoint updates, and exactly one successful concurrent deletion |
| Atomic failure checks against PostgreSQL | Passed: mixed valid/invalid service input leaves every field unchanged; a database CHECK failure rolls back the complete update; a test-only AFTER DELETE trigger failure restores the deleted row and member lookup |
| Docker build and Compose HTTP smoke | Passed with `docker build --network host -t finbot-slice02-check:latest .`, isolated Compose project `finbot-slice02-check`, and expanded `scripts/smoke.py` on `127.0.0.1:18083`. Compose's initial build command failed because BuildKit required explicit host-network entitlement; the direct Docker build succeeded |
| Frontend checks and Telegram allowed A/B / denied C | Not executed: these components are not implemented and are outside this slice. Repository owner filters do not verify bot authorization; mandatory MVP access checks remain pending |
| Live LLM / optional local check T13 | Not executed; outside this slice |

Test credentials and smoke state were kept outside the repository. The isolated test containers, network, and disposable volume were removed after verification. The existing application deployment was not changed. Work stops at this slice; the full MVP remains incomplete.

## Vertical Slice 03A — Bot-ready Expense Operations — 17.10.2026

Implemented the expense operations required by this slice of T02/T03. The administrative REST contract and privileges are preserved. No dependencies, schema migrations, Telegram transport, LLM adapter, or frontend were added.

- `expense/owned.go`: a focused owner service and persistence boundary alongside the administrative service. Owner identity is a separate trusted-caller argument; filter and update inputs have no `user_id`, and owner updates cannot reassign records. Validation reuses existing amount/category/date/comment rules and rejects invalid owners, IDs, ranges, pagination, empty updates, and missing selected-record versions before repository access.
- `postgres/owned.go`: owner-scoped retrieval, category/date filtering, pagination, exact totals, and latest selection ordered by `created_at DESC, id DESC`. Lists reuse the existing repeatable-read count/page snapshot. Totals use PostgreSQL `SUM(NUMERIC)` and return two-decimal strings, including `0.00`, without imposing the single-expense or int64 aggregate limit. A zero owner cannot become an unscoped list or total query.
- Owner updates and deletes check `user_id`, `id`, and the selected `updated_at` in one parameterized SQL statement. Missing, foreign, and stale records return the same `ErrNotFound`; the later conversation layer must clarify stale selections. `postgres/repository.go` also makes administrative updates advance `updated_at` by at least one database microsecond, so even a clock correction cannot restore an old selection's version. Ownership checks remain atomic when a statement waits for another writer; transaction isolation is unchanged.
- `expense/period.go`: `DateRangeAt` accepts a supplied reference timestamp and the caller's configured `*time.Location`, with inclusive today/yesterday, last seven days, Monday-through-today, and month-start-through-today ranges. Calculations use calendar arithmetic, never the wall clock. Loading the deployment's time zone and deriving identity from `message.from.id` remain responsibilities of the future Telegram/configuration stage.
- Added `expense/owned_test.go`, `expense/period_test.go`, and `postgres/owned_test.go`. Coverage includes A/B retrieval and mutation isolation; combined filters and inclusive bounds; exact `0.10 + 0.20`; empty results; totals exceeding int64 hundredths; creation-time ties and purchase-date independence; stale selections after panel edits, reassignment, and deletion; competing updates/deletes; and SQL guard rechecks after a verified row-lock wait. Calendar cases include Moscow midnight, another configured zone, month/year boundaries, leap day, Monday/Sunday, and DST transitions.

| Check | Verified result |
|---|---|
| `gofmt` on all added/modified Go files | Passed |
| `go test ./...` and `go vet ./...` from `backend/` | Passed with `GOCACHE=/tmp/finbot-go-cache`; PostgreSQL tests explicitly skip when `TEST_DATABASE_URL` is absent |
| All Go tests with `TEST_DATABASE_URL`, `go test ./... -count=1 -v` | Passed against disposable PostgreSQL 17 in Compose, including all existing HTTP/repository/migration tests and the new owner/concurrency tests. Test schemas are independently created and cleaned up |
| Docker build and Compose HTTP smoke | Passed with `docker build --network host -t finbot-slice03a-check:latest .`, isolated project `finbot-slice03a-check`, and the existing `scripts/smoke.py` on `127.0.0.1:18084`; administrative CRUD, owner reassignment, member lookup, filtering, sorting, pagination, and errors remain compatible |
| Final diff, readability/architecture review, and `git diff --check` | Passed; reviewed all added/modified Go functions, SQL parameters and ownership guards, error propagation, and unchanged transport boundaries |
| Frontend checks; Telegram allowed A/B and denied C; duplicate-delivery transaction | Not executed: these components are not implemented and are outside Slice 03A. Repository A/B isolation does not verify the bot allowlist. These mandatory MVP checks remain pending in T06/T09/T10–T12 |
| Live configured LLM and optional local check T13 | Not executed; outside this slice. T13 remains an additional check |

Test credentials, the temporary Compose override, and smoke state stayed outside the repository. The disposable test containers, network, and volume were removed after verification; the existing deployment was left untouched. The owner service is ready for a trusted bot caller but does not itself implement allowlist checks, conversation focus, previous-amount clarification, or committing a Telegram processing result with an expense change. Those remain later-stage work. Work stops after Slice 03A; the MVP is incomplete.

## Standard HTTP Mux Refactoring — 17.10.2026

Replaced manual HTTP dispatch in `httpapi/handler.go` with Go 1.22-style `http.ServeMux` method/path patterns and `Request.PathValue("id")`. Retained the five-second request timeout, query validation order, JSON 404/405 envelopes, exact Allow headers, and explicit HEAD rejection. ServeMux now supplies its standard path canonicalization and per-segment URL unescaping. No dependencies or business/repository logic changed. Added routing regression cases in `httpapi/handler_test.go`.

Verification: `gofmt`, `go test ./...`, `go vet ./...`, final diff review, and `git diff --check` passed. PostgreSQL integration tests skipped without `TEST_DATABASE_URL`. A rebuilt image (`docker build --network host`) passed the existing HTTP smoke script against disposable Compose project `finbot-mux-check` on port 18085; its containers, network, and test volume were removed afterward. Existing unrelated working-tree changes were preserved.

Mandatory MVP checks for the frontend and Telegram allowed A/B / denied C were not run because those components are not implemented. Optional live local LLM verification remains unperformed. Stage completion status is unchanged.

## Vertical Slice 03B — Configurable LLM Adapter — 17.10.2026

Implemented a standalone `internal/llm` client using only the Go standard library. Environment configuration and Compose support `LLM_BASE_URL`, `LLM_MODEL`, optional `LLM_API_KEY`, and a positive Go-duration `LLM_TIMEOUT`. Initial defaults select OpenRouter and `google/gemma-4-31b-it:free` with a 30-second timeout; provider/model selection is outside the adapter. No startup probe or application request invokes the client yet.

`New(Config)` validates settings; `Complete(context.Context, Request)` sends non-streaming Chat Completions with system/user messages, JSON Schema function definitions, and optional string/named-function tool choice. Responses expose nullable content, tool calls with `json.RawMessage` argument objects, and the finish reason. The caller can require exactly one tool call. Parsing rejects malformed JSON/envelopes, missing tool fields, invalid argument JSON, duplicate IDs, and inconsistent/incomplete tool completions. Unknown operation names and business fields are deliberately returned for later caller validation, never dispatched here.

Successful response bodies are bounded to 1 MiB, including chunked/decompressed data. HTTP failures return only a status and fixed category; transport errors are sanitized. Cancellation and timeouts cover both headers and body reads. Empty keys omit Authorization entirely. Redirects are rejected, with no retries, alternate endpoint, or model fallback. No dependencies, SQL, migrations, expense logic, REST handlers, or Telegram transport changed.

| Task | Current status after Slice 03B |
|---|---|
| T01 | **Incomplete**: REST contract checked in previous slices; live tool calling against the selected model remains unverified. No `LLM_API_KEY` is available in the environment or `.env`; no live request was made and no credentials were requested |
| T07 | **Implementation and automated acceptance criteria passed**: arguments, configuration/model selection, optional key, timeouts, malformed responses, and no fallback checked. Full stage sign-off remains pending its documented T01 live-verification dependency |
| T04 | LLM configuration added; Telegram/allowlist configuration and frontend build remain outstanding |
| T06, T08–T12 | No new completion claims; Telegram integration, conversational CRUD, full access checks, frontend, and end-to-end acceptance remain outside Slice 03B |
| T13 (additional) | Live local LLM check not executed; remains optional and separate from mandatory T01 |

| Check | Actual result |
|---|---|
| `gofmt` | Completed on new/modified Go files |
| `go test ./...` | Passed with `GOCACHE=/tmp/finbot-go-cache`; initial sandbox attempt could not open a loopback listener, then passed with local networking enabled. PostgreSQL integration tests skipped without `TEST_DATABASE_URL` |
| `go vet ./...` | Passed |
| Adapter/configuration tests | Passed using `httptest.Server`: exact payload/path/model/schema and tool choices; key present/absent; valid text/tools and multiple-call policy; malformed/missing structures and arguments; 400/404 provider rejection, 401/403, 429, 5xx; connection failure and short body; cancellation/deadlines before headers and during body; exact/over-limit bodies with known/chunked lengths; sanitized errors and redirects/no retry |
| Final diff/readability review and `git diff --check` | Passed; new files also checked with `git diff --no-index --check` |
| Docker build and Compose smoke | Passed with `docker build --network host -t finbot-slice03b-check:latest .`, isolated Compose project `finbot-slice03b-check`, and `scripts/smoke.py` on `127.0.0.1:18086`. CRUD, partial updates, reassignment, members, filters, sorting, pagination, and errors passed with an unreachable configured LLM endpoint and empty key |
| Frontend checks and Telegram allowed A/B / denied C | Not executable: those components are not implemented. These remain mandatory MVP checks; adapter tests do not establish bot authorization |
| Live OpenRouter/tool-calling check | Not executed: key unavailable. Selected model availability and provider compatibility remain unverified |

Readability and architectural review covered every new/modified Go function and the final diff after formatting: explicit multiline control flow, safe errors, no added interfaces or business/transport coupling. Ownership and transaction code are unchanged. Temporary smoke credentials/state remained outside the repository. The isolated test containers, network, and disposable volume were removed after verification. Work stops after Slice 03B; Slice 03C and the full MVP are not implemented.

## LLM Client Readability Refactoring — 17.10.2026

Refactored only `internal/llm/client.go`: renamed the transport field to `httpClient`, made constructor setup explicit, extracted HTTP request construction from `Complete`, separated request/response body variables, and expanded tool-definition and named-choice validation. Public types, payloads, validation results, error categories, response limits, cancellation, timeouts, redirects, and no-fallback behavior are preserved. No dependencies, business logic, SQL, or other adapter files changed; existing behavioral tests cover the refactoring.

Verification: `gofmt`, `go test ./...`, `go vet ./...`, final diff/readability and architectural review, and `git diff --check` passed. Go checks used `GOCACHE=/tmp/finbot-go-cache`; tests ran with local networking enabled for `httptest.Server`. PostgreSQL integration tests skipped without `TEST_DATABASE_URL`. Docker build with `--network host` and the existing HTTP smoke passed against isolated Compose project `finbot-llm-readability-check` on `127.0.0.1:18087`, with an unreachable LLM endpoint and empty key. The disposable containers, network, volume, and temporary credentials/state were removed after verification.

Frontend and Telegram A/B/C checks remain unavailable because those components are not implemented. Live provider checks were not rerun for this behavior-preserving refactoring. T01/T07 status and the Slice 03B boundary are unchanged.

## Vertical Slice 03C — Telegram and Conversational Expense CRUD — 17.10.2026

Completed the three checkpoints sequentially. 03C.1 passed fake Telegram/configuration tests and static checks before conversation implementation; 03C.2 passed the full Go suite, PostgreSQL ownership/search tests, and static checks before transactional duplicate protection. The initial run was stopped by the user before final review and this record. On resumption, final review, verification, documentation, and test-environment cleanup were completed. This closes the automated implementation scope of Slice 03C, not the full MVP or live acceptance.

| Checkpoint | Implemented behavior and verified result |
|---|---|
| 03C.1 — Telegram transport | Standard-library Bot API client with 25-second long polling, 40-second HTTP timeout, at most three attempts per request, bounded backoff, rejected redirects, bounded response bodies, and sanitized errors. Accepts private text from a non-bot sender; identity comes exclusively from `message.from.id`. A/B access, C denial, empty/malformed allowlist, message filtering, polling offsets, HTTP failures, and cancellation passed with fake servers. No Telegram framework, commands, buttons, or webhooks |
| 03C.2 — Conversational CRUD | Separate orchestrator validates exactly one known tool, object fields/types/required fields, duplicate keys, and business values; `user_id` is forbidden. Reuses the existing expense services for create/list/total/update/delete. Independent in-memory focus and pending clarification expire after 30 minutes of inactivity. Relative dates use the original message timestamp and configured location, including a missing-field follow-up across Moscow midnight. Focus, latest-by-creation fallback, previous-amount mismatch, multiple candidates, cancellation, and owner-only choices passed. Replies use actual operation results; model success claims are ignored |
| 03C.3 — Idempotency/security | Migration 002 adds `telegram_updates`. A unique `update_id` reserves execution inside the same REPEATABLE READ transaction that commits the expense mutation and result. SQL scopes replay, reads, totals, candidate searches, and mutations to the owner; mutations retain the existing `updated_at` guard. Five-second transaction context reaches every callback query; cleanup has an independent one-second bound. At most three local transaction attempts handle serialization/deadlock errors. LLM and Telegram calls happen outside transactions. Twelve concurrent deliveries committed one mutation; replay after restart, rollback, connection loss before commit, timeout, send failure after commit, and stale panel changes/deletion/reassignment passed |

Architecture and changed files:

- Added `internal/telegram/{client.go,bot.go,client_test.go}` and `internal/conversation/{tools.go,orchestrator.go,execute.go,orchestrator_test.go}`. The LLM adapter remains independent of permissions, persistence, and business operations.
- Added `internal/postgres/{telegram.go,telegram_test.go}` and `migrations/002_telegram_updates.sql`. Updated the existing repository to work with either its pool or the transaction supplied by the processing store, preserving administrative List's read-only REPEATABLE READ behavior. Bot List uses a savepoint in its outer REPEATABLE READ transaction. A minimal database interface reuses the existing SQL instead of duplicating CRUD.
- Updated `internal/expense/owned.go`, `internal/postgres/{owned.go,owned_test.go}` for literal, case-insensitive owner-scoped comment search. SQL values remain parameterized; REST fields, responses, filters, and privileges are preserved.
- Updated `internal/config/{config.go,config_test.go}`, `cmd/finbot/main.go`, `.env.example`, and `compose.yaml` for token, allowlist, time zone, wiring, and shutdown. Empty token disables polling; empty allowlist denies all bot users. No dependency changes.
- Updated `internal/postgres/migration_test.go` for both migration Down steps, `internal/postgres/rollback_test.go` for asynchronous pool cleanup, and the startup/status descriptions in `README.md`, `docs/architecture.md`, and this file.

Final review covered every new/modified Go function after formatting, control flow and error handling, transport boundaries, SQL parameters/ownership, freshness checks, transaction lifetimes, and the complete tracked/untracked implementation diff. Earlier review fixes retained in the final implementation prevent a candidate choice from bypassing a previous-amount mismatch, direct model mutations from bypassing a pending selection, and callback queries from outliving the transaction deadline. Final resumption strengthened the midnight regression and fixed a test-only timing assumption: pgx/puddle destroys broken pool connections asynchronously, so the existing rollback test now waits for release within a bound instead of requiring an immediate statistics update. Application behavior and transaction semantics were not changed by those final test corrections.

| Verification | Actual result |
|---|---|
| Formatting and diff | `gofmt` completed; final `git diff --check`, whitespace checks for new files, and readability/architecture review passed |
| `go test ./... -count=1 -timeout=120s` | Passed with `TEST_DATABASE_URL` against disposable PostgreSQL 17; integration tests actually ran in isolated random schemas. `GOCACHE=/tmp/finbot-go-cache`; local networking enabled for fake HTTP servers |
| `go test -race ./internal/conversation ./internal/postgres ./internal/telegram -count=1 -timeout=120s` | Passed with the same real PostgreSQL database. An earlier run exposed the rollback test timing assumption described above; the full affected suite passed after its correction |
| Rollback test stability | `go test -race ./internal/postgres -run '^TestListRollbackUnavailableConnection$' -count=5 -timeout=30s` passed after correction |
| `go vet ./...` | Passed after the final test corrections |
| Automated functional/security coverage | Fake-model scenarios for all six assignment examples; missing data; malformed/unknown/multiple tools; permission spoofing; A/B ownership, C denial and revoked replay; independent/expired contexts; mismatch and deferred selection; duplicate/concurrent updates; database errors/rollback; terminated transaction connection; five-second callback deadline; stale administrative edits/deletion/reassignment; committed send-failure replay; plain-text response length |
| Docker build and Compose REST smoke | Passed with `docker build --network host -t finbot-slice03c-check:latest .`, isolated project `finbot-slice03c-check`, and `scripts/smoke.py` on `127.0.0.1:18088`. CRUD, partial updates, reassignment, members, filters, sorting, pagination, and errors remain compatible with empty token/allowlist and an unreachable LLM endpoint |
| Restart, migrations, persistence | Persistence smoke passed after simultaneous app/database restart and after `down`/`up` preserving the test volume. Application logs showed normal HTTP stop/start. The interrupted run's immediate post-restart probe had failed with `ConnectionResetError`; resumed verification ran after readiness and passed |
| Frontend checks | Not executable: no React/frontend project exists in this slice. T10/T11 remain pending; no frontend work was performed |
| Live Telegram and configured LLM | Not executed. Automated tests require no actual Telegram/OpenRouter credentials; real model understanding/provider compatibility and manual A/B/C acceptance remain unverified |

Mandatory MVP status: T06/T08/T09 implementation and automated acceptance criteria passed. T01's live tool-calling requirement and the corresponding T07 dependency remain outstanding; T10/T11 and full T12 acceptance are incomplete. T04 now includes bot configuration, but the eventual frontend build remains outside this slice. Optional live local LLM check T13 was not performed and remains separate from mandatory acceptance.

Known limits: one sequential polling process; conversation focus/clarifications are not restored after restart; lists show the first 20 records and total count; more than 10 candidates require a narrower query; description search is a literal comment substring; displayed comments are truncated. Failed LLM/invalid-tool messages persist a safe processing result without an expense mutation. Telegram delivery is not exactly once: an uncertain send can repeat the reply, and a persistent send/database failure pauses the polling queue until recovery. Result retention/outbox management is outside this slice. These limits are documented in README. Synthetic credentials/state stayed outside the repository; the isolated test containers, network, and disposable volume were removed after verification. Work stops at Slice 03C.

## Telegram Logging — 17.10.2026

Added structured `slog` events in `internal/telegram/bot.go` for processing start/completion, delivery, access denial, polling/handler/delivery failures, and scheduled retries. Events include the component, update/sender IDs, processing duration, or polling offset/delay as applicable. Ignored and already acknowledged updates and batch receipt use DEBUG. Cancellation does not produce failure/retry alarms. Message/response text, expense contents, tokens, and dependency error strings are excluded. Existing default logger configuration is reused; no dependencies, authorization rules, offsets, transaction semantics, or retry delays changed.

Added `internal/telegram/bot_test.go` covering allowed A/B and denied C, denial/response delivery errors, handler errors, ignored updates, sensitive-data exclusion, retained offsets across polling and processing failures, successful recovery, and polling cancellation. Final formatted code/diff review covered all changed functions, error paths, transport boundaries, and unchanged ownership/idempotency behavior.

Verification: `gofmt`, `git diff --check`, `go test ./...`, and `go vet ./...` passed. The initial sandboxed test attempt failed because local HTTP test sockets were prohibited; the complete suite passed with local networking enabled. A separate `TEST_DATABASE_URL=… go test ./... -count=1` passed against isolated PostgreSQL 17, including existing A/B/C, ownership, replay, and transaction integration tests. Docker build with `--network host` and Compose HTTP smoke passed on `127.0.0.1:18089` using the isolated `finbot-telegram-logs-check` project and an empty bot token/allowlist. Its containers, network, and disposable volume were removed afterward.

Mandatory MVP limits are unchanged: frontend checks cannot run because no frontend project exists; live Telegram/configured LLM acceptance was not performed. Optional race checks and the live local LLM check T13 were not rerun for this logging change. No future stage was implemented.

## LLM Logging — 17.10.2026

Added structured `slog` events at the LLM adapter's `Complete` boundary: request start with message/tool counts, completion with validated finish reason/tool-call count, cancellation at INFO, and failures at ERROR with the adapter's existing sanitized error and HTTP status where available. Terminal events include duration in milliseconds. Request validation and HTTP/response processing remain inside this boundary so every attempt emits one terminal event. No prompts, generated content, tool names/arguments/schemas, configured model/endpoint, headers, proxy details, or keys are logged. Existing JSON stdout logging makes these events available through `docker compose logs app`; no configuration or dependency change was needed.

Added `internal/llm/logging_test.go` covering text/tool success, HTTP authentication/rate-limit/provider/redirect errors, malformed/oversized responses, untrusted finish reasons, network failures, timeout, invalid requests, and cancellation. Tests verify metadata, preserved errors/request counts, and exclusion of private request/provider/transport data. Updated the Telegram test log decoder in `internal/telegram/bot_test.go` to select Telegram events from the shared application log stream; pipeline tests now also encounter valid LLM events. Production authorization, SQL ownership, transactions, retries, and fallback behavior are unchanged. Reviewed all changed functions and the final formatted diff.

Verification: `gofmt`, `git diff --check`, and `go vet ./...` passed. `TEST_DATABASE_URL=… go test ./... -skip '^TestAPIRequestDiagnostics$/^timeout$' -count=1 -timeout=90s` passed against isolated PostgreSQL 17, including LLM tests, Telegram A/B/C, the polling pipeline, ownership, replay, and transaction checks. Full unfiltered test attempts were stopped after the independent existing `TestAPIRequestDiagnostics/timeout` hung in `httptest.Server.Close` with server handlers waiting on request cancellation; that test was not modified by this task. All its other subtests passed separately, and only its timeout subtest was skipped in the final run. Docker build with `--network host` and Compose HTTP smoke passed for the isolated `finbot-llm-logs-check` project on dynamically assigned `127.0.0.1:32768` (the initially selected fixed port was occupied). Test containers, network, and the disposable volume were removed.

Mandatory MVP limits remain: no frontend project exists for frontend checks; live Telegram/configured LLM acceptance was not run. Optional race checks and the local LLM check T13 were not performed for this change. The independent Telegram diagnostic timeout test remains outstanding.

## Telegram Tool Calling and Listing Fix — 17.10.2026

Investigated the existing OpenAI-compatible adapter, conversation dispatch/validation, and PostgreSQL owner queries without changing provider, model, dependencies, REST contracts, or database code. Preserved the pre-existing workspace changes.

Confirmed causes using the configured OpenRouter `openai/gpt-4.1-mini` endpoint with synthetic context only:

- `tool_choice: "required"` was already sent, but parallel calls were not disabled. A reproduced today-listing response contained **seven tool calls**, triggering `ErrToolCallCount`. That sentinel is returned for valid responses containing zero or multiple calls when the local one-call flag is set. Existing historical failure logs lacked the count, so the exact count of every earlier failure cannot be recovered.
- The model requested unnecessary clarification for a date-free list, or generated a single list call containing both `period` and explicit dates plus unrequested category/query filters. The adapter correctly accepted one structurally valid call; conversation filter validation then rejected the conflicting dates. Thus `finish_reason=tool_calls` and `tool_call_count=1` did not establish successful expense dispatch.
- The server also lacked the documented today default for lists/totals. An empty filter previously selected every date instead of the original message's local calendar date.

Changes: send `parallel_tool_calls: false` alongside the existing required tool choice; use strict nullable list/total schemas, including null in category/period enums; clarify list intent and unused-filter semantics in prompts/descriptions; default absent dates on the server. Nullable read filters remain separate from mutation fields, where null is still rejected except for comments. Invalid model responses and invalid expense filters now have distinct safe Russian replies. LLM failures retain only allowlisted finish reasons and call counts; conversation diagnostics log known operation names and rejection stages. No messages, raw names from unknown tools, argument values, expense data, secrets, or provider bodies are logged.

[Official OpenAI function-calling documentation](https://developers.openai.com/api/docs/guides/function-calling) defines required choice as one or more calls and disabled parallel calling as at most one. Together they request the intended single call on GPT-4.1 Mini; local rejection remains necessary for malformed, incomplete, or nonconforming provider responses. The schema uses required nullable fields as specified for strict mode.

Reviewed SQL ownership: `ListOwned` and `TotalOwned` pass the trusted owner into parameterized `user_id` predicates, including list counts and aggregates. Get/latest/candidate queries and writes remain owner scoped. Authorization still precedes model, replay, and expense access; no network request moved inside a transaction and duplicate delivery behavior is unchanged.

| Mandatory verification | Actual result |
|---|---|
| Formatting and final review | `gofmt`, final diff/readability/error/ownership/transaction review, and `git diff --check` passed |
| Complete Go suite | `TEST_DATABASE_URL=… go test ./... -count=1 -timeout=120s` passed against disposable PostgreSQL 17, including real SQL integration, A/B isolation, C denial, replay, and the real adapter/orchestrator/polling pipeline |
| New regressions | Passed: date-free/today lists, all categories, nullable/omitted filters, explicit dates and time-zone boundary, totals/empty results, malformed JSON/types/unknown fields/tools, invalid periods/categories/dates, conflicting/reversed dates, zero/multiple calls, safe replies and diagnostics, saved-failure replay, nullable enums, and exact wire controls |
| Static analysis | `go vet ./...` passed |
| Configured live LLM | Passed eight final calls through the actual adapter and orchestrator: three empty lists, expense creation, the three populated lists with conversation focus, and a total. All returned exactly one expected tool call. Tests checked the configured allowlist before calls and used only synthetic in-memory expense data; no real expense writes or Telegram sends occurred. The temporary diagnostic harness was removed |
| Compose build/smoke | Docker build with `--network host`, isolated project `finbot-listing-check`, and `scripts/smoke.py` at `127.0.0.1:18092` passed. Persistence passed after app/database restart and readiness. The immediate post-restart probe initially encountered a connection reset; rerunning after Compose readiness passed. Test stack/volume removed afterward; existing stacks left running |
| Frontend | Unavailable: no frontend project, package manifest, or TypeScript configuration exists. T10/T11 remain pending |

The initial sandboxed Go run could not open loopback test sockets; verification ran with local networking enabled. The first full focused run also reproduced the previously documented Telegram diagnostic timeout-test hang: handlers waited for cancellation with unread POST bodies while `httptest.Server.Close` waited for handlers. Added an explicit test-only release before server shutdown. The subsequent **unfiltered** full suite passed, including that timeout subtest. After the persistence restart, a verification attempt used the old dynamically assigned database port and failed to connect; re-reading the published port and rerunning the complete suite/static analysis passed.

Additional checks: `go test -race ./internal/llm ./internal/conversation ./internal/telegram -count=1 -timeout=120s` passed against the same disposable database. Optional live local LLM verification T13 remains unperformed. T01's live tool-calling check and T07's provider-verification dependency are now verified for the configured model; manual real-Telegram A/B/C acceptance and the full MVP/T12 remain incomplete. Existing 20-record list limit and sequential polling remain; the running application has not been redeployed by this fix.

## Vertical Slice 04 — React Admin Panel — 17.10.2026

Implemented the requested panel without changes to backend code/dependencies, SQL, migrations, Telegram, or LLM behavior. T10 is implemented and T11 is verified in Chromium. This slice stops before the remaining T12 live Telegram acceptance.

- `frontend/`: React/TypeScript/Vite, native fetch, and plain CSS. Russian expense table, combined member/category/inclusive date filters, reset, all backend sort fields/directions, server offset pagination and page size. Loading, empty, network/API errors and retry are distinct; obsolete list/detail responses are canceled/ignored. Deleting the final row on a page corrects the offset.
- Typed API client implements all six requested endpoint operations using the actual handlers/DTOs and documented envelopes. IDs/amounts stay strings; ID upper-bound validation uses BigInt, monetary validation/formatting uses string operations, and dates remain calendar strings. Member choices come from existing expense owners, with manual new-ID entry in forms.
- Create/edit/delete use native modal dialogs. Edit fetches current detail and PATCHes only changed fields, preserving untouched null/empty comments. Clearing text sends explicit null. Client/server field validation retains input on failure; deletion requires confirmation, starts on Cancel, and supports Escape/focus return. Comments render as React text.
- `compose.yaml`, `frontend/Dockerfile`, `frontend/nginx.conf`: nginx serves the production build and proxies `/api` with the original Host including port. Only frontend HTTP is published on loopback; Go and PostgreSQL stay private. The JSON body limit/error contract is delegated to Go. Optional Go HTTP_PROXY/HTTPS_PROXY/NO_PROXY settings are preserved. No host networking, developer proxy address, frontend runtime secrets, authentication, or unrelated features were added. npm uses a Docker build cache; runtime contains no Node or npm dependencies.
- `README.md`, `docs/architecture.md`, `docs/requirements.md`: describe the three-service deployment, development proxy, startup, form semantics and checks. The earlier Go-serves-React assumption P06 is explicitly revised as a project choice. `DESIGN.md` and locally bundled Cantarell/OFL define the restrained journal interface; no external font calls. Ignore rules exclude dependency/build/browser artifacts.

| Mandatory slice verification | Actual result |
|---|---|
| Frontend automated checks | `npm test`: **42 tests passed in 3 files**, with mocked fetch. Covers rendering/HTML text safety, combined filters/reset/date rejection, pagination/sorting/page-size, obsolete responses, exact create, validation, fresh detail/partial edit/comment clearing, unchanged saves, confirmation/cancel/delete/retry/page correction, members/detail/network/JSON errors, loading/empty states, ID/money/date bounds and untouched null/empty comments |
| TypeScript and production build | `npm run build` passed, including `tsc --noEmit`; the same production build/typecheck passed inside the Docker Node stage |
| Go regression/static checks | `GOCACHE=/tmp/finbot-go-cache go test ./... -count=1 -timeout=120s` passed first without TEST_DATABASE_URL (SQL integration skipped), then **unfiltered** with TEST_DATABASE_URL against isolated PostgreSQL 17. `go vet ./...` passed. Includes allowed A/B, denied C, owner isolation, replay, transaction, migrations and real adapter/orchestrator/polling pipeline tests; external APIs are mocked |
| Compose configuration | `docker compose --env-file /dev/null config --quiet` passed with synthetic settings. Parsed config assertions confirmed loopback frontend HTTP, no app/db publication, unchanged optional backend proxy values, no frontend environment secrets and no host networking |
| Full Compose build/start | Passed `docker compose ... -p finbot-panel-check up --build -d --wait` for all three services at `127.0.0.1:18094`. The initial uncached Docker npm download failed with ETIMEDOUT; registry requests from the Docker bridge timed out while host requests succeeded. Host downloads succeeded; seeded the Docker npm cache with the same lockfile dependencies (including Alpine Rollup), then the committed Dockerfile's npm ci/build and subsequent Compose builds passed in ordinary bridge networking. A completely uncached build with unrestricted registry egress was not verified |
| Real REST through nginx | `python3 scripts/smoke.py --url http://127.0.0.1:18094 --state-file /tmp/finbot-panel-smoke.json` passed CRUD, field preservation/reassignment, members, combined filters, numeric sorting, pagination and JSON errors. Restarted **all three** test services; `--verify` passed persistence afterward |
| Real browser integration | Chromium at 1440px and 390px passed real POST with browser Origin, max int64 owner and max exact amount, GET/edit/amount-only PATCH, combined filters/reset, HTML comment as text, Cancel/Escape/focus return, confirmed DELETE, server pages 20+1, sort controls, filter offset reset, last-row deletion/page correction, injected network failure/retry, mobile form validation and contained horizontal table scrolling. No uncaught application script exceptions; intentionally failed fetches produce expected resource console errors |
| Accessibility/render review | Inspected desktop/mobile screenshots. Found and fixed Cancel focus before showModal and mobile overflow from absolute screen-reader table labels. Native dialogs exclude background controls during Tab traversal. Measured text contrasts range from 6.79:1 to 15.78:1 for the sampled text/surface pairs; interactive field borders measure 3.61:1 against white. Visible focus, Russian labels and 44px controls are present |
| Final review | Reviewed the complete diff, new TypeScript components/client/validation/tests, deployment, documentation and dependency lockfile. `git diff --check` passed. No Go source changed, so no new Go functions require gofmt/readability review |

Test settings were synthetic: bot token and allowlist empty, no provider requests or real Telegram sends, separate images/project/volume, and a temporary loopback database port solely for Go integration. The disposable stack, network and data were removed after verification; existing application stacks and `.env` were left unchanged. No commits were created.

Remaining mandatory acceptance: T12 manual real-Telegram scenarios with two allowed users and one denied user were not rerun in this slice. Optional local LLM verification T13 and race checks were not performed. UI limits: member IDs have no display names in the API; native date controls follow browser locale, while application labels are Russian; new-expense date defaults to the browser's calendar; offset pages can shift during concurrent writes. Cross-browser and assistive-technology testing beyond Chromium remains manual.

## Member Display Names — 17.10.2026

Implemented the requested extension after Slice 04. Names are presentation metadata; immutable Telegram IDs continue to control expense ownership, filters, authorization and bot operations. No dependencies, authentication, Telegram profile requests, or bot behavior changes were added.

- Migration `003_member_names.sql` adds only `member_names(user_id PRIMARY KEY, display_name nullable)`. Existing expenses and Telegram results are untouched; there is no users/authentication table or expense foreign key. A parameterized upsert changes only a name. Member lookup unions existing expense owners with metadata IDs and LEFT JOINs names, so unnamed Telegram-created owners remain visible. Explicit metadata remains after an owner's last expense, including when the name is cleared.
- `GET /api/members` now includes nullable `display_name`. `PUT /api/members/{user_id}` creates/replaces only that ID's metadata and returns 200. The service trims Unicode whitespace, allows up to 100 code points, rejects control characters/invalid UTF-8, accepts duplicate names, and converts blank/null to no name. Body user_id changes are rejected. Shared HTTP JSON guards retain existing Origin, media type, UTF-8, size and error conventions; expense DTO field validation is unchanged.
- React adds a collapsible «Участники семьи» section and a small name editor. Existing IDs are static; new metadata may be entered before the first expense. Names/secondary IDs appear in table cells, filter options and expense-form suggestions; option values and all expense requests retain string IDs. Blank names restore numeric fallback. Input validation, API errors, focus handling and mobile wrapping reuse the existing interface. README, REST/architecture/requirements documentation, DESIGN.md and the smoke script reflect this extension.

| Mandatory verification | Actual result |
|---|---|
| Go tests and static checks | `GOCACHE=/tmp/finbot-go-cache go test ./... -count=1 -timeout=120s` and `go vet ./...` passed; full suite also passed with TEST_DATABASE_URL against isolated PostgreSQL 17. Includes existing A/B isolation, C denial, replay and transaction tests; polling integration explicitly checks discovery of unnamed Telegram-created A/B owners and exclusion of denied C |
| New backend coverage | Passed name trimming/Unicode length/clearing/control/ID validation, repository error propagation, PUT and named GET envelopes, unknown fields/immutable ID, JSON/Origin/media/body-limit/method guards. PostgreSQL checks duplicate names, literal SQL-like names, pre-expense metadata, cleared metadata retention, numeric ordering, unnamed discovery and foreign-owner denial. Renaming preserves complete expense objects, owner IDs and timestamps. Upgrade from schema 002 to 003, rollback of 003 and reapplication preserve existing expenses |
| Frontend | `npm test`: **49 tests passed in 4 files**, including 7 new name-display/edit/clear/new-ID/duplicate/error/HTML-text scenarios. `npm run build` passed with TypeScript checking and Vite production output; production Docker build also passed |
| Compose/API/persistence | Isolated `finbot-member-names-check` at `127.0.0.1:18095`: full Compose build/start passed using existing Docker dependency caches. Updated `scripts/smoke.py` passed expense CRUD, name create/update/clear, duplicate names, immutable owner/body-ID rejection, filters and errors through nginx. Restarted all three services; saved expenses and both named/cleared member metadata passed persistence verification |
| Browser | Chromium desktop/mobile (1440/390px): unnamed discovery, immutable existing ID, trim/PUT, primary name and secondary ID, ID-valued filters, unchanged expense owner, reload persistence, clearing/fallback, pre-expense metadata creation, duplicate names and contained mobile layout passed. Screenshots inspected; no uncaught application script errors |
| Final review | gofmt, final diff review of modified/new Go functions, SQL/ownership/transaction/error boundaries, frontend/components and docs, and `git diff --check` passed. No expense/owner SQL or Telegram processing code changed |

Verification used synthetic data, empty bot token/allowlist and mocked model/Telegram endpoints in tests. A post-restart test attempt used the old dynamically assigned database port and failed to connect; rereading the published port and rerunning the unfiltered suite/static analysis passed. The isolated stack/volume was removed after checking; the existing installation and `.env` were preserved and no commit was created. Cross-browser/assistive-technology checks, live Telegram/LLM acceptance and optional local LLM/race checks were not performed for this feature. The earlier outstanding manual MVP acceptance remains separate. This feature deliberately does not delete member metadata or provide identity-based authentication.

## Slice 04.5 — Configuration & Observability Hardening — 17.10.2026

Completed the requested configuration/logging review before final integration acceptance. Scope: backend startup/config, HTTP, Telegram, conversation, LLM, expense service/repository boundaries, React/Vite and Docker/nginx. No dependencies, REST contracts, SQL predicates, isolation levels, migrations, authorization rules, or product features changed; no commit was created.

| Hardcoded value discovered | Decision and operational reason |
|---|---|
| Implicit INFO log threshold | `LOG_LEVEL=INFO`, validated DEBUG/INFO/WARN/ERROR, allows diagnostics without rebuilding |
| REST operation deadline `5s` | `HTTP_REQUEST_TIMEOUT=5s`, for database latency; positive and below the existing `15s` write deadline |
| Startup `30s` and nested readiness `15s` | One `STARTUP_TIMEOUT=30s` budget for readiness plus migrations, so slower disks/startup can be accommodated. The redundant nested readiness cap is removed; readiness may now use the remaining shared budget |
| Telegram HTTP `40s`, long poll `25s` | `TELEGRAM_HTTP_TIMEOUT` and `TELEGRAM_POLL_TIMEOUT`; positive, poll whole seconds 1–50, HTTP timeout greater than poll, for different network/proxy conditions |
| Telegram API backoff `1s`, `2s`; outer polling pause `5s` | `TELEGRAM_RETRY_DELAY=1s` and `TELEGRAM_POLL_RETRY_DELAY=5s`, for recovery pacing; exponential duration overflow is rejected |
| Vite dev API `http://127.0.0.1:8080` | Server-only `FINBOT_DEV_API_URL`, retains the loopback default and Host preservation; another local API port no longer requires a source edit |
| LLM endpoint/model/key/`30s` timeout; HTTP listen/publication; DB connection settings; bot token/allowlist and time zone | Already environment-based; preserved. DSN now parsed at startup without logging parser text. PostgreSQL pool/connect settings use existing pgx DSN parameters instead of duplicate options. Proxy environment support is unchanged |
| Official Telegram API URL and protocol paths; internal nginx/app/PostgreSQL ports 80/8080/5432; Compose database/user names | Kept fixed: standard service wiring, not developer-specific addresses. Host publication is already configurable; alternative infrastructure can use a Compose override |
| HTTP header/read/write/idle deadlines 5/10/15/60s, shutdown 10s and Compose grace 15s; readiness probe 2s/250ms, Compose healthcheck timings | Kept as bounded transport/lifecycle safeguards; the overall startup and REST operation budgets provide the useful deployment controls |
| Three API/transaction attempts, backoff factor two, transaction 5s/rollback 1s, migration lock timing, response/body 1 MiB caps | Kept as bounded implementation/integrity/protocol safeguards; no additional switches justified |
| Categories, monetary/date rules, SQL/sort allowlists, conversation expiry 30m, selection/list/candidate/display limits, system prompt, required single-tool/no-parallel policy | Kept as business rules, security policy or internal details, rather than environment settings. No temperature/provider-selector/fallback switch added to the single compatible adapter |

Backend defaults and environment parsing remain in `internal/config`, which also validates numeric listen ports, DSN syntax and new duration/level constraints. Adapter constructors receive settings explicitly. `.env.example`, Compose and README document the settings and direct-process versus Compose behavior. README now distinguishes the existing Go/Compose model default from the explicit model override in `.env.example`. `.dockerignore` excludes nested `.env*` files.

Structured JSON `slog` events now share `component`/`operation`, fixed error categories, duration and protocol metadata. HTTP emits one completion event with status, a registered route template and a process-local request ID; no incoming correlation header, raw path, query, body, field name or raw driver error is copied. Telegram propagates update IDs to conversation, model and transaction events, records per-attempt duration/API status/category, distinguishes response-body timeouts, and combines outer poll failure/retry logging. Begin events for Telegram processing and LLM requests moved to DEBUG. LLM terminal events include model, latency, validated call count/finish reason and optional nonnegative integer token usage; recognized rejected tool responses retain usage too. Optional malformed/partial accounting metadata does not invalidate otherwise valid completions. Transaction retries log only safe categories/attempt/update metadata; final technical details stay at DEBUG because the transport reports the failed operation. Expense services remain free of transport/logging dependencies and do not duplicate transport-level success/failure events.

Raw external errors, provider bodies, prompts, user text, tool arguments, database details, keys and token-bearing URLs remain excluded, including at DEBUG. Startup errors carry fixed stage/category metadata. Existing early bot authorization, owner-scoped SQL, stored-result replay and transaction semantics are preserved.

| Mandatory verification for this slice | Actual result |
|---|---|
| Formatting and static analysis | `gofmt` applied; no unformatted Go files. `GOCACHE=/tmp/finbot-go-cache go vet ./...` passed from backend; `git diff --check` passed |
| Full backend suite with PostgreSQL | `TEST_DATABASE_URL=<isolated database> GOCACHE=/tmp/finbot-go-cache go test ./... -count=1 -timeout=120s` passed, including migration/CRUD/transaction checks, allowed A/B, denied C, owner isolation, replay, and mocked Telegram/LLM polling pipeline |
| Focused configuration/logging tests | Passed defaults/overrides, required/invalid DSN, ports, durations and overflow, all four log-level filters, safe startup failures, SQL/network/timeout classification, HTTP statuses/routes/request IDs/validation/write failures/deadlines, DEBUG secret sanitization, Telegram API/429/header and body timeouts/cancellation, LLM 429/network/timeouts/tool rejection/correlation and valid/absent/malformed/rejected-response token usage |
| Frontend | `npm test`: 49 tests passed in 4 files. `npm run build` passed, including `tsc --noEmit`. A build with synthetic backend-secret and dev-URL sentinels contained none of them in `dist`. Temporary Vite instance on 127.0.0.1:15173 used `FINBOT_DEV_API_URL` to proxy GET and same-origin PUT successfully; stopped afterward |
| Docker and Compose | `docker compose ... config -q` passed with a synthetic env file. Both production Docker builds passed with `--network host`. Isolated `finbot-045-check` at 127.0.0.1:18096 started cleanly. `scripts/smoke.py` passed CRUD, names/ownership, filters, pagination and errors through nginx, including a repeat against the final rebuilt app. All-service restart and persistence verification passed; existing records also survived final app recreation |
| Runtime logging/privacy | Inspected 96 DEBUG-level HTTP events from the Compose smoke: valid structured attributes, unique process-local request IDs, INFO successes/WARN validation failures, no synthetic comments/member names/SQL/credentials/raw queries. Normal shutdown/restart remained functional |
| Final review | Reviewed the complete tracked diff and new files, including every modified/new Go function, error paths and architecture boundaries. No unauthorized code/dependency/schema changes or commit |

Environment notes: initial Go invocations from the repository root were corrected to the backend module. Sandboxed socket tests could not bind loopback; approved runs outside the sandbox passed. A post-restart integration rerun used a stale dynamically published PostgreSQL port and failed to connect; rereading the port and rerunning the full suite passed. These were verification setup failures, not suppressed test failures. The existing installation and `.env` were preserved; the disposable test stack/volume was removed after checks.

Optional/unexecuted checks remain separate from mandatory slice verification: no live Telegram/provider or local-LLM calls, browser acceptance, race detector, load tests, or real proxy outage tests in this slice. The mandatory manual MVP acceptance remains outstanding where previously recorded; T13 stays optional. Token usage is provider-reported, absent/malformed usage cannot be costed, and no currency pricing calculation or log aggregation is introduced. Request IDs reset with the process; default transport/transaction bounds remain intentional. Existing sequential polling, permanent send failures blocking a pending update, and the absence of provider fallback/outbox remain unchanged.

## Telegram Transaction Diagnostics Follow-up — 18.10.2026

Implemented the three requested improvements to `internal/postgres/telegram.go`: safe transaction stages, focused retry coverage and an explicit callback/result contract. The private error wrapper exposes only a fixed stage in `Error()` and preserves the cause through `Unwrap()`, so `errors.Is/As`, SQLSTATE retry classification and transport error categories continue to work. Retry/final diagnostics now include `stage`: `begin`, `reserve_update`, `lookup_response`, `execute`, `save_response`, or `commit`. No raw SQL, driver message, expense content or response is logged.

The `Execute` comment now states that callbacks can repeat after rollback, must use the supplied repositories/context, rebuild derived in-memory state on each attempt and avoid external/nontransactional side effects. `committed=true` means a newly confirmed commit; replay or foreign-owner collision returns false without an error. False with an error does not establish that an uncertain commit was rolled back; recovery still uses the same update ID. Repeatable-read isolation, owner predicates, atomic expense/result writes, retry policy, rollback cleanup and public signatures are preserved.

Added `telegram_diagnostics_test.go` with SQLSTATE classification (direct/wrapped 40001/40P01; other SQLSTATEs, network, cancellation, deadline and message-only lookalikes are not retried), safe error-message/cause identity tests, deterministic retry recovery/exhaustion/cancellation checks and integration coverage for every transaction stage. Retry faults are injected as wrapped PostgreSQL errors from the callback; real PostgreSQL transactions verify that failed-attempt mutations roll back and only the successful attempt persists. A deferred foreign-key failure exercises the actual commit error path. Replay does not call the callback and is not reported as a new commit. Captured DEBUG/WARN logs assert stage, category, attempt/update ID, level and privacy.

| Mandatory verification | Actual result |
|---|---|
| Go formatting/static analysis | `gofmt` applied to the two Go files; `GOCACHE=/tmp/finbot-go-cache go vet ./...` passed. Final diff and modified/new functions reviewed; `git diff --check` passed |
| Backend including PostgreSQL | Full `TEST_DATABASE_URL=<isolated database> GOCACHE=/tmp/finbot-go-cache go test ./... -count=1 -timeout=120s` passed. Includes new tests, migrations/CRUD/rollback/deadline/concurrent delivery, A/B isolation, C denial and mocked Telegram/LLM pipeline |
| Frontend | Existing `npm test` passed: 49 tests in 4 files. `npm run build` passed, including TypeScript checking. No frontend source changes in this follow-up |
| Compose | Synthetic env plus isolated `finbot-tx-check` project at 127.0.0.1:18097: configuration validation, fresh backend Docker build, clean startup and `scripts/smoke.py` passed. Final app-image recreation and persistence verification also passed. Reused the previously built frontend image; existing installation and `.env` were untouched. Test stack/volume removed after verification |

Changes for this follow-up are limited to the PostgreSQL store, its new focused tests and this record; prior uncommitted work is preserved. No dependencies or schema migrations were added, and no commit was created. Live Telegram/LLM, race/load tests and physical network interruption during commit were not performed; these remain outside this follow-up. The deferred constraint check verifies commit-stage failure, not every ambiguous commit outcome.

## Slice 04.6 — CI, Reliability & Correctness Hardening — 18.10.2026

Implemented this maintenance slice only. GitHub CI is configured but has not run on GitHub; final MVP acceptance and Slice 05 have not started. No application dependencies, schema, expense REST contracts, provider/model defaults, permissions, or frontend product behavior changed.

### Audit and changes

- Added `.github/workflows/ci.yml` for pushes and pull requests to `master`, on Ubuntu with `contents: read`, no persisted checkout credentials, job timeouts and cancellation of superseded runs. `backend` obtains Go from go.mod, caches dependencies/builds, checks formatting/vet, and runs the entire suite with `-race` against disposable PostgreSQL 17. This includes migrations and Telegram pipeline integration, rather than just the repository package. `frontend` uses Node 24/npm/lockfile, cache, existing tests, build/typecheck, and calendar checks in two time zones. There is no existing lint script to invoke. `containers` validates Compose, builds both production images once, starts a fresh stack, and verifies REST/persistence across restart and recreation; cleanup removes only its disposable volume. Telegram is disabled and the LLM points to an unreachable loopback endpoint, with no API key or developer proxy.
- Confirmed an orchestration gap: Compose previously checked only PostgreSQL and could finish `--wait` before the application/nginx were ready. Added an internal `GET /readyz` after migration startup, with a two-second PostgreSQL ping deadline, empty 204 on success and generic 503 on failure. Compose checks application readiness before starting nginx and separately checks nginx's static page. A database outage marks the application unhealthy; this is readiness, not a liveness restart policy. LLM/Telegram are not required dependencies of the panel. The endpoint is not proxied publicly by nginx.
- No new money/date, ownership, transaction, LLM retry, or frontend mutation defect was reproduced. Preserved the existing mechanisms: integer hundredths and decimal strings, NUMERIC totals returned as text (including totals beyond int64), validated inclusive calendar filters, configured message-time timezone, deterministic ID tie breaks and snapshot pagination; parameterized owner/version SQL; bounded rollback and transaction deadlines; atomic Telegram mutation/result commit and replay; allowlist checks before stored responses/LLM, trusted sender identity, and isolated conversation state. The adapter already bounds HTTP calls, sanitizes errors, rejects redirects and validates tools; it does not retry/fallback. Frontend cancellation, error/empty/loading states, confirmation, failed-input retention, and pagination reset already work.
- Added regression coverage in `cmd/finbot/health_test.go` and `main_test.go` for readiness status, safe bodies, deadline/cancellation and graceful draining of an active HTTP request. Extended Telegram tests for a delayed transient-poll retry, cancellation during a long retry wait, and an LLM 429 followed by failed response delivery: the safe committed reply replays without another model call or expense write. Existing A/B allowed, C denied, identity spoofing, per-owner context, stale selections, SQL concurrency, rollback and duplicate-delivery scenarios remain mandatory and passed.
- Added frontend tests for repeated submissions while a POST is pending, disabled controls, retained money/date inputs after a network failure, successful explicit retry, exact fractional/max amount serialization/display, and the browser's calendar near midnight. No UI redesign or state dependency was needed. README documents CI and full integration/race commands; architecture changes are limited to the operational readiness paragraph.

### Verification actually executed locally

| Check | Result |
|---|---|
| Formatting and review | `test -z "$(gofmt -l backend/cmd backend/internal backend/migrations)"` and `git diff --check` passed. Reviewed the complete diff and all changed Go functions for readability, error handling and architectural boundaries |
| Go static checks | `go vet ./...` passed from `backend/` |
| Complete Go suite | `TEST_DATABASE_URL=… go test ./... -count=1 -timeout=180s` and `TEST_DATABASE_URL=… go test -race ./... -count=1 -timeout=180s` passed. Used a dedicated PostgreSQL 17 container on `127.0.0.1:18466`, database `finbot_test`, random schemas and synthetic data. No integration tests skipped; no application database credentials used |
| Frontend | `npm ci --cache /tmp/finbot-046-npm-cache --no-audit --no-fund`, `npm test` (54 tests in 4 files), `npm run typecheck` and `npm run build` passed. `TZ=America/Los_Angeles npm test -- src/validation.test.ts` and `TZ=Pacific/Kiritimati npm test -- src/validation.test.ts` passed (27 cases each) |
| Workflow | `actionlint .github/workflows/ci.yml` passed with actionlint v1.7.12 installed under `/tmp`, without project dependency changes. This is local validation, not a GitHub Actions run |
| Images and Compose | Synthetic env and isolated `finbot-046` project at `127.0.0.1:18467`: `docker compose … config --quiet`, fresh production builds of both Dockerfiles, and `up -d --no-build --wait --wait-timeout 90` passed. Builds used `docker build --network host` locally; CI retains ordinary Compose build networking |
| REST and persistence | `python3 scripts/smoke.py --url http://127.0.0.1:18467 --state-file /tmp/finbot-046-smoke.json` passed. `--verify` passed after full-stack restart and after `down`/`up` with the same isolated volume |
| Readiness and shutdown | Direct container probe returned 204, then 503 while only the disposable database was stopped; app health became unhealthy and recovered after DB restart. SIGTERM through `compose stop app` produced exit code 0, a normal HTTP shutdown event and zero remaining application database sessions. A separate container with an unreachable DB and `STARTUP_TIMEOUT=250ms` exited 1 before HTTP startup, without credentials in logs. No Telegram polling started |

Initial sandbox attempts failed because mock listeners/default Go cache writes and npm registry DNS were restricted; reruns with the required access passed. Local Compose's Bake path rejected host networking without an extra builder entitlement, so both Dockerfiles were built directly using the existing environment's `--network host` approach. These are environment limitations, not silently skipped checks. Only the isolated Slice 04.6 containers/network/volume were removed after verification; the development database and `.env` were untouched. No Git commit was created.

### Remaining verification and limitations

- **CI verification pending:** an actual GitHub workflow run after push/PR. **Mandatory MVP acceptance still pending:** live Telegram/selected-provider scenarios with allowed users A/B and denied C, and final real-browser acceptance. This slice made no paid LLM requests. No frontend lint script exists; none was added. High-load/physical network interruption and exhaustive goroutine-leak testing were not performed.
- Existing documented limits remain: one sequential polling process, a permanently failed Telegram delivery can block the queue, conversation focus is in memory, and processed responses have no retention cleanup/outbox. No speculative retry framework, schema redesign or new product functionality was introduced.
- **Optional T13:** live local LLM verification remains unperformed and does not block MVP acceptance. It is separate from the mandatory checks above.

## Slice 05 — Final QA, README & Submission Readiness — 18.10.2026

Completed the final audit and local QA scope; no additional development phase started. Compared the original R01–R14 requirements with the implementation, REST contract, architecture, configuration, tests, CI and Compose. No missing assignment feature or confirmed critical production defect was reproduced. The detailed A–G report, evidence boundaries and manual scenario are in [submission-readiness.md](submission-readiness.md).

### Changes

- Replaced the root README with a professional English entry point: purpose, factual configuration/version badges, features/stack, real Mermaid architecture, verified quick start, full environment tables, Russian usage examples, REST overview, development/testing instructions, network/security boundary, project map, design decisions and known limits. No passing-CI or license claim is implied. Added screenshot capture guidance without invented images.
- Documented optional proxy settings in `.env.example` without changing values or requiring a gateway/VPN. Clarified the existing template model override versus Go/Compose fallbacks in architecture/README; no provider/model selection changed. Ignored the documented `frontend/.env.local` file.
- Added `internal/telegram/assignment_test.go`: real PostgreSQL, scripted LLM HTTP and Telegram HTTP clients, all eight requested Russian examples, message-time Moscow calendar/yesterday, exact monthly total, full expense snapshot before/after read tools, A/B isolation and denied C. A REST edit is read and then deleted through the bot without changing B's row. This validates adapters and execution, not live model interpretation.
- Added a focused full create-edit-delete workflow in one mounted React journal using existing Vitest/Testing Library/fetch mocks. No testing framework, dependency, production application code or migration was added.

### Verification actually executed

| Check | Result |
|---|---|
| Go formatting and review | `gofmt -w backend/internal/telegram/assignment_test.go`; formatting check, `go vet ./...`, complete diff/new-function review and `git diff --check` passed |
| Backend | `go test ./... -count=1 -timeout=180s`, focused workflow test, and full `go test -race ./... -count=1 -timeout=180s` passed with disposable PostgreSQL; integration tests ran rather than skipping. Final race run verified the README's standalone database setup on loopback 15432. Used `GOCACHE=/tmp/finbot-05-go-cache` |
| Frontend | Offline cached `npm ci` passed with host execution access; `npm test` passed 55 tests in four files, `npm run typecheck` and `npm run build` passed. Calendar/validation tests passed 27 cases each under America/Los_Angeles and Pacific/Kiritimati |
| Browser | Real production frontend/REST in Chromium passed create/edit/delete, invalid amount, member rename, combined filters, sorting, 21-row pagination and last-page deletion recovery, injected network-error/retry, empty state and mobile dialogs. Desktop 1440×1000/mobile 390×844; mobile document width stayed 390px with contained table scroll. No page errors in the normal workflow; intentional network abort produced its expected console error |
| CI configuration | Existing actionlint binary at `/tmp/finbot-046-tools/actionlint` validated `.github/workflows/ci.yml`. Workflow contents and local equivalents checked. No actual GitHub run was verified |
| Docker | Isolated project `finbot-05`, synthetic env, own image tags, HTTP loopback 18505 and disposable volume: Compose config, ordinary Compose image builds, healthy startup, complete `scripts/smoke.py`, persistence after restart and after down/up recreation passed. After cleanup, the exact single-command `up -d --build --wait` path with a fresh QA volume and a new smoke run also passed. Blank bot token/key and unreachable LLM proved the panel works independently; no proxy configured |
| Shutdown and network boundary | QA app SIGTERM exit code 0 and healthy restart verified. Base Compose publishes only frontend on 127.0.0.1; Go/database ports are internal. Test-only database publication used loopback 18506 |
| Docs / Git / privacy | Relative links/internal anchors, README commands, environment parity and Mermaid reviewed; formatting/diff checks passed. No tracked credential-pattern or exact local credential matches; ignored env/build/browser artifacts confirmed. No staged changes/commits; original assignment and development database unchanged |

Initial sandbox Docker socket access and npm esbuild spawn were restricted; authorized host-access reruns passed. Temporary credentials, overrides, caches and logs stayed in `/tmp`. The standalone README test container and isolated QA stack/volume were removed after verification; existing development volumes, `.env` and image tags were preserved.

### Mandatory versus optional status

- T02–T11 implementation and applicable automated checks are verified; current final browser checks also pass. T01's historical configured-provider tool-calling run remains recorded above; it was not repeated here. T12 documentation, startup, restart/persistence, mocked A/B/C and panel acceptance passed.
- **Mandatory T12 remainder:** real Telegram/selected-provider startup and acceptance with allowed A/B and denied C. Not executed; no live Telegram sends or paid LLM calls were made. Therefore full MVP/submission acceptance is still conditional on the manual scenario in the report. No original implementation requirement is known to be missing/broken.
- **GitHub run:** still unverified; local workflow validation/equivalent checks passed. No passing-CI badge or claim was added.
- **Optional T13:** live local LLM check unperformed, not a blocker. Screenshots, other browser engines, assistive technology, real proxy outages and load tests remain unperformed optional checks. Existing sequential polling, in-memory focus, limited bot lists/candidates and saved-reply retention limits are documented.

## README Russian Translation — 18.10.2026

Added a complete Russian translation before the unchanged English README, as requested. Added stable language-navigation anchors; translated prose, tables, diagram labels and directory descriptions while retaining commands, API paths, environment names and configuration values.

Verification: compared the English section with its pre-edit copy (identical), checked that all six shell blocks match across languages, compared section/subsection counts and environment variable names, checked relative Markdown links and anchors including existing links to English headings, and reviewed the translation/diff. `git diff --check` passed. This documentation-only follow-up changes only README and this task record; application code/dependencies are unchanged. Go/frontend/Compose checks were not rerun for the translation; prior Slice 05 results and pending live acceptance remain unchanged.

## Fix — Listing Without Date Filters — 18.10.2026

Confirmed the cause before implementation: the conversation system prompt and list tool description explicitly instructed “no date means today”, and `Orchestrator.filter` independently injected `PeriodToday` for missing dates. The existing optional-date service validation and parameterized PostgreSQL predicates already handled unbounded dates correctly; no SQL/schema change was needed.

- Lists now leave omitted/null dates unset; creation and totals keep their existing today defaults. Independent lists do not inherit dates or offsets. The model receives explicit rules for new requests versus follow-ups and per-owner resolved listing context for “ещё”/filter refinements. An unrelated pending clarification no longer replaces an independent list's calendar reference.
- Added nullable string list arguments `limit`/`offset`, validated server-side (1–20/default 20 and 0–2147483647/default 0). Kept owner predicates, `spent_at DESC, id DESC`, count/item transaction semantics and existing offset pagination. “Последние 5 трат” uses limit 5 without dates. Russian replies show actual dates/category/comment filters, displayed/total counts, positions and continuation guidance. Rendering enforces 4096 UTF-16 units; continuation advances only by displayed rows.
- Regression coverage includes historical unfiltered rows, omitted and null model arguments, today/last seven days/current week/month, latest five, empty results with/without filters, multiple pages and ties, A/B ownership/context separation, repeated independent lists after a filtered query, pending clarification across midnight, invalid pagination and worst-case Unicode replies. Updated scripted Telegram fixtures to match all-date listing semantics.
- Changed production files: `internal/conversation/{orchestrator,tools,execute}.go`; tests: `internal/conversation/{listing,orchestrator}_test.go`, `internal/postgres/owned_test.go`, `internal/telegram/{pipeline,assignment}_test.go`; updated the related date/context rules in requirements and architecture. REST, dependencies, provider/model settings and CRUD are unchanged. Existing working-tree changes were preserved; no commit was made.

Verification actually executed:

| Check | Result |
|---|---|
| Go | `go test ./... -count=1 -timeout=180s` passed; full `go test -race ./... -count=1 -timeout=180s` passed with `TEST_DATABASE_URL` against disposable PostgreSQL 17, including real integration tests and mandatory allowed A/B / denied C. A focused follow-up run passed the strengthened Unicode/null-argument cases |
| Formatting/static review | `gofmt`, `go vet ./...`, formatting check, `git diff --check`, full task diff and changed-function review passed; ownership, optional SQL predicates and transaction boundaries reviewed |
| Frontend | `npm test`: 55 tests in four files passed. `npm run build` passed, including TypeScript checking |
| Compose | Separate `finbot-listing-fix-check` images/volume, loopback HTTP 18615 and test PostgreSQL 18616: `up -d --build --wait` passed with all three services healthy; `scripts/smoke.py` passed CRUD, names/ownership, filters, sorting, pagination and error scenarios. Bot token/key empty and LLM unreachable; development stack preserved |

Initial sandbox Go attempts could not open mock HTTP listeners; reruns with host access passed. Automated LLM/Telegram calls used local test servers only; no paid OpenRouter requests or live Telegram sends occurred. The disposable Compose stack/volume was removed after verification; the existing development services remained running. Live model interpretation/manual Telegram acceptance was not repeated; existing mandatory MVP live acceptance and optional T13 remain separate. Listing context expires after 30 minutes/restart, and existing offset pagination may shift when expenses change between pages.

## Telegram Response Formatting — 18.10.2026

Completed the requested presentation follow-up after inspecting AGENTS.md, the specifications, current formatting, storage/replay flow, and Telegram transport. This is a formatting task, not an additional original-assignment requirement.

- Added the dependency-free `internal/botresponse` presentation package: escaped HTML, bold headings/values, seven category indicators, secondary `<code>#ID</code>` labels, Russian calendar dates including the year, and exact string-based money formatting (`6 000,10 ₽`, integral values without `,00`). ₽ is a display convention matching the request examples; storage/calculations have no currency field or conversion.
- Lists show the actual period/filters, matching count, displayed count and positions, group adjacent dates while preserving query order, and keep existing limit/offset behavior. Whole rendered entries fit within 4096 visible UTF-16 units; continuation advances by displayed entries only. Unbounded comments are explicit ellipsis previews (240 units in lists, 600 in confirmation cards, 120 in candidate selection; filter text 120), with original data unchanged. HTML is escaped after shortening, preserving complete entities and astral characters.
- Creation/update/deletion cards use successful results; updates identify actual changed fields, including an unchanged-value response. Summaries use only the sum already returned by the backend and identify the applied period/filters. No extra queries, invented counts/category breakdowns, SQL/schema changes, REST changes, dependencies, provider changes or calculations were introduced.
- Every Telegram send uses HTML parse mode. New replies carry an internal version prefix in the existing stored response text so replay can distinguish trusted formatter output from old plain-text replies, which are escaped on delivery. Allowlist order, owner scoping, transaction atomicity and idempotent replay remain intact. Handler failures send a safe waiting notice while retaining the original retry/error semantics; Telegram delivery failures retain safe structured logs and existing retries.
- Changed production files: `backend/internal/botresponse/format.go`, `backend/internal/conversation/{execute,orchestrator}.go`, `backend/internal/telegram/{bot,client}.go`. Added `botresponse/format_test.go` and `conversation/formatting_test.go`; updated `conversation/{listing,orchestrator}_test.go`, `postgres/telegram_test.go`, and `telegram/{assignment,bot,client,pipeline}_test.go`. Documentation changes are this record, architecture presentation/replay notes, and the P07 currency-display wording. No commit was created.

Verification actually executed:

| Check | Result |
|---|---|
| Go and PostgreSQL | Final `go test -race ./... -count=1 -timeout=180s` passed with `TEST_DATABASE_URL` pointing to disposable PostgreSQL 17. Includes real transaction/SQL tests, scripted end-to-end CRUD, owner isolation, mandatory allowed A/B and denied C, stale selections, replay and delivery failures |
| Focused coverage | Formatter/conversation/Telegram tests passed. Cases cover lists/cards/summaries, optional comments, Cyrillic/special characters, escaped HTML and literal entities, all categories, large aggregate amounts beyond int64, fractional precision, Russian/leap/year-boundary dates, long lists without skipped rows, 4095/4096/4097 UTF-16 length boundaries, exact preview boundaries, legacy plain-text replay, failed writes without confirmation, and HTML parse mode on all pipeline sends. XML decoding independently validates the emitted tag subset and visible lengths |
| Formatting and static review | `gofmt`, final formatting check, `go vet ./...`, `git diff --check`, complete tracked/new-file review, changed-function readability and architectural/ownership/transaction review passed |
| Frontend | `npm test`: 55 tests in four files passed. `npm run build` passed, including TypeScript checking |
| Compose | Separate `finbot-format-check` images/volume and loopback ports 18625/18626: final `up -d --build --wait` passed with all three services healthy. Final `scripts/smoke.py` passed CRUD, member names/ownership, updates/reassignment, filters, sorting, pagination and errors. Bot/key empty, LLM address unreachable; disposable stack/volume removed after checks; existing development stack preserved |

Initial attempts encountered sandbox build-cache/listener restrictions and a Docker host-network entitlement requirement; authorized Go reruns and standard Docker build networking passed. Earlier test failures were stale expectations for plain-text money/headers and raw rather than rendered HTML length; updated expectations passed in the final full suite.

**Remaining checks/limitations:** no live Telegram sends or paid LLM calls were made; automated external APIs were local scripted test servers. Live Telegram visual inspection was not performed. The previously outstanding manual MVP acceptance remains separate and unchanged; optional local LLM check T13 was not run. Long comments are previews, totals have no operation count/category breakdown from the existing query, and existing offset pagination can shift when expenses change between requests. No future tasks were implemented.

## README Listing Examples — 18.10.2026

Corrected both language sections of README: `мои расходы` lists all dates; `мои расходы за сегодня` explicitly selects today. Updated the adjacent default-period explanation to distinguish unbounded expense lists from creation/totals defaulting to today, matching the documented date rules. Reviewed both sections and the task diff; `git diff --check` passed. This follow-up changes only README and this record. Go/frontend/Compose checks were not rerun for documentation-only edits; previous verification results and outstanding live checks remain unchanged.

## Compact Telegram Responses — 18.10.2026

Completed the requested compact-format follow-up after reviewing AGENTS.md, specifications, the existing presentation layer and tests. Runtime edits are confined to `backend/internal/botresponse/format.go`; business services, orchestration, SQL, transactions, REST, schema, dependencies, provider settings and Telegram transport are untouched by this follow-up.

- Confirmations normally use three logical lines with no blank lines: a bold heading including the ID, comment/category label and bold exact amount, then category/abbreviated Russian date. Blank comments fall back to the category; the footer does not repeat the label's category. Updates still identify only fields that actually changed; unchanged values are identified explicitly.
- Lists use one expense per logical line with inline ID and category emoji. Exactly one blank line separates the header/date groups, with none between expenses of the same date. The header combines the matching count (with Russian plural forms) and period/filters. A single pagination footer appears only when a page represents part of the results. Backend ordering, filters, per-owner scope, candidate numbering and offset increments remain intact.
- Summaries use two logical lines, and selection prompts use one line per candidate. Existing concise validation, clarification and availability errors retain their safe wording; no extra spacing is added. Shared newline/group/inline separators centralize spacing.
- Comments that fit are now shown in full instead of fixed 240/600/120-unit previews; whitespace is normalized into a single logical label line, allowing natural wrapping. Lists defer whole records before shortening. A single record too large to fit receives an ellipsis and an explicit “Текст сокращён; полностью — в панели.” notice, with the original data unchanged. Oversized candidate lists retain every candidate with disclosed previews. Filter text exceeding 512 UTF-16 units is explicitly marked as shortened. Fixed fields and the notice are budgeted before labels, preserving the 4096-unit limit without cutting HTML entities or astral characters. Deterministic rendering and HTML escaping remain in Go; money formatting is unchanged.
- Changed files for this follow-up: `backend/internal/botresponse/{format,format_test}.go`, `backend/internal/conversation/{listing,orchestrator}_test.go`, `backend/internal/postgres/telegram_test.go`, `backend/internal/telegram/{assignment,pipeline}_test.go`, `docs/architecture.md`, and this task record. Earlier working-tree edits, including README, were preserved; no Git commit was created.

Verification actually executed:

| Check | Result |
|---|---|
| Focused Go tests | Formatter/conversation/Telegram tests passed. Exact expected outputs cover three-line CRUD confirmations, fallback labels, compact multi-date lists, empty results, summaries and choices; checks reject extra blank lines and verify escaped Cyrillic/special content, categories, dates, fractional precision and large monetary aggregates |
| Message limits | Tests passed for complete 250/700/3500-character comments, whole-record deferral, oversized Unicode/entity-rich comments and filters, complete candidate numbering, no skipped/duplicated list records, and actual rendered creation/list/selection messages at 4095/4096/4097 UTF-16 units. Independent XML decoding checks HTML structure and visible lengths. Final strengthened count/candidate assertions passed with `go test -race ./internal/botresponse ./internal/conversation -count=1 -timeout=90s` |
| Full Go/PostgreSQL suite | `go test -race ./... -count=1 -timeout=180s` passed against disposable PostgreSQL 17, including mandatory allowed A/B and denied C, ownership/stale-selection checks, scripted bot CRUD, HTML parse mode, duplicate delivery and failure handling |
| Formatting/static/review | `gofmt`, formatting check, `go vet ./...`, `git diff --check`, complete formatter/test/documentation diff review, changed-function style and presentation/business-boundary review passed |
| Frontend | `npm test`: all 55 tests in four files passed. `npm run build` passed, including TypeScript checking |
| Compose | Isolated `finbot-compact-check` images/volume, loopback ports 18635/18636: `up -d --build --wait` passed with all three services healthy. `scripts/smoke.py` passed CRUD, member names/ownership, updates/reassignment, filters, sorting, pagination and errors. Bot token/key empty and LLM unreachable; test stack/volume removed after checks; original development containers preserved |

**Limitations and remaining checks:** logical lines may wrap on narrow Telegram clients. Only text too large for a message is explicitly shortened; full expense text remains available in the panel. Previously stored replies replay their original formatting to preserve idempotency. No live Telegram visual check or paid LLM request was performed; automated LLM/Telegram interactions used local mocks. Existing manual MVP acceptance and optional local-model check T13 remain separate and unchanged. No subsequent stages were implemented.

## Mandatory MVP

Maintenance check, 16.10.2026: centralized all expense model/service errors in a single `var` block in `model.go`, including `ErrNotFound` and validation messages. Existing messages and REST behavior are preserved. `gofmt`, `go test ./...`, and `go vet ./...` passed; PostgreSQL integration tests were skipped without `TEST_DATABASE_URL`. Compose smoke was not rerun for this declaration-only refactoring.

Comment follow-up, 16.10.2026: replaced category literals in `ParseCategory` with seven typed `Category` constants, resolving the `Dehardcode categories` comment. Accepted values and API behavior are unchanged. `gofmt`, `go test ./...`, and `go vet ./...` passed; PostgreSQL integration tests were skipped without `TEST_DATABASE_URL`, and Compose smoke was not rerun for this constants-only change.

Entrypoint readability, 16.10.2026: separated database initialization, HTTP serving, and graceful shutdown in `cmd/finbot/main.go`; `run` now reads as startup orchestration with explicit repository/service wiring. Signal handling, timeout values, startup order, cleanup, and public error messages are preserved. `gofmt`, `go test ./...`, and `go vet ./...` passed (repository integration tests skipped without `TEST_DATABASE_URL`). Docker build with `--network host`, Compose HTTP smoke, simultaneous app/database restart, and HTTP persistence verification passed; operational logs confirmed normal shutdown and restart. Frontend and Telegram checks remain outside the implemented scope.

All tasks T01–T12 are mandatory. Independent branches can proceed in any order once their stated dependencies are ready.

| ID | Stage | Dependencies | Completion criterion |
|---|---|---|---|
| T01 | Finalize the REST contract and verify one configured LLM endpoint | Documents | Contract examples have been checked; a direct test request confirms tool calling for the selected model. The production adapter is not yet required for this run |
| T02 | Implement amount, category, and date rules | Contract | Monetary precision, calendar periods, and validation have been checked |
| T03 | Add migrations and PostgreSQL operations | T02 | CRUD, filters, totals, selection of the user's most recent record, and owner restrictions have been checked against PostgreSQL |
| T04 | Prepare configuration, build, and Compose | Contract | The application and database start; migrations and the persistent volume work; allowlist and LLM setting formats are defined |
| T05 | Implement the REST API | T03 | JSON, required and nullable fields, PATCH, errors, filters, sorting, and pagination match [api.md](api.md) |
| T06 | Connect Telegram, the allowlist, and duplicate-delivery protection | T03 | Private messages are received; a denied ID does not reach the LLM, stored responses, or expense operations; a repeated `update_id` does not repeat a change |
| T07 | Implement a configurable OpenAI-compatible adapter | T01, T02 | Tool arguments, settings, the optional key, timeouts, malformed responses, and the absence of automatic fallback to another endpoint have been checked |
| T08 | Implement conversational CRUD | T06, T07 | Assignment examples, focus-based selection, selection of the user's most recently created expense without a focus, previous-amount validation, and clarification in text pass |
| T09 | Verify access and security | T05, T08 | Checks for allowed A/B, denied C, other users' IDs, independent contexts, and stale records pass |
| T10 | Implement the React panel | REST contract; T05 for integration | The list, filters, sorting, pagination, and CRUD work |
| T11 | Verify the panel in a browser | T10 | Main actions, empty states, API errors, and safe text rendering have been checked |
| T12 | Perform end-to-end acceptance and prepare startup instructions | T01–T11 | Clean Compose startup, restarts, scenarios for two allowed users, and denial of a disallowed user are reproducible; one configured LLM endpoint works; limitations are documented |

Frontend work can start against [api.md](api.md) using test responses; the delivered application must be verified against the real REST API.

## Critical Path

- Data preparation: T02 → T03; LLM verification: T01 → T07.
- These branches converge with Telegram transport T06 at T08, then pass through mandatory security verification T09 and end-to-end acceptance T12.
- REST and the panel form a parallel mandatory branch: T03 → T05 → T10 → T11 → T12.
- Configuration and Compose T04 are needed for the final startup. Additional task T13 is not on the critical path and is not a dependency of T12.

## Additional Check

| ID | Check | Condition | Result |
|---|---|---|---|
| T13 | A live local LLM run through the same adapter | After the MVP is ready, when a server and suitable model are available | The server, model, Docker reachability, tool support, and CRUD results are recorded. An omitted or unsuccessful check does not block the MVP |

For the local run, verify an empty key, a correct address from inside the container, timeout behavior, and the absence of requests to a cloud provider. The model server is provided separately; model installation and GPU management are not project tasks.

## Mandatory Test Scenarios

| Group | Check and expected result | Requirement references |
|---|---|---|
| Model | All fields and categories, optional comment; system fields are protected from client writes | R01, P11 |
| Money | `0.10 + 0.20 = 0.30`; zero, negative amounts, out-of-range values, and excess decimal places are rejected without rounding | R01–R04, R09, P07 |
| Dates | Moscow midnight, month and year boundaries, leap day, the last seven days; filter boundaries are inclusive, and a reversed interval is rejected | R02, R03, R08, P08 |
| Bot | All assignment examples; changes to category, date, and comment; empty results; missing information prompts clarification | R02–R05, R07, P09 |
| Allowlist | A/B are allowed, C is denied; empty configuration denies everyone while the panel works; a malformed non-empty list is a startup error; whitespace and duplicates follow the contract | P02 |
| Authorization order | C does not call the LLM, read a stored result, or perform expense operations. Removing an ID from the list and restarting also blocks a repeat of a previously processed message | P02, P12 |
| A/B isolation | Another user's known ID cannot be read, updated, or deleted; A's lists, totals, last record, and candidates do not disclose B; no other user's rows are changed | R06, P02 |
| Permission spoofing | An ID in text, a claim of administrative rights, forwarded text, and forged LLM arguments do not change the owner or permission | R06, P02, P12 |
| Last expense | A current focus takes precedence; without a focus, select the user's own record by `created_at DESC, id DESC`, regardless of the purchase date; equal `created_at`, no expenses, and recovery after context loss are checked | R04, P04, P09 |
| Previous amount | A match permits the change; a mismatch prompts clarification without changing the record, with or without a focus | R04, P04 |
| Clarifications | A/B contexts are independent; multiple taxi expenses prompt a question without deletion; «вторая» selects only a proposed candidate belonging to the user | R04–R06, P09 |
| Stale record | A change, deletion, or reassignment through the panel does not execute an old deferred action; another expense is not silently selected | R04–R06, R09, P04, P11 |
| JSON and REST CRUD | IDs and amounts are strings; required fields, `null`, an empty comment, omitted PATCH fields, and clearing a comment follow the contract; system and unknown fields are rejected | R09, R10, P05, P11 |
| REST errors | Statuses and codes from [api.md](api.md), the common error object, absence of internal details, `Allow` for an unsupported method, and bodyless `204` for deletion have been checked | R10, P05 |
| Filters and pages | Each filter and AND combinations, inclusive boundaries, defaults and bounds for `limit`/`offset`, exact `total`, empty results and pages; unknown, empty, and repeated parameters are rejected | R08, R10, P05 |
| Sorting | Allowed fields and directions; numeric ordering of IDs and amounts; stable ordering of equal values by `id`; an unknown field is rejected | R10, P05 |
| Members and owners | The lookup list contains unique owners of existing expenses and is not the allowlist; the panel creates a record for a new ID and changes the owner; without permission, that owner is denied bot access | R06, R09, R14, P02, P11 |
| Duplicate delivery | One `update_id` changes data once, including a repeat after commit and restart; identical text with different IDs creates separate expenses | R02, R04, R05, P12 |
| LLM adapter | One configured endpoint passes a live run; automated checks cover configuration, an empty key, an incorrect model, missing tool calling, malformed JSON, an unknown operation, and timeout | R13, P03 |
| Failures | A database error rolls back the change and result; an LLM error before execution produces no write; a response delivery error does not repeat a change; another endpoint is not called automatically | P03, P12 |
| Injections | SQL-like text remains data; HTML and scripts are rendered as text; instructions in comments grant no additional permissions | R06, R10, P12 |
| Browser | CRUD, filters, sorting, and pagination work; empty results and network errors are understandable; failed saves are not shown as successful | R08–R10, P05 |
| Startup | Clean Compose startup after configuration, repeated migrations, expense persistence after restart; the panel works with an empty allowlist and an unavailable LLM | R11–R14, P01–P03, P06, P12 |

Automated scenarios use LLM and Telegram test doubles; SQL and transactions are verified against PostgreSQL. The mandatory manual run uses one configured LLM endpoint, two allowed Telegram users, and one denied user. A separate live local run is recorded only under T13.

## MVP Readiness Criteria

These are acceptance criteria, not a statement that every check is complete. The current verdict and remaining live acceptance are in [submission-readiness.md](submission-readiness.md).

- R01–R14 and mandatory project rules have been verified; results for T01–T12 are recorded in this document.
- Assignment examples, the allowlist, expense ownership isolation, and last-expense scenarios pass.
- REST and the panel conform to [api.md](api.md).
- Go checks, frontend type checking, and the frontend build have run; the Compose smoke test passes.
- Startup instructions are reproducible and describe allowlist and selected LLM endpoint configuration, network boundaries, and context loss.
- T13 status is recorded separately: not performed, passed, or failed. Any of these statuses is acceptable for MVP delivery.
- Limitations and remaining risks are documented. Coverage percentage alone is not an acceptance criterion.
