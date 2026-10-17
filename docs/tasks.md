# FinBot Implementation Stages

Basis: [assignment requirements and project decisions](requirements.md), [architecture](architecture.md), and [REST contract](api.md). The current increment implements complete administrative expense CRUD and member lookup through a Go REST API with PostgreSQL; the full MVP is not yet implemented.

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

## MVP Readiness

- R01–R14 and mandatory project rules have been verified; results for T01–T12 are recorded in this document.
- Assignment examples, the allowlist, expense ownership isolation, and last-expense scenarios pass.
- REST and the panel conform to [api.md](api.md).
- Go checks, frontend type checking, and the frontend build have run; the Compose smoke test passes.
- Startup instructions are reproducible and describe allowlist and selected LLM endpoint configuration, network boundaries, and context loss.
- T13 status is recorded separately: not performed, passed, or failed. Any of these statuses is acceptable for MVP delivery.
- Limitations and remaining risks are documented. Coverage percentage alone is not an acceptance criterion.
