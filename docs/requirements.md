# FinBot Requirements

The [original assignment](test-task-expenses.md) is the source of mandatory requirements. Assignment requirements, project requirements from the current request, and implementation assumptions are documented separately below. Stages are in [tasks.md](tasks.md), the application architecture is in [architecture.md](architecture.md), and the exact REST contract is in [api.md](api.md).

## Mandatory Requirements from the Original Assignment

Identifiers R01–R14 refer only to the assignment. The criteria explain how to verify each obligation; implementation details and interpretations are documented as project decisions.

| ID | Assignment requirement | Acceptance criterion |
|---|---|---|
| R01 | Expense model | Store `id`, Telegram `user_id`, `amount`, `category`, `spent_at`, optional `comment`, `created_at`, and `updated_at`. Categories: еда, транспорт, жильё, развлечения, здоровье, покупки, другое |
| R02 | Add expenses through the bot | Plain text, including «кофе 350» and «такси 1200 вчера», is stored as an expense with an automatically determined amount, category, and date |
| R03 | View expenses through the bot | Support «покажи траты за неделю» and «сколько потратил на еду в этом месяце»: the user receives their own expenses or their total for the requested period |
| R04 | Edit expenses through the bot | The user can edit their own expense in natural language, including «последнее было не 350, а 450» |
| R05 | Delete expenses through the bot | The user can delete their own expense in natural language, including «удали такси» |
| R06 | User isolation | The bot cannot read, calculate totals for, edit, or delete another user's expenses |
| R07 | Natural language | Bot CRUD works without buttons or commands |
| R08 | Expense list in the panel | The panel displays all members' expenses; owner, period, and category filters work individually and in combination |
| R09 | CRUD in the panel | Expenses for all members can be created, viewed, edited, and deleted |
| R10 | REST API | The web panel communicates with the backend through REST |
| R11 | Go backend | The backend is written in Go; the assignment does not prescribe a specific frontend stack |
| R12 | Compose | The application starts with its dependencies through a single `docker compose` invocation |
| R13 | Configurable LLM | LLM access is configurable |
| R14 | Panel without authentication | No sign-in is required to use the panel |

## Project Requirements and Assumptions

“Current request” refers to a change explicitly requested by the user. “Project choice” refers to an implementation approach or interpretation, rather than an additional requirement of the original assignment.

| ID | Decision | Basis |
|---|---|---|
| P01 | A modular Go monolith, PostgreSQL, REST, React/TypeScript, Telegram, and LLM tool calling | Architectural foundation specified in the current request |
| P02 | A configurable allowlist of Telegram IDs; the bot does not execute operations without permission. There is no separate users table | Current request |
| P03 | A single OpenAI-compatible adapter. Verifying one configured endpoint is sufficient for the MVP; local LLM verification is an optional additional check | Current request |
| P04 | Conversation focus takes precedence; without it, use the user's most recently created record. A mismatch with the stated previous amount requires clarification | Current request |
| P05 | Exact JSON contracts, errors, filters, pagination, and sorting are documented in [api.md](api.md); IDs and amounts are transmitted as strings | Current request and project defaults |
| P06 | nginx serves built React and proxies REST to Go; Go runs REST and the bot. Compose starts frontend, application, and PostgreSQL. The panel is intended for a trusted local network (deployment choice revised in Slice 04) | Project choice |
| P07 | Amounts are positive, have two decimal places of precision, and do not use `float`. Currency is not stored, displayed, or converted | Project choice |
| P08 | The default time zone is `Europe/Moscow` and is configurable. Relative dates are calculated from the Telegram message timestamp; filter boundaries are inclusive | Project choice |
| P09 | Context is stored in memory separately for each user and expires after 30 minutes of inactivity; ambiguous actions require clarification in text | Project choice |
| P10 | One family per installation; members are identified by Telegram ID. Private text messages are supported; each request modifies one expense | Project choice |
| P11 | The panel can change the owner, amount, category, date, and comment. The server generates IDs and timestamps | Project choice |
| P12 | A persistent volume, automatic migrations, transactional protection against duplicate delivery, and server-side validation of LLM tools | Project choice for correctness and security |

## Member Display Names (Requested Extension)

Administrators may assign optional human-readable names to immutable Telegram IDs through the panel and member API. This is an extension requested after Slice 04, not a requirement of the original assignment. Metadata lives separately from expenses; names are trimmed, limited to 100 Unicode characters, reject control characters, and need not be unique. Blank/null names use the numeric ID fallback. Both saved metadata IDs and owners discovered in expenses appear in the member list. Names do not change ownership, permissions, the bot allowlist, or Telegram processing; no Telegram profile lookup is performed.

## Bot Allowlist

- `TELEGRAM_ALLOWED_USER_IDS` contains positive Telegram IDs separated by commas. Whitespace around entries is allowed; duplicates are removed.
- A missing or empty value denies bot access to everyone; the panel continues to work. A malformed non-empty list is a startup configuration error.
- The list is loaded at startup; changes require a restart. The format is specified in [architecture.md](architecture.md).
- Check `message.from.id` before calling the LLM, reading a stored message result, or performing expense operations. A user outside the list receives a generic denial without data disclosure.
- Permission to use the bot does not grant access to other users' expenses. Every query remains scoped to the user's own `user_id`.
- The allowlist does not restrict the panel's REST API or owner assignment in the panel. Records with an owner outside the list are allowed, but that owner cannot use the bot until permission is granted.

## Dates and Conversation

- If no date is specified, use today's date in the configured time zone relative to the original message.
- «За неделю» means today and the six preceding days; «на этой неделе» means Monday through today; «в этом месяце» means the first day of the month through today.
- Focus is set after creating, editing, or unambiguously selecting one of the user's own expenses. A result containing multiple records does not select one automatically; deleting the selected expense clears the focus.
- When there is no focus, including after context expiration or a restart, «последняя трата» is the user's own record ordered by `created_at DESC, id DESC`, regardless of `spent_at`.
- If the user has no expenses, the bot reports this without changing any data.
- If a previous amount is stated, compare it with the actual amount before making a change; on a mismatch, the bot asks for clarification.
- If an existing focus becomes stale because the record was changed, deleted, or reassigned through the panel, the bot asks for clarification without silently switching to another record.
- Before a deferred change, ownership and record freshness are checked atomically with the write.
- With multiple matches, such as «удали такси», nothing changes until clarification is received in text. The bot also asks for missing mandatory information in text.

## LLM and MVP Acceptance

A single adapter is configured through `LLM_BASE_URL`, `LLM_MODEL`, optional `LLM_API_KEY`, and `LLM_TIMEOUT`. The MVP requires automated contract checks and a manual run against one configured endpoint that supports tool calling.

A local server can be connected in the same way, but live verification is additional and does not block the MVP. There is no automatic fallback to the cloud. The model server is provided separately; model installation and GPU management are outside the project scope.

The MVP is ready when R01–R14 and the mandatory project rules have been verified, security scenarios pass, startup is reproducible, and limitations are documented. A local run is not a mandatory delivery criterion. Tasks and checks are in [tasks.md](tasks.md).

## Risks and Scope Boundaries

| Risk | Decision and remaining limitation |
|---|---|
| The panel does not verify the head of the family's identity | Anyone with network access to the panel or API can work with all expenses; the bot allowlist does not change this |
| Allowlist configuration error | An empty list denies bot access to everyone; an invalid non-empty list prevents startup; changes require a restart |
| External service availability | Startup interpretation: Compose starts the application and database; Telegram and the configured LLM endpoint are provided separately |
| Model compatibility | Plain text generation is insufficient; tool calling is verified for the selected model |
| Local endpoint not verified | This is a known status of an additional check, not a reason to reject an otherwise complete MVP |
| LLM errors and malicious instructions | The server validates arguments and ownership; the model has no administrative tools or SQL access |
| Context loss | After a restart, the explicitly defined selection of the user's most recently created expense applies; the previous amount is checked before a change |
| Duplicate delivery and stale data | The result and change are committed in one transaction; a deferred operation rechecks ownership and record freshness |
| Changes while browsing pages | Offset pagination allows rows to shift between requests; stable sorting does not provide a data snapshot |
| Data sent to a provider | Send only the necessary data for the current user, without a family-wide dataset or secrets; there is no automatic switching between servers |

Do not add panel authentication, multiple families, roles, invitations, budgets, charts, exports, receipts, voice messages, recurring expenses, or multiple currencies. Any extension requires separate justification.
