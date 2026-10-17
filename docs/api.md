# FinBot REST API

The web panel API contract. Assignment obligations and project decisions are described in [requirements.md](requirements.md); implementation is described in [architecture.md](architecture.md). Pagination, sorting, and the formats below are project decisions.

The API requires no authentication. Any client with network access can work with all expenses. The Telegram allowlist does not restrict REST or owner assignment in the panel.

## General Rules

- The base path is `/api`; field names use `snake_case`.
- Responses with a body use `Content-Type: application/json`. JSON is encoded as UTF-8.
- POST and PATCH accept `Content-Type: application/json`, optionally with `charset=utf-8`; the body is a single JSON object.
- POST and PATCH accept at most 1 MiB (1,048,576 bytes), including JSON whitespace. A larger body returns `413` with code `request_too_large` and `fields: {}`, before JSON or field validation and without changing expenses. This applies to streamed bodies without `Content-Length` as well. Routing, query, Origin, and media-type checks retain precedence.
- Unknown body fields are prohibited. Unknown query parameters are also prohibited.
- IDs and amounts are transmitted as strings, not JSON numbers. Pagination metadata uses JSON numbers.
- Every field of the full object is present in the response; an absent comment is represented as `null`.

## Full Expense Object

| Field | Format | Nullable in response | Required in POST | Allowed in PATCH |
|---|---|---|---|---|
| `id` | Positive `int64` as a string | No | No; must not be supplied | No |
| `user_id` | Positive `int64` as a string | No | Yes | Yes |
| `amount` | Decimal string | No | Yes | Yes |
| `category` | String from the category list | No | Yes | Yes |
| `spent_at` | Valid calendar date in `YYYY-MM-DD` format, without a time or time zone | No | Yes | Yes |
| `comment` | String or `null` | Yes | No; defaults to `null` | Yes |
| `created_at` | RFC 3339 in UTC, with a `Z` suffix | No | No; must not be supplied | No |
| `updated_at` | RFC 3339 in UTC, with a `Z` suffix | No | No; must not be supplied | No |

IDs range from `"1"` to `"9223372036854775807"`, without a sign, whitespace, or leading zeros. The same rule applies to the path and the `user_id` filter. A well-formed but nonexistent expense ID returns `404`; an invalid format returns `400`.

An amount is strictly greater than zero and does not exceed `"999999999999.99"`. The integer part has no leading zeros except for zero itself; an optional fractional part contains one or two digits separated by a decimal point. Requests accept `"350"`, `"350.5"`, `"350.50"`, and `"0.01"`. Responses always contain two decimal places. JSON numbers, signs, whitespace, commas, exponents, and more than two fractional digits are rejected without rounding.

Categories: `еда`, `транспорт`, `жильё`, `развлечения`, `здоровье`, `покупки`, `другое`. Case and spelling must match exactly.

A comment may be a string, including an empty string, or `null`. An empty string is stored as an empty string; `null` means no comment. Other fields cannot be `null`.

Example of a full object:

```json
{
  "id": "42",
  "user_id": "123456789",
  "amount": "350.00",
  "category": "еда",
  "spent_at": "2026-10-16",
  "comment": "кофе",
  "created_at": "2026-10-16T09:00:00Z",
  "updated_at": "2026-10-16T09:00:00Z"
}
```

## Get an Expense

`GET /api/expenses/{id}`

- `200`: the full expense object without an additional wrapper.
- `400`: invalid ID.
- `404`: expense not found.

## Create an Expense

`POST /api/expenses`

`user_id`, `amount`, `category`, and `spent_at` are required. `comment` is optional; if omitted, it is stored as `null`. The server sets system fields.

```json
{
  "user_id": "123456789",
  "amount": "350",
  "category": "еда",
  "spent_at": "2026-10-16",
  "comment": "кофе"
}
```

The response is `201` with the full expense object. The Telegram ID may be absent from the allowlist and the member lookup list; no separate registration is required.

## Update an Expense

`PATCH /api/expenses/{id}`

The request must contain at least one of the following fields: `user_id`, `amount`, `category`, `spent_at`, `comment`. Omitted fields remain unchanged. `comment: null` clears the comment. An empty object, unknown fields, and system fields are rejected with `400`.

```json
{
  "amount": "450.00",
  "comment": null
}
```

The response is `200` with the full object after the update. The update is atomic: if any field has an error, no other fields are saved. Assigning a new owner changes the expense's availability to the corresponding bot users, provided they are allowed by the allowlist.

## Delete an Expense

`DELETE /api/expenses/{id}`

The response is `204` without a body. A missing record returns `404`, including a repeated deletion of an expense that has already been deleted.

## List Expenses

`GET /api/expenses`

| Parameter | Format and constraints | Default |
|---|---|---|
| `user_id` | One positive owner ID following the general rules | All owners |
| `date_from` | Valid calendar date in `YYYY-MM-DD` format; inclusive lower bound | No lower bound |
| `date_to` | Valid calendar date in `YYYY-MM-DD` format; inclusive upper bound | No upper bound |
| `category` | One value from the category list | All categories |
| `limit` | Integer from 1 to 100 | 50 |
| `offset` | Integer from 0 to 2147483647 | 0 |
| `sort` | `spent_at`, `created_at`, `amount`, `id` | `spent_at` |
| `order` | `asc` or `desc` | `desc` |

Filters are combined with AND. `date_from` cannot be later than `date_to`. Unknown parameters, repeated parameters, and explicitly supplied empty values return `400`; omitted parameters use their defaults.

Numeric fields are sorted numerically. The secondary key is `id` in the same direction; sorting by `id` needs no additional key. The default order is therefore `spent_at DESC, id DESC`. The sort field and direction are selected from fixed lists.

The response is `200`:

```json
{
  "items": [
    {
      "id": "42",
      "user_id": "123456789",
      "amount": "350.00",
      "category": "еда",
      "spent_at": "2026-10-16",
      "comment": "кофе",
      "created_at": "2026-10-16T09:00:00Z",
      "updated_at": "2026-10-16T09:00:00Z"
    }
  ],
  "total": 1,
  "limit": 50,
  "offset": 0
}
```

`total` is the count after filtering and before pagination. `limit` and `offset` are the applied values. `items` is always an array of full objects.

An empty result and an offset beyond the last page return `200`, `items: []`, and the current `total`, rather than `404`. A well-formed filter for an owner with no expenses returns an empty result.

Offset pagination does not provide a data snapshot: rows may shift when changes occur between requests.

## List Members

`GET /api/members`

Returns the unique Telegram IDs of owners of existing expenses, sorted numerically in ascending order. There are no query parameters, filters, or pagination; an unknown parameter returns `400`.

```json
{
  "items": [
    {"user_id": "123456789"}
  ]
}
```

The response is `200`; an empty result is `{"items":[]}`. After an owner's last expense is deleted, their ID disappears from the list. The list is neither an allowlist nor a separate user registry.

## Errors

All API errors with a body use one format:

```json
{
  "error": {
    "code": "validation_error",
    "message": "Некорректные данные",
    "fields": {
      "amount": "Сумма должна быть больше нуля"
    }
  }
}
```

`code` and `message` are strings; `fields` is an object whose keys are field or parameter names and whose values are explanatory strings. All three fields are required. If the error does not relate to an individual field, `fields` is `{}`. `message` is intended for humans; clients distinguish errors by `code`.

| HTTP | `code` | Cause |
|---|---|---|
| `400` | `invalid_json` | Malformed JSON or a body that is not a single JSON object |
| `400` | `validation_error` | Invalid fields, IDs, filters, sorting, pagination, or an empty PATCH |
| `403` | `forbidden` | Rejected Origin on a mutating browser request |
| `404` | `not_found` | The record or API route does not exist |
| `405` | `method_not_allowed` | The method is not supported; the `Allow` header is also returned |
| `413` | `request_too_large` | The JSON request body exceeds 1 MiB (1,048,576 bytes); `fields` is `{}` |
| `415` | `unsupported_media_type` | POST/PATCH was not sent as JSON |
| `500` | `internal_error` | Internal error without SQL, secrets, or technical details |

A Telegram allowlist denial is not a REST error. The API does not treat a Telegram ID in a request as proof of identity: it is the panel's open administrative interface.
