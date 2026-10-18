# Slice 05 — Historical QA and Current Acceptance Guide

**Current status — 2026-10-19:** the owner reports successful live Telegram acceptance for natural-language CRUD, deletion confirmation, repeated cancellation, amount/description extraction, unsupported currency rejection, recovery and user isolation. This was reported by the owner, not repeated by this automated polish. F1–F4 passed the subsequent regression audit; see the [final polish record](tasks.md#final-submission-polish--2026-10-19) for current checks and repository status. The original Slice 05 evidence below is historical; sections E–G contain updated limitations and acceptance instructions.

Historical audit date: **2026-10-18**. Scope: final requirements review, regression testing, fresh setup, README, and security review. The [original assignment](test-task-expenses.md) and implementation are the source of truth; [project choices](requirements.md) are tracked separately.

**Historical Slice 05 verdict: implementation and local automated/browser QA were ready; live Telegram A/B/C acceptance was still pending at that audit.** No missing assignment feature or confirmed critical application defect was found. Current provider interpretation and real-account delivery were not exercised in this slice. No live Telegram messages or paid LLM requests were made.

## A. Requirements Coverage

“Implemented and verified” identifies the evidence shown, not exhaustive production assurance. Scripted model responses verify transport/orchestration/database behavior; they cannot prove natural-language interpretation by the currently configured provider.

| ID | Original requirement | Status | Evidence / verification boundary |
|---|---|---|---|
| R01 | Expense model and seven categories | Implemented and verified | Domain/HTTP/SQL tests; migrations; persisted fields and exact amounts |
| R02 | Natural-language creation, including coffee and yesterday's taxi | Implemented but not fully verified | Mock HTTP model/Telegram workflow with real PostgreSQL passed; current live provider/user flow pending |
| R03 | List expenses and calculate period/category totals | Implemented but not fully verified | Owner-scoped lists, month totals, week/date boundaries passed with scripted tools; live interpretation pending |
| R04 | Natural-language expense editing | Implemented but not fully verified | Previous amount, stale focus, ambiguity, owner/version guards and web-to-bot edit passed; live conversation pending |
| R05 | Natural-language expense deletion | Implemented but not fully verified | Focused deletion and ambiguous taxi selection passed in automated tests; live conversation pending |
| R06 | No access to another user's expenses in the bot | Implemented and verified | A/B reads, totals, selections and mutations; SQL ownership; spoofed IDs; denied C before model/replay/operations. Real-account acceptance remains a separate pending check |
| R07 | Plain text without buttons or commands | Implemented but not fully verified | Private text transport and all requested example messages run through scripted model responses; live model understanding pending |
| R08 | Family list with member/period/category filters | Implemented and verified | Real Chromium with combined filters; repository/REST tests and smoke |
| R09 | Web CRUD over all family expenses | Implemented and verified | Real-browser create/edit/delete, exact amount validation, API owner reassignment and partial updates |
| R10 | REST communication between panel and backend | Implemented and verified | Same-origin nginx proxy, real browser requests, API contract tests and smoke |
| R11 | Go backend | Implemented and verified | Go build, vet, full tests and race detector |
| R12 | One Compose invocation starts application dependencies | Implemented and verified | Both images built; isolated three-service startup/readiness; migrations; restart and recreation persistence |
| R13 | Configurable LLM access | Implemented and verified | Environment validation and actual OpenAI-compatible adapter with mock HTTP responses/timeouts/errors. Current live provider availability was not checked |
| R14 | Panel needs no authentication | Implemented and verified | Browser and REST work without credentials; default publication is loopback only |

No original requirement was classified as missing or broken. The unfinished items above are live verification gaps. The historical selected-provider check on 2026-10-17 remains recorded in [tasks.md](tasks.md); it is not claimed as a new Slice 05 run or as complete real-Telegram acceptance.

### Additional requested QA

| Area | Result |
|---|---|
| Backend correctness | Full PostgreSQL suite passed: exact hundredths and large aggregates, inclusive filters, deterministic sorts/pages, calendar/timezone boundaries, validation, concurrent changes, rollback/deadlines, migrations, member-name updates and ownership |
| Bot reliability | Existing mock pipeline passed: polling recovery, duplicate delivery, empty allowlist, allowed A/B and denied C, malformed/unsupported tools, zero/multiple calls, model errors/429/timeouts, and failed delivery after commit |
| Read-only safety | New workflow compares every expense field before/after list and total tools; no expense mutation occurs |
| Cross-transport consistency | New integration test creates via Telegram, edits via REST, reads the new value through Telegram, then deletes only the focused owner's expense |
| Frontend | 55 existing/new Vitest tests; production typecheck/build; real Chromium CRUD, names, combined filters, sorting, next page, last-page deletion recovery, network error/retry, empty state, desktop/mobile |
| Infrastructure | Fresh isolated storage, no live bot token/key, unreachable LLM, direct networking without proxy; successful healthchecks, persistence after restart/recreation, clean SIGTERM exit and recovery |
| Privacy/security | Parameterized data SQL/fixed sort expressions; SQL owner/version predicates; allowlist before model/replay; safe logs/errors; bounded contexts/rollback and shutdown. Tracked-file credential checks passed |

## B. Changes Made

| File | Change |
|---|---|
| [README.md](../README.md) | Replaced stale Russian slice notes with an English reviewer entry point, verified setup/testing instructions, Mermaid architecture, complete configuration, API overview and honest limits |
| [.env.example](../.env.example) | Documented optional outbound proxy variables without selecting a gateway/VPN or changing defaults |
| [.gitignore](../.gitignore) | Ignored the documented `frontend/.env.local` development configuration |
| [architecture.md](architecture.md) | Clarified unchanged Go/Compose fallback versus the explicit template model and historical/live verification distinction |
| [assignment_test.go](../backend/internal/telegram/assignment_test.go) | Focused real-PostgreSQL workflow through mock LLM/Telegram HTTP adapters plus REST edit; exact totals, message-time calendar, unchanged reads and A/B/C checks |
| [App.test.tsx](../frontend/src/App.test.tsx) | Full create-edit-delete regression in one mounted journal using existing test tools |
| [screenshots/README.md](screenshots/README.md) | Real screenshot capture convention; no fake images or broken image links |
| [tasks.md](tasks.md), this report | Recorded final audit, actual results, acceptance scenario and remaining mandatory/optional checks |

No production application code, dependencies, schema, architecture, provider/model, or authentication behavior changed. No Git commit was created. No speculative refactoring or additional development phase was started.

## C. Test Results

All commands below were executed locally. Temporary environment/Compose files and test logs live under `/tmp`, outside the repository. The test credentials shown in the README are synthetic.

### Backend

Commands from `backend/` used `GOCACHE=/tmp/finbot-05-go-cache`:

```sh
test -z "$(gofmt -l cmd internal migrations)"
go vet ./...
TEST_DATABASE_URL='<disposable PostgreSQL DSN>' go test ./... -count=1 -timeout=180s
TEST_DATABASE_URL='<disposable PostgreSQL DSN>' go test ./internal/telegram -run TestAssignmentWorkflowIntegration -count=1 -v
TEST_DATABASE_URL='<disposable PostgreSQL DSN>' go test -race ./... -count=1 -timeout=180s
```

**Passed.** `gofmt -w backend/internal/telegram/assignment_test.go` was applied from the root. The first full suite used the Compose QA PostgreSQL at loopback port 18506. Final full race verification used the README's separately created disposable `finbot-test-db` on port 15432, confirming that exact setup recipe. All database-dependent packages ran; the integration suites did not skip. The modified/new Go function was reviewed after formatting for explicit control flow, safe error handling, owner constraints and architectural boundaries.

### Frontend and workflow

Commands from `frontend/`:

```sh
npm ci --cache /tmp/finbot-046-npm-cache --offline --no-audit --no-fund
npm test
npm run typecheck
npm run build
TZ=America/Los_Angeles npm test -- src/validation.test.ts
TZ=Pacific/Kiritimati npm test -- src/validation.test.ts
```

**Passed:** 55 tests in four files; 27 calendar/validation cases in each additional timezone run; TypeScript and production build. The reused `/tmp` npm cache is only a local installation optimization. Normal `npm ci` also completed in the production frontend Docker build. No lint script exists, so no frontend lint command is claimed.

`/tmp/finbot-046-tools/actionlint .github/workflows/ci.yml` **passed** using the previously installed actionlint binary. The workflow was inspected for permissions, jobs, disposable databases/volumes, and absence of production credentials. **No completed GitHub Actions run was verified.**

### Docker and HTTP

The QA stack used project `finbot-05`, synthetic `/tmp/finbot-05.env`, image tags `finbot-05-app:local` / `finbot-05-frontend:local`, HTTP on `127.0.0.1:18505`, and its own `finbot-05_postgres_data` volume. A temporary override published the QA database on loopback port 18506 solely for integration tests. The ordinary Compose file publishes no app/database ports.

From the repository root, the exact Compose prefix was:

```sh
docker compose --env-file /tmp/finbot-05.env -f compose.yaml -f /tmp/finbot-05.compose.yaml
```

The following subcommands **passed** with that prefix:

- `config --quiet`
- `build` (both production Dockerfiles using ordinary Compose networking)
- `up -d --wait --wait-timeout 90 db`, followed by full `up -d --no-build --wait --wait-timeout 90`
- After cleanup, exact README `up -d --build --wait` from an absent QA network/volume; all services became healthy and a new REST smoke run passed (`/tmp/finbot-05-fresh-smoke.json`)
- `restart`, followed by readiness and persistence verification
- `down` without volume removal, then `up -d --no-build --wait --wait-timeout 90` and persistence verification
- `stop app`; `docker inspect --format '{{.State.ExitCode}}' finbot-05-app-1` returned **0**; restarting the app restored healthy state

```sh
python3 scripts/smoke.py --url http://127.0.0.1:18505 --state-file /tmp/finbot-05-smoke.json
python3 scripts/smoke.py --url http://127.0.0.1:18505 --state-file /tmp/finbot-05-smoke.json --verify
```

**Passed:** CRUD, member names, immutable identity, partial updates, reassignment, filters, sorting, pagination, errors, and persistence after both restart and recreation. `docker compose config --quiet` also passed against the existing local configuration without printing its values. The base Compose publication was inspected separately: frontend only, `127.0.0.1`; app/database private. Existing `.env`, images tagged for development, and development database/volumes were preserved. After verification, the README test container and only the `finbot-05` containers/network/volume were removed.

### Real browser

Playwright-controlled Chromium exercised the production frontend against the real disposable backend, without mocking its CRUD responses:

- Invalid fractional amount rejected, then create `350.10`, edit to `450.20`, rename the owner, combine member/category/date filters, sort, cancel deletion, confirm deletion, verify empty result and database absence through REST.
- Desktop 1440×1000 and mobile 390×844. Mobile document width was exactly 390px; the 827px table scrolled inside its container. No page JavaScript errors occurred during this workflow.
- Seeded 21 synthetic rows for another test owner; navigated to page two, checked amount order, deleted its only row, and verified recovery to the preceding page with 20 rows.
- Intentionally aborted a list request, observed the visible error, removed the fault and retried successfully. The expected network console error belongs to this injected failure.

These browser runs are local QA evidence, not a new CI browser framework. The added Vitest workflow regression is included in the existing CI test command.

### Documentation, Git and security

- Reviewed README commands against source/config, and executed the standalone database recipe, Compose startup/configuration, Go/frontend commands and smoke commands. Clone URL matches the configured Git remote; a second clone/remote access was not attempted.
- Relative Markdown paths and internal README/report anchors checked; Mermaid syntax and component/data-flow boundaries reviewed. README/environment table compared against config, Compose and `.env.example`.
- `git diff --check`, formatting verification and complete tracked/untracked change review passed. `git status --short` inspected; no staged changes or commits were made.
- Scanned tracked files for token/private-key patterns and exact local credential values without displaying those values: no matches. `.env`, `frontend/.env.local`, build/dependency directories and browser artifacts are ignored. This is a working-tree check, not a forensic audit of all remote Git history.

### Failed or unavailable checks

The initial sandboxed Docker call could not access the Docker socket; the initial offline npm installation was blocked from spawning esbuild (`EPERM`). Reruns with the required host access passed. No final check remained failed because of these restrictions.

Not executed: live Telegram startup/delivery with valid credentials; current live provider calls; real-account A/B/C acceptance; an actual GitHub workflow run; optional local LLM verification; live proxy/VPN outage tests; cross-browser/assistive-technology and high-load testing. No missing external verification is represented as a pass.

## D. README Improvements

The README now contains the project purpose, factual Go/configured-CI badges, features, actual stack, Mermaid diagram, minimal Docker setup, complete environment settings, Russian command examples, concise endpoint table, developer/test commands, security boundary, directory map, design rationale, screenshot guidance and known limits. Relative documentation links point to real files; no image or license badge claims absent artifacts.

Usage examples correspond to tool schemas, calendar/focus rules, tests and the original assignment. Their server-side behavior was verified using scripted tools; live interpretation remains qualified explicitly. The existing OpenRouter/model configuration was not changed. Instructions explain that `up -d` reloads changed Compose environment while `restart` does not.

## E. Remaining Limitations

1. **Live acceptance:** the owner reports successful live Telegram testing as recorded above. The repeatable A/B/C checklist below remains available; no new live run was performed during final polish.
2. **Remote CI:** [CI succeeded](https://github.com/Venamuss/FinBot/actions/runs/39154509084) for HEAD `d18e5a735923f22335ab495ed2a7d9e6dbefefca`. These final uncommitted changes are checked locally; verify a new run after the developer commits and pushes.
3. The admin interface is intentionally unauthenticated and must stay on loopback/a trusted network. It is not suitable for unrestricted public exposure.
4. One sequential polling process. Denial delivery is best-effort; recipient-specific permanent 400/403 delivery failures after a saved result do not indefinitely block later updates. Temporary failures need retries; database, configuration and unclassified failures can still pause the queue. Transactional idempotency prevents duplicate committed mutations, but a network-ambiguous send can duplicate a reply. There is no durable delivery outbox or guarantee that every reply reaches its recipient.
5. Conversation focus expires/lives in memory; lists are limited to 20 bot rows, ambiguity candidates to 10, and comment search is a literal substring. Saved replies have no retention cleanup/outbox; deleting an expense does not erase saved replies.
6. Single family, private text messages, one expense write per request, no currency conversion, and offset pages can shift under concurrent changes.
7. Optional live local LLM verification (T13), screenshots, other browser engines, real proxy outages and load testing were not performed. They remain optional checks.

## F. Manual Acceptance Test

Use a **separate test checkout/Compose project and disposable volume**, synthetic expenses, two actual permitted accounts A/B, and a third account C absent from the allowlist. In that checkout's `.env`, set `COMPOSE_PROJECT_NAME=finbot-acceptance` and `HTTP_PORT=18085` before running any Compose command; retain that project name for restart and cleanup. The test panel URL is `http://127.0.0.1:18085`. Supply valid bot/provider credentials locally; do not paste them into reports or command output. Use the configured provider/model unchanged. These manual messages can incur provider charges.

1. Follow the [quick start](../README.md#quick-start), set A/B in `TELEGRAM_ALLOWED_USER_IDS`, and launch `docker compose up -d --build --wait`. Confirm three healthy services, the panel and `GET /api/expenses`. On an existing test configuration, use `up -d` after environment changes.
2. A sends `кофе 350`; B sends `такси 1200 вчера`. Confirm committed replies, their IDs, categories and dates. In React, see both records and verify member/category/date filters. Assign optional synthetic member names.
3. In React, edit A's coffee to `450.00`; leave the owner unchanged. A sends `покажи кофе за сегодня` to reselect the now-current record; expect `450.00`. Reading it should not change timestamps or amounts. A stale edit attempted before reselection must ask for clarification.
4. Run `docker compose restart`, wait for health with `docker compose up -d --wait`, and confirm both expenses and member names remain in React. A/B each send `покажи траты за неделю` and confirm only their own record appears. A's monthly food total must exclude B's taxi.
5. A sends `удали последнюю трату`; in this isolated scenario A owns only the coffee, so it is the last-created record after restart. The bot identifies it and asks for confirmation. Verify the expense remains in React until A replies `да`; only then should it disappear from React and A's list. B's taxi must remain. In separate attempts, `стоп`, `отмена` or a negative confirmation (`нет`) must cancel the pending deletion without deleting the expense; repeated cancellation and a later idle `да` must be harmless.
6. A asks to edit/delete B's known expense ID; expect no disclosure/change. C sends `кофе 350` and `мои расходы`; expect only generic access denial, with no LLM call or data change. Also verify an empty allowlist denies everyone after applying configuration while the panel continues working.
7. For ambiguity, A creates two taxi expenses, then sends `удали такси`. Confirm neither is deleted before selecting an offered number; selection must then open a separate confirmation, with deletion only after `да`. Send a mismatched previous-amount edit and confirm clarification is required.

Record date, configured model identifier, passed/failed steps, observed IDs/amounts from **synthetic data only**, and whether A/B/C used real accounts. Do not record keys, tokens, or real expense contents. Stop the test stack normally; remove its disposable volume only after confirming the Compose project name. Never reset the development database.

## G. Final Verdict

**No confirmed F1–F4 application defect remains after the regression audit.** The owner reports successful live acceptance, and the final polish adds a specific server-selected unsupported-currency response with automated pipeline regressions. Current local verification and any unavailable checks are recorded in the [final polish record](tasks.md#final-submission-polish--2026-10-19). Remote CI succeeded for the current HEAD as linked in section E; the final uncommitted changes require their own CI run after push. Local results do not establish that future remote result. Optional T13 is not a blocker.

The older [code review](FINAL_CODE_REVIEW.md) and [submission audit](SUBMISSION_READINESS.md) describe their stated revisions and evidence dates. Their original findings/verdicts are historical, not current unresolved defects. Before the final commit, review the intended files, including the existing untracked reports, and include any documents referenced by committed links. No automatic staging, commit or push is part of this polish.
