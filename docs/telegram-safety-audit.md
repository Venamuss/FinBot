# Telegram reliability and safety audit — 18 October 2026

Scope: the current Telegram agent, without provider/model changes, schema changes,
new infrastructure, or changes to the panel/REST contract. Automated LLM responses
are scripted; they cannot establish live natural-language interpretation quality.

## Findings recorded before implementation

1. `conversation.applyModification` immediately deletes a unique match; selecting
   an ambiguous delete also immediately deletes it. There is no deletion-specific
   yes/no confirmation. Existing `confirmation` means previous-amount mismatch.
2. `execute` trusts the selected mutation tool even when the message explicitly
   asks to list expenses. Owner/version checks protect the target but do not
   establish that the user requested a write.
   A separate regression also reproduced a read request seeding a deferred write
   through `clarify(intent=update_expense)`; validation covers that path too.
3. `choose` validates the model's choice and state, but not the user's reply.
   A model-selected choice can execute a deferred update for unrelated text.
4. `creationComment` accepts an omitted `amount_text` and a clean comment without
   verifying that the supplied monetary value appears in the source. This lets
   a model invent an amount for an incomplete creation.
5. State expiry is based on last activity, including rejected replies. A pending
   destructive decision has no independent deadline.
6. Additional regressions written during final review failed before their fixes:
   a model could replace an explicit `#ID` with another owned ID; a complete
   update without the optional `request_context=new` flag stayed trapped in a
   deletion confirmation; a bare `удали` implicitly selected the focus.
7. Both optional and supplied source-price fragments accepted a model-selected
   value from explicit alternatives (`кофе 200 или 300`). A hostile-output
   regression reproduced this before adding an alternative-price guard.
8. The previous-amount comparison depended on the model supplying the right
   `previous_amount`. Omitting it bypassed mismatch clarification; swapping old
   and new values could update the wrong amount. Both failures were reproduced
   before binding explicit `не X, а Y` corrections to their source values.
9. A model could choose a quantity instead of an explicitly marked price in
   `купил 2 кофе за 500`, with or without `amount_text`. Both paths reproduced
   before checking proposed amounts against source expressions marked with `за`.

Existing protections inspected: cancellation is already intercepted before the
LLM, including repeated idle cancellation; owner identity comes exclusively from
the private Telegram sender; empty allowlist denies access before replay/LLM/SQL;
owner and `updated_at` are checked in writes; transaction retries rebuild state;
expense changes and stored replies commit atomically; response delivery retries
reuse stored results. Pagination already stores actual previous page offsets.
Currency defaults to RUB and explicit foreign currencies are checked in Go.
Description separation already preserves model numbers and quantities when a
valid source-price expression is supplied. These are retained, not reimplemented.

## Update lifecycle and responsibilities

| Step | Responsible component |
|---|---|
| Poll and validate private text update | `telegram.Client`, `telegram.Bot.Process/Run` |
| Establish sender and allowlist | `Bot.Process`, repeated in `Orchestrator.Handle` before any storage read |
| Replay and load/expire owner state | `Orchestrator.Handle`, `postgres.TelegramStore.Lookup` |
| Interpret general language | Configured `llm.Client`; exactly one proposed tool |
| Validate protocol/arguments and state | `llm.response`, `conversation.tools`, `conversation.execute/state` |
| Resolve own target, ambiguity, previous amount | `selectExpenses`, `modify`, `choose`, `expense.OwnerService` |
| Write with ownership/version guard | `postgres.Repository` owner methods |
| Commit write and response, retry serialization/deadlock | `TelegramStore.Execute` (REPEATABLE READ) |
| Publish conversation state | `Orchestrator.Handle` only after a new successful commit |
| Deliver/retry response and advance polling offset | `Bot.Process/Run`, `Client.Send` |

LLM planning occurs outside the database transaction. The existing process-wide
orchestrator mutex serializes direct callers as well as the sequential poller;
separate owners have separate state but share this scheduling lock.

## Enforced boundaries and state transitions

- Cancellation remains deterministic and never calls a mutation repository.
  Standalone yes/no controls also bypass the LLM. A yes can only execute the
  current single-target confirmation; it cannot resolve ambiguity or resume a
  creation draft. A no clears pending work without changing expenses.
- Delete request → selection when ambiguous → `delete_confirmation` → explicit
  yes → version/owner-guarded delete. A unique request starts at confirmation.
  Selection, cancellation, expiry, replay and stale panel versions never select
  a replacement expense. Bare deletion without a reference asks for a target.
- Explicit read commands cannot dispatch mutations or seed mutation drafts.
  Complete edit/delete commands supersede previous pending work independently of
  the model's optional context flag. Scalar answers remain bound to their field;
  displayed choices must match the actual standalone ordinal reply.
- Explicit `#ID` references cannot be replaced by model-selected IDs. Source
  price tokens constrain monetary values, `за` prices cannot be replaced by
  quantities, and explicit alternatives ask for an amount. Source `не X, а Y`
  corrections enforce both values and the actual previous-amount comparison.
  Other update fields require evidence that those fields were requested.
- Pending decisions have a fixed 30-minute deadline, checked before planning
  and again inside each transaction attempt. Rejected messages do not extend it.
  Activity timestamps and new state publish only after commit; failed commits
  leave the prior pending decision available for retry.
- SQL ownership/version predicates, REPEATABLE READ, rollback/serialization
  retries, update deduplication, response persistence and delivery retries are
  preserved. No new dependencies, migrations, REST changes or provider changes.

## Changed files

All paths below are relative to the repository root.

| Files | Purpose |
|---|---|
| `backend/internal/conversation/safety.go` (new) | Control replies, command-family safety boundary, source corrections/IDs/fields, confirmed delete |
| `backend/internal/conversation/orchestrator.go`, `state.go`, `execute.go` | Fixed deadlines, commit-only state publication, independent commands, source validation and deletion confirmation transitions |
| `backend/internal/conversation/description.go`, `tools.go` | Validate source/marked/alternative prices; align deletion tool guidance |
| `backend/internal/botresponse/format.go` | Render the bounded, escaped yes/no deletion question with ID, price and description |
| `backend/internal/conversation/safety_test.go` (new) | Hostile model outputs, target/source constraints, deadlines, concurrency/deduplication and state regressions |
| `backend/internal/conversation/description_test.go`, `diagnostics_test.go`, `formatting_test.go`, `listing_test.go`, `modification_test.go`, `orchestrator_test.go` | Preserve existing assertions with explicit confirmation and realistic source text; retain safe logging checks |
| `backend/internal/telegram/safety_test.go` (new) | Sequential real-PostgreSQL scenarios through HTTP-mocked APIs and actual orchestration |
| `backend/internal/telegram/modification_test.go` | Reuse the workflow with controllable timeout/429/delivery failures; adapt existing deletion tests |
| `backend/internal/telegram/assignment_test.go`, `cancellation_test.go`, `pipeline_test.go` | Explicit confirmation in old sequences and source amounts in synthetic creation fixtures |
| `backend/internal/postgres/description_test.go`, `telegram_test.go` | Confirmed deletion persistence and realistic identity/stale-selection source fixtures |
| `README.md`, `docs/requirements.md`, `docs/architecture.md`, `docs/tasks.md`, this report | Document behavior, findings, verification and remaining manual acceptance |

## Regression coverage

| Requested scenario | Evidence |
|---|---|
| A: full CRUD | `TestAuditFullCRUDSequenceIntegration`: exact count/amount after each mutation, unchanged fields, read snapshots, confirmation before deletion and repeated yes |
| B: repeated cancellation | Existing `TestCancellationIdleAndAfterDeletionIntegration` and pending cancellation/replay tests rerun with confirmation; repository counters and complete DB snapshots |
| C: ambiguous deletion | `TestAuditAmbiguousDeletionAndMixedStateIntegration`: no mutation before selection/confirmation, bare yes cannot select, no cancels deletion |
| D: stale incomplete operation | Same sequential test: missing coffee amount → independent food creation; amount/state unit regressions also rerun |
| E: mixed commands | Same test: list → next page → ambiguous update → stop → create → fresh list; previous/backward message-budget pagination tests rerun unchanged |
| F: multiple users | `TestAuditDeleteConfirmationIsolationIntegration`, existing read/total/update/delete A/B tests and C denial before replay/LLM/DB |
| G: failure recovery | `TestAuditModelFailureRecoveryIntegration` (timeout, 429, malformed arguments and replay); `TestAuditDeleteCommitAndDeliveryRecoveryIntegration` (real deferred commit failure after DELETE, rollback, same-update retry, exhausted delivery attempts and replay) |
| Panel race/stale target | `TestAuditPendingDeletePanelChangesIntegration`: actual REST amount/comment/owner changes and deletion while confirmation is pending; subsequent and repeated yes preserve complete DB snapshots |
| Concurrent messages | `TestAuditConcurrentMessagesAndDeduplication`: 40 simultaneous calls, 20 distinct update IDs across A/B; existing real-PostgreSQL concurrent duplicate and owner-write races rerun |
| Expiry during interaction/planning | `TestAuditConfirmationDeadlineDoesNotSlide`, `TestAuditPendingExpiresDuringPlanning`: controllable clock; no wall-clock 30-minute wait |
| Tool/protocol/logging failures | Existing zero/multiple/unknown/malformed calls, null/duplicate fields, unavailable tools, provider failures and sensitive-data diagnostic tests rerun |

Fixtures are synthetic. PostgreSQL tests use random isolated schemas in a
dedicated disposable PostgreSQL 17 container on loopback port 18716. They never
connect to or reset the development database. Expiry and direct concurrent-state
tests use the memory test repository; SQL atomicity/failures use real PostgreSQL.

## Verification results

Final command results are recorded in `docs/tasks.md`. Failed reproduction runs
preceded fixes for the findings above. The initial unprivileged baseline could
not open loopback sockets; the baseline rerun with local socket access passed.
Subsequent full checks used the disposable database and local HTTP mocks.

During implementation, regression runs also caught a new comma-boundary conflict
in previous-amount verification. It was corrected before final acceptance; failed
intermediate runs are not counted as successful verification.

## Remaining limits and manual acceptance

- **Not live-verified:** this audit made no real Telegram or paid OpenRouter
  calls. Mocked completions prove orchestration and validation, not GPT-4.1 Mini's
  understanding of all Russian phrasing. The existing configured model/provider
  is unchanged.
- The source checks deliberately handle explicit controls, command families,
  numeric prices, displayed ordinals and correction/ID syntax. They are not a
  complete semantic proof of arbitrary language. Unrecognized edit phrasing,
  spelled-out amounts or unsupported selection wording can require restatement;
  free-form descriptions/categories/dates still depend on model interpretation.
- One process and the existing sequential poller are the deployment model.
  The process-wide mutex also serializes direct callers from different users,
  so one slow model call delays other users. Multiple independent bot instances
  do not share in-memory conversations. No distributed state was introduced.
- Restart loses focus/pending confirmations. An uncertain commit still uses
  stored-result replay; memory cannot reconstruct a lost pending decision and
  the user may need to restate it. The stored expense mutation is not repeated.
- Telegram delivery is at least once: a network failure after acceptance may
  duplicate a visible reply. Expense mutations remain deduplicated.
- Offset pagination can shift if the panel concurrently adds/deletes records.
  Stored page history fixes backward positions for a stable dataset, not a
  persistent cross-message snapshot. No schema or pagination redesign was made.
- The trusted-network panel remains unauthenticated by design. The development
  installation was not redeployed; apply these source changes by rebuilding its
  application service during the user's deployment process.

Manual checklist, using only disposable expense records in a test installation:

1. With allowed A, run Scenario A exactly: `тестовое такси 300` →
   `последнее было 450` → `мои расходы` →
   `измени сумму тестового такси на 600` → `удали тестовое такси` → `да` →
   `мои расходы`. Check the panel after each step; no deletion before yes.
2. Create three records and send `стоп` three times. Check all fields unchanged.
   Confirm `купил стоп-сигнал за 500` creates an ordinary expense.
3. Create two taxis, request deletion, choose one, then answer `нет`. Both remain.
   Repeat with yes; verify the displayed ID is the only deleted ID. Send yes again.
4. Start a missing-amount draft, then send a complete new expense. Mix listing,
   next/back pages, an incomplete update, cancellation and another creation.
5. Test the six amount/description examples from the audit request, including
   decimals/model numbers/quantities. `магазин 123$` must ask for RUB; omitted
   currency must use RUB. `кофе 200 или 300` must ask which amount.
6. Keep A's confirmation pending while B lists/edits/deletes B's own data and
   sends yes; A's record stays unchanged. Denied C must obtain no expense data.
7. While A awaits deletion confirmation, change, reassign or delete that exact
   record through the panel; A's yes must report a stale/missing target and leave
   every other record intact. Test confirmation expiry and restart separately.
8. Simulate model/Telegram unavailability in the test environment. Retry delivery
   of the same update and verify no additional mutations. Do not replay synthetic
   tests or inject failures against the development database.
