---
title: Port rusik636/nalogo Python library to Go with full feature parity
---

## Initial User Prompt

Порт Python-библиотеки rusik636/nalogo (async-клиент API ФНС «Мой налог», lknpd.nalog.ru) на Go в виде standalone-библиотеки с полным паритетом фич.

Целевой репо: github.com/AlexZzz/nalogo. Рабочий каталог: /Users/aleksei/repos/nalogo-go (модуль уже инициализирован: github.com/AlexZzz/nalogo, Go 1.26, MIT).

Scope паритета: авторизация (ИНН+пароль, SMS, refresh токенов), регистрация дохода (single/multi-item), аннулирование чеков, получение чека (JSON, print URL, валидация), профиль пользователя, способы оплаты, история налогов/OKTMO, иерархия исключений (ValidationException/UnauthorizedException и т.п.), маскирование sensitive-данных в логах, персистентное хранение токенов.

Тесты: интеграционные через httptest.Server с зафикшенными ответами апстрима, целевое покрытие сравнимое с апстримом (~88%).

Подход: light SDD — один sdd:plan на дизайн-пасс (Go-идиомы: маппинг Pydantic→структуры с тэгами, иерархия ошибок, context.Context, options pattern для клиента, net/http вместо httpx), потом sdd:add-task на каждый модуль апстрима (auth, income, receipts, users, payments, taxes), без sdd:implement с LLM-as-Judge — верификация через интеграционные тесты на фикстурах.

Апстрим: https://github.com/rusik636/nalogo (Python 3.11+, httpx, Pydantic v2).

## Description

> **Required Skill**: You MUST use and analyse `python-to-go-port-patterns` skill before doing any modification to task file or starting implementation of it!
>
> Skill location: `.claude/skills/python-to-go-port-patterns/SKILL.md`

NaloGO is a standalone Go library that ports the Python library `rusik636/nalogo` (the async client for the Russian Federal Tax Service "Мой налог" API at lknpd.nalog.ru) into idiomatic Go at module path `github.com/AlexZzz/nalogo`, MIT-licensed, Go 1.26. The library lets Go services authenticate Russian self-employed taxpayers, issue and cancel income receipts, fetch receipt data and print URLs, and read user / payment-type / tax information — all with decimal-precise money handling, automatic single-flight refresh of expired access tokens, pluggable persistent token storage, typed errors (`errors.Is` / `errors.As` compatible), and audit-safe `log/slog` logging that masks ИНН, phone numbers, tokens, and passwords. The reference behavior is the upstream Python package; on-the-wire JSON shapes (field names, Russian `CancelComment` values, `/v1` vs `/v2` paths, ATOM/RFC3339 datetime serialization) are byte-compatible with upstream so the FNS server treats Go-issued requests identically.

This unblocks Go services that need FNS integration today (currently they must run Python alongside or hand-roll an HTTP client and re-derive auth / refresh / masking semantics from scratch). The artifact also serves as a maintained open-source reference implementation for the Go ecosystem: the Python upstream achieves ~88% test coverage; the Go port targets the same.

Audience: Go developers building самозанятый-facing tooling (invoicing services, accounting integrations, freelancing platforms) who want a typed, ctx-aware client with no Python runtime dependency.

**Scope**:

- Included:
  - Auth: ИНН+пароль (`POST /v1/auth/lkfl`), SMS two-step (`POST /v2/auth/challenge/sms/start` then `POST /v1/auth/challenge/sms/verify`), refresh (`POST /v1/auth/token`), persistent token storage via a pluggable interface with a file-based default implementation.
  - Income: `Create` (single-item), `CreateMultipleItems`, `Cancel` — with client-side validation (positive amounts, non-empty UUIDs, legal-entity INN/displayName), decimal-precise totals via `shopspring/decimal`, Russian `CancelComment` wire values preserved.
  - Receipt: `JSON` (`GET /receipt/{inn}/{uuid}/json`) and `PrintURL` (pure, no I/O).
  - User profile (`GET /v1/user`), PaymentType `Table` + `Favorite` (`GET /v1/payment-type/table`), Tax `Get` / `History` / `Payments` (`GET /v1/taxes`, `POST /v1/taxes/history`, `POST /v1/taxes/payments`).
  - Typed error hierarchy: `ValidationError`(400), `UnauthorizedError`(401), `ForbiddenError`(403), `NotFoundError`(404), `ClientError`(406), `PhoneError`(422), `ServerError`(500), `UnknownError`(other ≥ 400) — all `errors.Is` and `errors.As` compatible, carrying status code and response body.
  - Sensitive-data masking in `log/slog` records.
  - Idiomatic Go API surface: `context.Context` first arg on every network method, functional-options constructor, `sync.Mutex` for refresh single-flight, no panics on predictable failure modes.
  - Integration tests on `httptest.Server` with fixtures derived from upstream Python tests; coverage ≥ 0.85 (target ~0.88).

- Excluded:
  - CLI binary, demo server, web UI.
  - Bulk/batch issue orchestration, payout integration, retry policies beyond the auth-refresh path.
  - Invoice API (upstream Python marks it "Not Implemented").
  - Translation of upstream Russian wire values to English (the FNS API requires the Russian `CancelComment` strings byte-for-byte; library API is otherwise English).
  - Live-FNS endpoint smoke tests in CI.
  - Backwards-compatible parity with the original PHP `shoman4eg/moy-nalog` library — only the Python upstream is the behavior reference.

**User Scenarios**:

1. **Primary Flow**: A Go service authenticates via ИНН+пароль, issues a multi-item income receipt with a decimal-precise total, optionally retrieves its JSON or print URL, and (on demand) cancels it — all token persistence and expiry handled transparently.
2. **Alternative Flow**: A Go service authenticates via SMS challenge+verify (two-step phone flow) instead of ИНН+пароль; subsequent operations are identical.
3. **Error Handling**: Pre-flight client-side validation (empty UUIDs, non-positive amounts, legal-entity-without-INN) returns a typed `*ValidationError` (or its sentinel-based equivalent) before any HTTP call; HTTP error responses surface as the appropriate typed error reachable via both `errors.Is` sentinels and `errors.As` struct unwrap; expired access tokens trigger exactly one refresh attempt (single-flight under concurrency) and one retry, then surface `ErrUnauthorized` if refresh also fails.

---

## Acceptance Criteria

### Functional Requirements

- [ ] **AC-1 (Auth ИНН+пароль persists token)**: Library authenticates via ИНН+пароль and persists the resulting token to the configured storage.
  - Given: A configured file-based `TokenStore` and a fixture for `POST /v1/auth/lkfl` returning `200` with token JSON.
  - When: Caller invokes `CreateAccessToken` with a valid ИНН and password.
  - Then: Token JSON is written to the file path and a subsequent `Authenticate` call enables authenticated requests with the correct `Bearer` `Authorization` header.

- [ ] **AC-2 (Auth SMS two-step flow)**: Library completes the two-step SMS challenge / verify flow.
  - Given: Fixture A for `POST /v2/auth/challenge/sms/start` returning `200` with a `challengeToken` and fixture B for `POST /v1/auth/challenge/sms/verify` returning `200` with token JSON.
  - When: Caller invokes `CreatePhoneChallenge` then `CreateAccessTokenByPhone` in sequence.
  - Then: Both calls succeed and the resulting token is persisted to the configured store.

- [ ] **AC-3 (Refresh-on-401, single retry)**: Library performs at most one refresh and one retry on 401.
  - Given: A fixture endpoint that returns `401` once then `200` on retry, and a refresh fixture returning `200` with a new token.
  - When: Client issues an authenticated request.
  - Then: Library performs exactly one refresh round-trip and one retry of the original request, and the caller observes the final `200` response.

- [ ] **AC-4 (Refresh single-flight under concurrency)**: Concurrent 401s collapse into a single refresh.
  - Given: 20 goroutines simultaneously issuing authenticated requests, each racing into a fresh `401`.
  - When: A request counter is attached to the refresh fixture endpoint.
  - Then: Exactly one `POST /v1/auth/token` is observed across all goroutines.

- [ ] **AC-5 (Refresh failure surfaces as ErrUnauthorized)**: Refresh failure does not loop.
  - Given: Both the original endpoint and `/v1/auth/token` fixtures return `401`.
  - When: Client issues an authenticated request.
  - Then: Caller receives an error such that `errors.Is(err, nalogo.ErrUnauthorized)` is `true` and `errors.As(err, &apiErr)` populates `apiErr.StatusCode = 401`.

- [ ] **AC-6 (Decimal-precise multi-item total)**: Totals are computed without float artifacts.
  - Given: Two `IncomeServiceItem`s `{amount=50000, qty=1}` and `{amount=5000, qty=3}`.
  - When: Library posts `/v1/income`.
  - Then: The captured request body has `totalAmount` equal to the exact string `"65000"` (not `"65000.0"`, not `"6.5e4"`).

- [ ] **AC-7 (Validation pre-flight: empty UUID on Cancel)**: Empty UUID is rejected before any HTTP call.
  - Given: No fixture is registered.
  - When: Caller invokes `Income.Cancel(ctx, "", CancelCommentCancel)`.
  - Then: The call returns an error that satisfies `errors.Is(err, nalogo.ErrValidation)` and the fixture's request counter remains `0`.

- [ ] **AC-8 (Validation pre-flight: legal-entity client without INN/DisplayName)**: Legal-entity validation runs locally.
  - Given: An `IncomeClient` with `IncomeType = FROM_LEGAL_ENTITY` and an empty `INN` (or empty `DisplayName`).
  - When: Caller invokes `CreateMultipleItems`.
  - Then: The call returns an error satisfying `errors.Is(err, nalogo.ErrValidation)` before any HTTP request is issued.

- [ ] **AC-9 (Validation pre-flight: positive amount/quantity)**: Non-positive money values are rejected locally.
  - Given: `amount = 0` (or negative) or `quantity = 0` (or negative).
  - When: Caller invokes `Create`.
  - Then: The call returns an error satisfying `errors.Is(err, nalogo.ErrValidation)` before any HTTP request is issued.

- [ ] **AC-10 (Russian wire values preserved byte-for-byte)**: `CancelComment` Russian strings reach the API unchanged.
  - Given: A fixture for `POST /v1/cancel`.
  - When: Caller invokes `Income.Cancel(ctx, uuid, CancelCommentRefund)`.
  - Then: The captured request body's `comment` field equals exactly `"Возврат средств"` as a UTF-8 byte match.

- [ ] **AC-11 (Receipt.PrintURL is pure)**: `PrintURL` performs no I/O.
  - Given: An authenticated client with `profile.inn = "123456789012"` and base URL `https://lknpd.nalog.ru/api`.
  - When: Caller invokes `Receipt.PrintURL("uuid-1")`.
  - Then: The result equals `"https://lknpd.nalog.ru/api/receipt/123456789012/uuid-1/print"` and the fixture server records zero requests.

- [ ] **AC-12 (Receipt.PrintURL pre-auth)**: `PrintURL` errors clearly when not authenticated.
  - Given: A client where `Authenticate` has not been called.
  - When: Caller invokes `Receipt.PrintURL("uuid-1")`.
  - Then: The call returns an error reachable via `errors.Is(err, nalogo.ErrNotAuthenticated)`.

- [ ] **AC-13 (Error hierarchy mapping)**: HTTP statuses map to typed errors deterministically.
  - Given: Fixtures returning each of `400`, `401`, `403`, `404`, `406`, `422`, `500`, and one unmapped status (e.g., `418`).
  - When: Library issues a request reaching each fixture.
  - Then: For each response, `errors.Is(err, sentinel)` (e.g., `ErrValidation`, `ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`, `ErrClient`, `ErrPhone`, `ErrServer`, `ErrUnknown`) and `errors.As(err, &apiErr)` are both `true`; `apiErr.StatusCode` equals the HTTP code; `apiErr.Body` equals the response body (with sensitive fields masked per AC-16).

- [ ] **AC-14 (Functional options applied)**: All constructor options are observably applied.
  - Given: A `Client` constructed with `WithBaseURL`, `WithDeviceID`, `WithHTTPClient`, `WithTokenStore`, `WithLogger`, and `WithTimeout`.
  - When: Auth and API calls are made.
  - Then: Each option is observably applied — `WithBaseURL` prefixes outbound URLs, `WithDeviceID` populates `deviceInfo.sourceDeviceId` in auth payloads, `WithHTTPClient` is the transport actually used, `WithTokenStore` receives `Load`/`Save` calls, `WithLogger` receives records, `WithTimeout` is enforced on outbound requests.

- [ ] **AC-15 (Context cancellation respected)**: Network methods abort when the context expires.
  - Given: A fixture that sleeps 5 seconds before responding.
  - When: Caller invokes `Income.Create` with a context whose deadline is `100ms`.
  - Then: The call returns within ~`100ms` with an error wrapping `context.DeadlineExceeded`.

### Non-Functional Requirements

- [ ] **AC-16 (Slog masking — all sensitive keys)**: No sensitive value leaks into log records.
  - Given: A memory `slog.Handler` attached to the library.
  - When: Library emits any record referencing a request URL, request body, response body, or error containing values for keys `token`, `refreshToken`, `password`, `code` (SMS), `inn`, `phone`, `displayName`, or `Authorization`.
  - Then: No captured record contains those values in plaintext (each is rendered as `***` or omitted).

- [ ] **AC-17 (Coverage)**: Test suite reaches the upstream parity target.
  - Given: The integration test suite running against `httptest.Server` fixtures.
  - When: `go test -cover ./...` is executed.
  - Then: The reported line coverage on the public package is ≥ `0.85` (target ~`0.88`).

- [ ] **AC-18 (Module identity & license)**: Repository declares the correct module path and license.
  - Given: The repository state.
  - When: `go.mod` and `LICENSE` are read.
  - Then: `go.mod` declares `module github.com/AlexZzz/nalogo` with `go 1.26`, and `LICENSE` is the MIT license.

- [ ] **AC-19 (No live-FNS dependency in tests)**: Test suite is hermetic.
  - Given: The CI environment with outbound network blocked (e.g., DNS resolution disabled or all egress blocked).
  - When: `go test ./...` is executed.
  - Then: All tests pass without any attempted connection to `lknpd.nalog.ru` or any other external host.

- [ ] **AC-20 (Idiomatic Go API surface)**: Exported API follows Go conventions.
  - Given: The repository state.
  - When: The exported API is inspected (statically and via tests).
  - Then: Every exported method that performs network I/O accepts `context.Context` as the first argument; the `Client` constructor accepts a variadic `...Option`; every error type implements `error` and is detectable via both `errors.Is` (sentinel) and `errors.As` (struct).

### Definition of Done

- [ ] All acceptance criteria pass.
- [ ] Integration tests written against `httptest.Server` with fixtures derived from upstream Python responses.
- [ ] `go test -cover ./...` ≥ `0.85` (target ~`0.88`).
- [ ] `go vet ./...` passes; `staticcheck ./...` (or equivalent linter) passes.
- [ ] `go.mod` declares `module github.com/AlexZzz/nalogo`, `go 1.26`; `LICENSE` is MIT.
- [ ] Public package GoDoc covers every exported type and function with at least one usage example for `Client`, `Income`, and `Receipt`.
- [ ] No CLI / demo / web artifact is committed to the repository.
- [ ] Tests pass with outbound network blocked (no real-FNS dependency).

---

## Architecture Overview

> References: Skill `/Users/aleksei/repos/nalogo-go/.claude/skills/python-to-go-port-patterns/SKILL.md` — Scratchpad `/Users/aleksei/repos/nalogo-go/.specs/scratchpad/f1e8585b.md`

### Solution Strategy

**Architecture Pattern**: Hexagonal (Ports & Adapters) — the library exposes two outbound ports: the HTTP transport port (abstracted by `http.RoundTripper` and `TokenStore` interface) and the inbound API surface (functional-options `Client`). `authTransport` and `MemoryStore`/`FileStore` are the concrete adapters. All domain logic (income validation, decimal arithmetic, datetime formatting) is independent of HTTP and storage frameworks.

**Approach**: Single `nalogo` package (~14 files). `authTransport` (implements `http.RoundTripper`) injects `Bearer` auth and handles 401 single-flight token refresh under `sync.Mutex`. A separate plain `*http.Client` (`authClient`) is used exclusively for auth/refresh endpoints — it does NOT go through `authTransport`, preventing recursive 401 loops. Token persistence is provided by a `TokenStore` interface with a `MemoryStore` default (zero-config) and a `FileStore` (path-based, matches upstream `storage_path`). Wire format is byte-compatible with the FNS API: Russian `CancelComment` string constants, millisecond-precision `AtomTime` with literal `"Z"` suffix, quoted-string `MoneyAmount` JSON, and `/v1`/`/v2` path split preserved from the upstream Python source.

**Key Decisions**:

1. **Monopkg `nalogo`** (single Go package, ~14 files): upstream Python is 11 flat files; shared types (`TokenData`, `APIError`, `MoneyAmount`, `AtomTime`) cross all API boundaries — sub-packages would require a `nalogo/types` package creating import cycles or import friction. Skill Pattern 8 mandates this; Go library convention (e.g., `net/http`, `database/sql`) confirms it.

2. **`authTransport` as `http.RoundTripper`**: single composable locus for Bearer injection and 401 retry; testable via `WithHTTPClient` injection at construction time; stackable with logging or test transports. Auth endpoints (`CreateAccessToken`, refresh) use a plain `authClient` that bypasses `authTransport` entirely.

3. **`sync.Mutex` + double-check for refresh single-flight**: after acquiring the lock, re-read `TokenStore` to detect if a concurrent goroutine already completed a refresh — if the stored token has changed, return it without issuing another `POST /v1/auth/token`. This collapses all concurrent 401s into exactly one refresh call (AC-4).

4. **`MoneyAmount` wrapper over `shopspring/decimal`**: `shopspring/decimal` default JSON marshaling outputs unquoted numbers; FNS API requires quoted decimal strings (`"100.50"` not `100.50`). `MoneyAmount.MarshalJSON()` wraps the value in quotes; `UnmarshalJSON` strips them. `decimal.Decimal` arithmetic precision is preserved for total computation.

5. **`AtomTime` wrapper**: `time.RFC3339` emits `"+00:00"`; FNS API requires literal `"Z"`. `AtomTime.MarshalJSON()` uses format `"2006-01-02T15:04:05.000Z"` (millisecond precision + `Z`), matching upstream Python `isoformat().replace('+00:00', 'Z')`.

6. **Hand-rolled field validation** (no `go-playground/validator`): upstream has exactly 4 field validators; reflection-based struct tags add dependency weight and unfamiliar API surface for consumers. Validation runs at method call boundaries before any HTTP is issued.

**Trade-offs Accepted**:
- Monopkg means ~14 files in one package: accepted for zero import-path friction and no type-sharing complexity across packages.
- `authTransport` is stateful (holds `sync.Mutex` + `TokenStore` ref): accepted because per-method auth injection would duplicate retry logic across all 5 API modules.
- `FileStore.Load` silently ignores file-not-found and JSON parse errors: accepted for behavioral parity with Python's `except (JSONDecodeError, OSError): pass`.

---

### Architecture Decomposition

**Components**:

| Component | File | Responsibility | Dependencies |
|-----------|------|---------------|--------------|
| `Client` | `client.go` | Top-level facade; `New()` constructor; `Income()`/`Receipt()`/`Tax()`/`User()`/`PaymentType()` factories; wires `apiClient` and `authClient` | options, transport, tokenstore, errors |
| `Option` / `config` | `options.go` | `Option func(*config)` type; `WithBaseURL`, `WithTimeout`, `WithDeviceID`, `WithTokenStore`, `WithHTTPClient`, `WithLogger`; unexported `config` struct with defaults | tokenstore (interface only) |
| `authTransport` | `transport.go` | `http.RoundTripper` impl; Bearer injection; 401 single-flight refresh under `sync.Mutex` with double-check; `HTTPDoer` interface for test injection | tokenstore, errors |
| `TokenStore` / `MemoryStore` / `FileStore` | `tokenstore.go` | `TokenStore` interface (`Save`/`Load`/`Clear`); `TokenData` and `UserProfile` structs; `MemoryStore` (`sync.RWMutex`); `FileStore` (`os.WriteFile` at `0600`) | stdlib only |
| Error hierarchy | `errors.go` | `ErrDomain` + all `Err*` sentinels + `ErrNotAuthenticated`; `APIError` struct + `Is`/`Unwrap`/`Error`; `checkResponse`; `statusToSentinel`; `newValidationError` | masking |
| Masking | `masking.go` | `MaskedString` (`slog.LogValuer` returning `"***"`); `sanitizeBody` (compiled regex, replaces token/refreshToken/password/secret); `sanitizeHeaders` (masks Authorization, X-Api-Key, Cookie) | stdlib only |
| Money / Time | `money.go` | `MoneyAmount` (quoted decimal JSON); `AtomTime` (`"Z"`-suffix datetime); `AtomTimeNow()`; `generateDeviceID()` (21-char lowercase, mirrors Python `uuid4()[:21]`) | shopspring/decimal, google/uuid |
| Auth methods | `auth.go` | `CreateAccessToken`, `CreatePhoneChallenge`, `CreateAccessTokenByPhone`, `Authenticate` on `*Client`; wire request/response structs; all use plain `authClient` | (same package) |
| Income API | `income.go` | `Income` type; `Create`, `CreateMultipleItems`, `Cancel`; `IncomeServiceItem`, `IncomeClient`, `IncomeRequest`, `CancelRequest` structs; `CancelCommentCancel`/`CancelCommentRefund` consts; pre-flight validation | money, errors |
| Receipt API | `receipt.go` | `Receipt` type; `PrintURL` (pure — no context, no HTTP); `JSON` (HTTP GET) | errors |
| User API | `user.go` | `User` type; `Get`; `UserResponse` struct | errors |
| PaymentType API | `payment.go` | `PaymentType` type; `Table`, `Favorite`; `PaymentTypeEntry` struct | errors |
| Tax API | `taxes.go` | `Tax` type; `Get`, `History`, `Payments`; `TaxResponse`, `TaxHistoryResponse`, `TaxPaymentsResponse` structs | errors |
| Package doc | `doc.go` | Package-level GoDoc; usage examples for `Client`, `Income`, `Receipt` | — |

**Call graph** (abridged — bold paths are new; `authTransport` is transparent to callers):

```
nalogo.New(opts...)
    ├── plainTrans = http.DefaultTransport
    ├── authTrans  = &authTransport{base: plainTrans, store, mu, authClient}
    ├── apiClient  = &http.Client{Transport: authTrans, Timeout}
    └── authClient = &http.Client{Timeout}  ← no authTransport

client.CreateAccessToken(ctx, inn, pwd)         ← authClient (no auth loop risk)
    └── POST baseURL/v1/auth/lkfl
    └── store.Save → c.inn = profile.INN

client.Income().Create(ctx, name, amt, qty)     ← apiClient (through authTransport)
    ├── validate: amt>0, qty>0
    ├── build IncomeRequest (AtomTimeNow, MoneyAmount sum)
    ├── http.NewRequestWithContext → apiClient.Do
    │       └── authTransport.RoundTrip
    │               ├── store.Load → inject Bearer
    │               ├── base.RoundTrip → resp
    │               └── if 401:
    │                       mu.Lock
    │                       store.Load (double-check already refreshed?)
    │                       authClient.Do(POST /v1/auth/token)
    │                       store.Save + mu.Unlock
    │                       retry base.RoundTrip
    └── checkResponse → json.Unmarshal → *IncomeResponse
```

---

### Expected Changes

```
github.com/AlexZzz/nalogo/
├── go.mod            UPDATE  add shopspring/decimal v1.4.0, google/uuid v1.6.0
├── go.sum            UPDATE  generated by go mod tidy
├── doc.go            UPDATE  expand package doc + Client/Income/Receipt examples
├── client.go         NEW     Client struct, New(), Income/Receipt/Tax/User/PaymentType factories
├── options.go        NEW     Option type, config struct, WithBaseURL/Timeout/DeviceID/TokenStore/HTTPClient/Logger
├── transport.go      NEW     authTransport (RoundTripper), HTTPDoer interface, refreshToken (mutex + double-check)
├── tokenstore.go     NEW     TokenData, UserProfile, TokenStore interface, MemoryStore, FileStore
├── errors.go         NEW     ErrDomain + 9 sentinels + ErrNotAuthenticated, APIError, checkResponse
├── masking.go        NEW     MaskedString, sanitizeBody, sanitizeHeaders, bodyMaskREs
├── money.go          NEW     MoneyAmount, AtomTime, AtomTimeNow, generateDeviceID
├── auth.go           NEW     CreateAccessToken, CreatePhoneChallenge, CreateAccessTokenByPhone, Authenticate
├── income.go         NEW     Income, Create, CreateMultipleItems, Cancel, DTOs, CancelComment consts, validation
├── receipt.go        NEW     Receipt, PrintURL (pure), JSON (HTTP)
├── user.go           NEW     User, Get, UserResponse
├── payment.go        NEW     PaymentType, Table, Favorite, PaymentTypeEntry
├── taxes.go          NEW     Tax, Get, History, Payments, TaxResponse/TaxHistoryResponse/TaxPaymentsResponse
├── nalogo_test.go    NEW     hermetic integration tests (AC-1..AC-16), newTestServer helper
└── testdata/         NEW     10 JSON fixture files
    ├── auth_token.json         {token, refreshToken, tokenExpireIn, refreshTokenExpiresIn, profile:{id,inn,...}}
    ├── phone_challenge.json    {challengeToken, expireDate, expireIn}
    ├── income_create.json      {approvedReceiptUuid}
    ├── income_cancel.json      {incomeInfo:{approvedReceiptUuid, cancellationInfo:{comment,...}}}
    ├── receipt_json.json       full receipt object
    ├── payment_types.json      array of payment type objects
    ├── taxes.json              current tax info
    ├── taxes_history.json      history records
    ├── taxes_payments.json     payment records
    └── user.json               UserType response
```

---

### Building Block View

```
┌─────────────────────────────────────────────────────────────────┐
│                       package nalogo                             │
│                                                                  │
│  ┌──────────┐  ┌──────────┐  ┌─────────┐  ┌──────────────────┐ │
│  │  Income  │  │ Receipt  │  │   Tax   │  │  User/PaymentType │ │
│  │income.go │  │receipt.go│  │taxes.go │  │  user.go,        │ │
│  │validation│  │PrintURL  │  │Get      │  │  payment.go      │ │
│  │DTOs      │  │ (pure)   │  │History  │  └──────────────────┘ │
│  └────┬─────┘  └────┬─────┘  │Payments │                        │
│       │              │        └────┬────┘                        │
│       └──────────────┴─────────────┘                             │
│                              │                                    │
│                 ┌────────────▼─────────┐                         │
│                 │       Client         │                         │
│                 │  client.go + auth.go │                         │
│                 │  factories + auth    │                         │
│                 └────┬──────────┬──────┘                         │
│                      │          │                                 │
│           ┌──────────▼──┐  ┌───▼──────────────┐                 │
│           │authTransport│  │  plain authClient │                 │
│           │transport.go │  │  (no RoundTripper)│                 │
│           │sync.Mutex   │  │  auth + refresh   │                 │
│           └──────┬──────┘  └──────────────────┘                 │
│                  │                                                │
│      ┌───────────▼──────────────────────────────────────┐        │
│      │   Foundation (imported by everything above)       │        │
│      │  ┌────────────┐  ┌──────────┐  ┌─────────────┐  │        │
│      │  │ TokenStore │  │ errors.go│  │  masking.go │  │        │
│      │  │tokenstore.g│  │sentinels │  │ MaskedString│  │        │
│      │  │MemoryStore │  │APIError  │  │ sanitize*   │  │        │
│      │  │FileStore   │  │checkResp │  └─────────────┘  │        │
│      │  └────────────┘  └──────────┘                   │        │
│      │  ┌─────────────────────────────────────────────┐ │        │
│      │  │   money.go: MoneyAmount, AtomTime, DeviceID  │ │        │
│      │  └─────────────────────────────────────────────┘ │        │
│      └──────────────────────────────────────────────────┘        │
└─────────────────────────────────────────────────────────────────┘
```

---

### Runtime Scenarios

**Scenario: 401 refresh single-flight under concurrency (AC-3, AC-4)**

```
G1..G20 → income.Create(ctx)
  → apiClient.Do(req) → authTransport.RoundTrip
      → store.Load → inject old Bearer
      → base.RoundTrip → 401 (all 20 goroutines)
      → resp.Body.Close()
      → refreshToken(ctx, curToken):

G1  acquires mu.Lock()  ←── G2..G20 BLOCK here
G1: store.Load() → latest.Token == curToken (not yet refreshed)
G1: authClient.Do(POST /v1/auth/token) → 200 → newToken
G1: store.Save(newToken), mu.Unlock()

G2: acquires lock → store.Load → latest.Token != curToken → return latest, mu.Unlock
G3..G20: same as G2

All 20: inject newToken.Token, retry base.RoundTrip → 200
Exactly 1 POST /v1/auth/token observed across 20 goroutines ✓ (AC-4)
```

**Scenario: Validation pre-flight (AC-7, AC-8, AC-9)**
```
income.Cancel(ctx, "", CancelCommentCancel)
  → receiptUUID == "" → newValidationError("receipt UUID cannot be empty")
  → returns *APIError{Sentinel: ErrValidation, StatusCode: 400}
  → [zero HTTP requests issued]
  → errors.Is(err, ErrValidation) == true ✓
```

**Auth state machine**:
```
[NoToken]
  ── CreateAccessToken / CreateAccessTokenByPhone ──► [TokenPersisted]
  ── Authenticate(tokenJSON) ──► [TokenPersisted + inn cached on Client]

[TokenPersisted]
  ── API call → 200 ──► [TokenPersisted]   (unchanged)
  ── API call → 401 ──► [RefreshInFlight]  (mu.Lock)

[RefreshInFlight]
  ── refresh → 200 ──► [NewTokenPersisted] → retry original → 200
  ── refresh → non-200 ──► [TokenPersisted] + caller receives ErrUnauthorized (AC-5)
```

---

### Architecture Decisions

#### Decision 1: Single `nalogo` package vs sub-packages

**Status**: Accepted

**Context**: Python upstream has 11 files in a flat layout with a sub-directory `dto/` for DTOs; shared types reference across all modules.

**Options**:
1. Single package `nalogo` (~14 files) — chosen
2. Sub-packages: `nalogo/auth`, `nalogo/income`, `nalogo/receipt`, `nalogo/types`
3. Internal sub-package: `nalogo` (surface) + `nalogo/internal` (implementation)

**Decision**: Single package `nalogo`. Shared types (`TokenData`, `APIError`, `MoneyAmount`, `AtomTime`) are referenced by every module; sub-packages create import cycles or force a `nalogo/types` package adding import friction with no isolation benefit. `Receipt` needs `INN` from `TokenData` (an auth concern); `Income` returns `approvedReceiptUuid` used by `Receipt` — these concerns cannot be cleanly split.

**Consequences**:
- Single import path `github.com/AlexZzz/nalogo` for all consumers
- ~14 files; well within Go convention for focused libraries (`net/http` has 27 files)
- All exported types in one namespace — clear for callers, no disambiguation needed

---

#### Decision 2: `authTransport` as `http.RoundTripper` for 401 refresh

**Status**: Accepted

**Context**: Upstream Python uses `asyncio.Lock` inside `AsyncHTTPClient._handle_401_response`; Go needs an equivalent synchronous pattern for concurrent goroutines.

**Options**:
1. Custom `http.RoundTripper` with `sync.Mutex` — chosen
2. `doWithRefresh()` helper called per API method
3. Channel-based refresh goroutine

**Decision**: Custom `http.RoundTripper`. Single composable transport layer; testable by injecting a mock `*http.Client` via `WithHTTPClient`; idiomatic Go HTTP middleware pattern; all auth concerns live in one file (`transport.go`). Auth endpoints use a separate plain `authClient` that does not pass through `authTransport` — prevents recursive 401 loop on `POST /v1/auth/token`.

**Consequences**:
- 401 retry logic is invisible to callers and API module implementations
- `WithHTTPClient` option in tests injects a test transport without any setup boilerplate
- `authTransport` must NOT be reused across multiple `Client` instances (each `New()` creates a fresh one)

---

#### Decision 3: `MoneyAmount` wrapper + `AtomTime` wrapper for wire compatibility

**Status**: Accepted

**Context**: FNS API requires monetary amounts as quoted JSON strings (`"100.50"`) and datetimes with literal `"Z"` suffix; `shopspring/decimal` default JSON emits unquoted numbers; `time.RFC3339` emits `"+00:00"` instead of `"Z"`.

**Options**:
1. `MoneyAmount struct{decimal.Decimal}` + `AtomTime struct{time.Time}` wrappers with custom MarshalJSON/UnmarshalJSON — chosen
2. `string` fields with manual decimal parsing on every use
3. Post-processing `json.RawMessage` rewrite before sending

**Decision**: Dedicated wrapper types. Preserves full `decimal.Decimal` arithmetic API for computation while overriding only JSON serialization. `AtomTime` encapsulates the format string and cannot be accidentally replaced with `time.RFC3339`. Both wrappers implement `json.Marshaler` and `json.Unmarshaler` so standard `encoding/json` handles them transparently.

**Consequences**:
- All monetary fields in DTOs use `MoneyAmount` (not `decimal.Decimal` directly)
- `totalAmount` computation: `item.Amount.Decimal.Mul(item.Quantity.Decimal)` → wrap in `MoneyAmount` for JSON
- Datetime fields use `AtomTime` (not `time.Time` directly)
- Callers access the underlying `decimal.Decimal` via `.Decimal` field when needed for arithmetic

---

### Contracts

**`TokenStore` interface** (persistence port — implement to swap storage backends):
```go
type TokenStore interface {
    Save(ctx context.Context, t *TokenData) error
    Load(ctx context.Context) (*TokenData, error)
    Clear(ctx context.Context) error
}
// Implementations: MemoryStore (default, in-process), FileStore (path-based, persisted)
```

**`Client` constructor**:
```go
func New(opts ...Option) *Client
// Option constructors:
//   WithBaseURL(u string) Option           — default: "https://lknpd.nalog.ru/api"
//   WithTimeout(d time.Duration) Option    — default: 10s
//   WithDeviceID(id string) Option         — default: auto-generated 21-char lowercase UUID fragment
//   WithTokenStore(s TokenStore) Option    — default: &MemoryStore{}
//   WithHTTPClient(c *http.Client) Option  — default: built from authTransport + timeout
//   WithLogger(l *slog.Logger) Option      — default: slog.Default()
```

**Error hierarchy** (all errors wrap `ErrDomain`, all carry `StatusCode` + masked `Body`):
```go
var (
    ErrDomain           = errors.New("nalogo")
    ErrValidation       = fmt.Errorf("%w: validation (400)", ErrDomain)
    ErrUnauthorized     = fmt.Errorf("%w: unauthorized (401)", ErrDomain)
    ErrForbidden        = fmt.Errorf("%w: forbidden (403)", ErrDomain)
    ErrNotFound         = fmt.Errorf("%w: not found (404)", ErrDomain)
    ErrClient           = fmt.Errorf("%w: client error (406)", ErrDomain)
    ErrPhone            = fmt.Errorf("%w: phone error (422)", ErrDomain)
    ErrServer           = fmt.Errorf("%w: server error (500)", ErrDomain)
    ErrUnknown          = fmt.Errorf("%w: unknown error", ErrDomain)
    ErrNotAuthenticated = fmt.Errorf("%w: not authenticated", ErrDomain)
)
type APIError struct {
    Sentinel   error  // one of Err* above
    StatusCode int
    Body       string // sensitive fields masked via sanitizeBody
}
// (*APIError).Is(target) returns true for target == e.Sentinel OR target == ErrDomain
// (*APIError).Unwrap() returns e.Sentinel (enables errors.As chain)
```

**Key public method signatures** (all I/O methods: `context.Context` first; pure methods: no context):
```go
// Auth (on *Client, all use plain authClient)
func (c *Client) CreateAccessToken(ctx context.Context, inn, password string) (string, error)
func (c *Client) CreatePhoneChallenge(ctx context.Context, phone string) (*ChallengeResponse, error)
func (c *Client) CreateAccessTokenByPhone(ctx context.Context, phone, challengeToken, code string) (string, error)
func (c *Client) Authenticate(ctx context.Context, tokenJSON string) error

// API module factories
func (c *Client) Income() *Income
func (c *Client) Receipt() (*Receipt, error)  // error if not authenticated (ErrNotAuthenticated)
func (c *Client) Tax() *Tax
func (c *Client) User() *User
func (c *Client) PaymentType() *PaymentType

// Income
func (i *Income) Create(ctx context.Context, name string, amount, quantity decimal.Decimal) (*IncomeResponse, error)
func (i *Income) CreateMultipleItems(ctx context.Context, services []IncomeServiceItem, opTime *time.Time, client *IncomeClient) (*IncomeResponse, error)
func (i *Income) Cancel(ctx context.Context, receiptUUID string, comment CancelComment) (*CancelResponse, error)

// Receipt
func (r *Receipt) PrintURL(receiptUUID string) (string, error)          // pure — no context, no HTTP
func (r *Receipt) JSON(ctx context.Context, receiptUUID string) (json.RawMessage, error)

// Tax
func (t *Tax) Get(ctx context.Context) (*TaxResponse, error)
func (t *Tax) History(ctx context.Context, oktmo *string) (*TaxHistoryResponse, error)
func (t *Tax) Payments(ctx context.Context, oktmo *string, onlyPaid bool) (*TaxPaymentsResponse, error)
```

**Wire-compatibility constants** (FNS API requires these byte-for-byte):
```go
type CancelComment string
const (
    CancelCommentCancel = CancelComment("Чек сформирован ошибочно")
    CancelCommentRefund = CancelComment("Возврат средств")
)
// AtomTime JSON format: "2006-01-02T15:04:05.000Z"  (NOT time.RFC3339 — that emits "+00:00")
// Auth paths: /v1/auth/lkfl, /v1/auth/challenge/sms/verify, /v1/auth/token
// SMS challenge:  /v2/auth/challenge/sms/start   (v2, not v1)
// Receipt paths:  /receipt/{inn}/{uuid}/json     (no /v1/ prefix)
//                 /receipt/{inn}/{uuid}/print    (no /v1/ prefix)
// DeviceInfo wire: {sourceType:"WEB", sourceDeviceId, appVersion:"1.0.0", metaDetails:{userAgent}}
```

**AC-to-component mapping** (implementation traceability):

| AC | Component(s) |
|----|-------------|
| AC-1 (INN+pwd persists token) | `auth.go` CreateAccessToken + `tokenstore.go` FileStore.Save |
| AC-2 (SMS two-step) | `auth.go` CreatePhoneChallenge + CreateAccessTokenByPhone |
| AC-3 (Refresh on 401, single retry) | `transport.go` authTransport.RoundTrip |
| AC-4 (Refresh single-flight) | `transport.go` refreshToken — `sync.Mutex` + double-check |
| AC-5 (Refresh failure → ErrUnauthorized) | `transport.go` refreshToken returns nil → `*APIError{ErrUnauthorized}` |
| AC-6 (Decimal-precise totalAmount) | `money.go` MoneyAmount + `income.go` totalAmount computation |
| AC-7 (Empty UUID pre-flight) | `income.go` Cancel — `newValidationError` before HTTP |
| AC-8 (Legal-entity validation) | `income.go` CreateMultipleItems — IncomeClient validation |
| AC-9 (Positive amount/qty) | `income.go` Create/CreateMultipleItems — MoneyAmount > 0 check |
| AC-10 (Russian wire values) | `income.go` CancelCommentCancel/Refund consts, direct JSON string encoding |
| AC-11 (PrintURL is pure) | `receipt.go` PrintURL — no context, no HTTP call |
| AC-12 (PrintURL pre-auth error) | `client.go` Receipt() factory + `receipt.go` PrintURL checks inn |
| AC-13 (Error hierarchy mapping) | `errors.go` statusToSentinel maps 400/401/403/404/406/422/500/other |
| AC-14 (Functional options) | `options.go` all WithXxx constructors |
| AC-15 (Context cancellation) | `http.NewRequestWithContext` in every I/O method |
| AC-16 (Slog masking) | `masking.go` MaskedString + sanitizeBody + sanitizeHeaders |
| AC-17 (Coverage ≥ 0.85) | `nalogo_test.go` — all ACs covered by fixture-based hermetic tests |
| AC-18 (Module identity) | `go.mod` — already declares `github.com/AlexZzz/nalogo`, `go 1.26` |
| AC-19 (No live-FNS in tests) | `testdata/` fixtures + `httptest.Server` — no DNS calls |
| AC-20 (Idiomatic Go API) | `context.Context` first-arg, `...Option` constructor, `errors.Is`/`errors.As` |

---

## Implementation Process

### Implementation Strategy

**Approach**: Bottom-Up
**Rationale**: The hardest technical risks live at the foundation — `authTransport` refresh single-flight under concurrency (sync.Mutex double-check pattern, AC-4), and wire-format JSON serialization (`MoneyAmount` quoted decimal, `AtomTime` Z-suffix, Russian `CancelComment` byte-exact strings). Building and unit-testing these primitives before the API-module layer catches bugs early and gives each upper layer a stable base. The six API modules (auth, income, receipt, user, payment, taxes) are thin wrappers once transport and foundation are solid.

---

### Phase Overview

```
Phase 1: Setup          go.mod + Makefile
         │
         ▼
Phase 2: Foundation     masking.go │ tokenstore.go │ money.go  (parallel)
         │                          └──────────────────────┘
         │                                    │
         ▼                                    ▼
         errors.go (depends on masking)
         │
         ▼
Phase 3: Transport      transport.go │ options.go  (parallel)
         │               └────────────────────┘
         │                         │
         ▼                         ▼
         client.go
         │
         ▼
Phase 4: Auth           auth.go
         │
         ▼
Phase 5: API modules    income.go  │  receipt.go  (parallel)
         │               └──────────────────┘
         │
         ▼
Phase 6: Simple modules user.go │ payment.go │ taxes.go  (parallel)
         │               └──────────────────────────┘
         │
         ▼
Phase 7: Fixtures+Doc   testdata/ (10 JSON files) + doc.go expand
         │
         ▼
Phase 8: Tests          nalogo_test.go (all ACs, httptest.Server)
         │
         ▼
Phase 9: Polish         coverage gate ≥ 85%, go vet, staticcheck
```

---

### Step 1: Project Setup — go.mod, go.sum, Makefile

**Goal**: Bring in the three required external dependencies and provide reproducible build/test/lint/coverage targets so every subsequent step compiles and runs tests identically.

**Complexity**: Small
**Uncertainty**: Low
**Dependencies**: None

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/go.mod` — updated with three `require` lines
- `/Users/aleksei/repos/nalogo-go/go.sum` — generated by `go mod tidy`
- `/Users/aleksei/repos/nalogo-go/Makefile` — four targets: `build`, `test`, `lint`, `coverage`

#### Success Criteria

- [ ] `go.mod` contains `require github.com/shopspring/decimal v1.4.0`
- [ ] `go.mod` contains `require github.com/google/uuid v1.6.0`
- [ ] `go.mod` contains `require github.com/stretchr/testify v1.9.0`
- [ ] `go mod tidy` runs without error and generates `go.sum`
- [ ] `make build` runs `go build ./...` without error
- [ ] `make test` runs `go test ./...`
- [ ] `make lint` runs `go vet ./...` (and optionally `staticcheck ./...`)
- [ ] `make coverage` runs `go test -coverprofile=coverage.out ./...` and prints coverage percent

#### Subtasks

- [ ] Add `require` directives for shopspring/decimal v1.4.0, google/uuid v1.6.0, testify v1.9.0 to `go.mod`
- [ ] Run `go mod tidy` to generate `go.sum` and fetch indirect dependencies
- [ ] Create `Makefile` with targets: `build` (`go build ./...`), `test` (`go test -race ./...`), `lint` (`go vet ./...`), `coverage` (`go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out`)
- [ ] Verify `make build` succeeds on the stub `doc.go` already in the repo

#### Blockers

- None

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| shopspring/decimal v1.4.0 not yet tagged (use latest stable) | Low | Low | Check pkg.go.dev; use `v1.3.1` if v1.4.0 absent |

#### Definition of Done

- [ ] `go build ./...` exits 0
- [ ] `go mod tidy` exits 0 with no diff after running
- [ ] `make coverage` prints a coverage line

---

### Step 2: masking.go — Sensitive-data masking for slog

**Goal**: Provide the `MaskedString` slog.LogValuer and `sanitizeBody`/`sanitizeHeaders` helpers that every other component depends on for log-safe output. This is the lowest-level utility with no package dependencies.

**Complexity**: Small
**Uncertainty**: Low
**Dependencies**: Step 1

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/masking.go`

#### Success Criteria

- [ ] `MaskedString` type implements `slog.LogValuer`; `MaskedString("secret").LogValue()` returns `slog.StringValue("***")`
- [ ] `sanitizeBody` replaces JSON fields `token`, `refreshToken`, `password`, `secret`, `inn`, `phone`, `displayName`, `code` values with `***` using compiled `*regexp.Regexp`
- [ ] `sanitizeBody` is idempotent (calling twice produces same result)
- [ ] `sanitizeHeaders` masks `Authorization`, `X-Api-Key`, `Cookie`, `Set-Cookie` header values to `***` (case-insensitive key match)
- [ ] All regex patterns are package-level compiled vars (not compiled per-call)
- [ ] Unit tests in `masking_test.go` pass: at minimum one positive and one negative case per masked field
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `masking.go` with `package nalogo` declaration
- [ ] Define `MaskedString string` type; implement `LogValue() slog.Value` returning `slog.StringValue("***")`
- [ ] Compile package-level `[]*regexp.Regexp` (or a single multi-field regex) for body masking fields: `token`, `refreshToken`, `password`, `secret`, `inn`, `phone`, `displayName`, `code` — pattern `("FIELD":\s*")[^"]*(")`  replaced with `\1***\2`
- [ ] Implement `sanitizeBody(body string) string` applying all body regexps
- [ ] Implement `sanitizeHeaders(h http.Header) http.Header` returning a copy with sensitive header values replaced by `***`
- [ ] Create `masking_test.go`; write table-driven tests covering: each masked field present in body, field absent (no change), header masking (Authorization, X-Api-Key, Cookie)

#### Blockers

- None

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Regex too greedy, masks legitimate data | Medium | Low | Test with multi-field JSON containing both masked and unmasked fields |

#### Definition of Done

- [ ] `masking.go` exists with `MaskedString`, `sanitizeBody`, `sanitizeHeaders`
- [ ] Unit tests written and `go test ./...` exits 0

---

### Step 3: tokenstore.go — Token persistence interface and implementations

**Goal**: Define the `TokenStore` port and provide `MemoryStore` (zero-config default) and `FileStore` (path-based, matches Python `storage_path`) so every component that needs token state has a stable abstraction.

**Complexity**: Medium
**Uncertainty**: Low
**Dependencies**: Step 1

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/tokenstore.go`

#### Success Criteria

- [ ] `TokenStore` interface declares `Save(ctx context.Context, t *TokenData) error`, `Load(ctx context.Context) (*TokenData, error)`, `Clear(ctx context.Context) error`
- [ ] `TokenData` struct contains `Token string`, `RefreshToken string`, `TokenExpireIn string`, `RefreshTokenExpiresIn *string`, `Profile UserProfile` with JSON tags matching upstream wire names (`token`, `refreshToken`, `tokenExpireIn`, `refreshTokenExpiresIn`, `profile`)
- [ ] `UserProfile` struct contains `ID int64`, `INN string`, `DisplayName string`, `Email string`, `Phone string` with matching JSON tags
- [ ] `MemoryStore.Load` returns `nil, nil` when no token has been saved (not an error)
- [ ] `MemoryStore` is safe for concurrent use (uses `sync.RWMutex`)
- [ ] `FileStore.Load` returns `nil, nil` (not an error) when file does not exist or JSON is malformed — mirrors Python `except (JSONDecodeError, OSError): pass`
- [ ] `FileStore.Save` writes file with permissions `0600` and creates parent directories
- [ ] Unit tests in `tokenstore_test.go` cover: MemoryStore round-trip Save/Load/Clear, concurrent Load (race detector), FileStore Save creates file at path, FileStore Load on missing file returns nil/nil, FileStore Load on malformed JSON returns nil/nil
- [ ] `go test -race ./...` exits 0

#### Subtasks

- [ ] Create `tokenstore.go` with `package nalogo`
- [ ] Define `UserProfile` struct with JSON tags matching upstream: `id`, `inn`, `displayName`, `email`, `phone`
- [ ] Define `TokenData` struct with JSON tags: `token`, `refreshToken`, `tokenExpireIn`, `refreshTokenExpiresIn`, `profile`
- [ ] Define `TokenStore` interface with `Save`, `Load`, `Clear` methods (all `context.Context` first arg)
- [ ] Implement `MemoryStore` with `sync.RWMutex`; `Load` returns nil,nil for empty store
- [ ] Implement `FileStore` struct with `Path string`; `Save` writes JSON to `Path` at mode 0600 (creates parent dirs via `os.MkdirAll`); `Load` silently returns nil,nil on `os.IsNotExist` or `json.Unmarshal` error; `Clear` deletes the file (ignores not-found error)
- [ ] Create `tokenstore_test.go`; write tests for all criteria listed above using `t.TempDir()` for FileStore path

#### Blockers

- None

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Race condition in MemoryStore under concurrent writes | High | Low | Use `sync.RWMutex`; validate with `-race` flag |

#### Definition of Done

- [ ] `tokenstore.go` exists with interface, MemoryStore, FileStore
- [ ] Unit tests written and `go test -race ./...` exits 0

---

### Step 4: money.go — MoneyAmount, AtomTime, generateDeviceID

**Goal**: Implement the three wire-format primitives that are referenced by both the income DTOs and the auth DeviceInfo. Getting the JSON serialization right here prevents subtle FNS API rejections.

**Complexity**: Small
**Uncertainty**: Medium (MoneyAmount must produce quoted string preserving decimal places; AtomTime must use "Z" not "+00:00")
**Dependencies**: Step 1

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/money.go`

#### Success Criteria

- [ ] `MoneyAmount` wraps `shopspring/decimal.Decimal` as `struct{ Decimal decimal.Decimal }`
- [ ] `MoneyAmount.MarshalJSON()` produces `"\"100.50\""` (quoted string) — not bare `100.50`
- [ ] `MoneyAmount.UnmarshalJSON()` accepts quoted string `"\"100.50\""` and sets Decimal correctly
- [ ] `json.Marshal(MoneyAmount{Decimal: decimal.NewFromString("50000")})` produces `"50000"` (no trailing `.0` or exponent)
- [ ] `AtomTime` wraps `time.Time` as `struct{ Time time.Time }`
- [ ] `AtomTime.MarshalJSON()` produces format `"2006-01-02T15:04:05.000Z"` with literal `Z` suffix (not `+00:00`)
- [ ] `AtomTimeNow()` returns `AtomTime` wrapping `time.Now().UTC()`
- [ ] `generateDeviceID()` returns a 21-character lowercase string derived from a UUID (first 21 chars after stripping hyphens)
- [ ] Unit tests in `money_test.go` cover: MoneyAmount round-trip JSON, AtomTime MarshalJSON format, generateDeviceID length and charset
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `money.go` with `package nalogo`
- [ ] Import `github.com/shopspring/decimal` and `github.com/google/uuid`
- [ ] Define `MoneyAmount struct{ Decimal decimal.Decimal }`; implement `MarshalJSON()` as `[]byte(fmt.Sprintf("%q", d.Decimal.String()))` (adds surrounding quotes); implement `UnmarshalJSON()` stripping quotes then calling `decimal.NewFromString`
- [ ] Define `AtomTime struct{ Time time.Time }`; implement `MarshalJSON()` using format string `"2006-01-02T15:04:05.000Z"` on `t.UTC()`; implement `UnmarshalJSON()` parsing same format
- [ ] Implement `AtomTimeNow() AtomTime` returning `AtomTime{Time: time.Now().UTC()}`
- [ ] Implement `generateDeviceID() string` using `strings.ReplaceAll(uuid.New().String(), "-", "")[:21]` then `strings.ToLower` (Python behavior: `str(uuid4()).replace("-","")[:21].lower()`)
- [ ] Create `money_test.go`; write tests: MoneyAmount{50000} marshals to `"50000"`, MoneyAmount{100.50} marshals to `"100.50"`, AtomTime UTC marshal contains `Z` suffix not `+00:00`, generateDeviceID length == 21 and all lowercase alphanumeric

#### Blockers

- None

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| shopspring/decimal.String() drops trailing zeros | High | Medium | Use decimal.NewFromString("100.50").String() in test; if it returns "100.5", use StringFixed(2) — but FNS API may accept either; upstream Python uses str(Decimal("100.50")) which preserves trailing zero |
| AtomTime MarshalJSON uses wrong millisecond count | Medium | Low | Pin test to a known UTC time and assert exact string |

#### Definition of Done

- [ ] `money.go` exists with MoneyAmount, AtomTime, AtomTimeNow, generateDeviceID
- [ ] Unit tests written and `go test ./...` exits 0

---

### Step 5: errors.go — Error hierarchy, APIError, checkResponse

**Goal**: Establish the full typed error hierarchy that all HTTP responses and validation pre-flight errors flow through. Correct `errors.Is`/`errors.As` compatibility is critical for AC-5, AC-7, AC-8, AC-9, AC-13.

**Complexity**: Medium
**Uncertainty**: Low
**Dependencies**: Steps 1, 2 (masking — sanitizeBody used in APIError.Body)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/errors.go`

#### Success Criteria

- [ ] `ErrDomain` is the root sentinel: `errors.New("nalogo")`
- [ ] Nine derived sentinels exist wrapping ErrDomain: `ErrValidation`, `ErrUnauthorized`, `ErrForbidden`, `ErrNotFound`, `ErrClient`, `ErrPhone`, `ErrServer`, `ErrUnknown`, `ErrNotAuthenticated`
- [ ] `APIError` struct carries `Sentinel error`, `StatusCode int`, `Body string`
- [ ] `(*APIError).Is(target error)` returns `true` for `target == e.Sentinel` OR `target == ErrDomain`
- [ ] `(*APIError).Unwrap()` returns `e.Sentinel`
- [ ] `(*APIError).Error()` returns a non-empty string including status code
- [ ] `errors.Is(err, ErrValidation)` is `true` when `err` is a `*APIError{Sentinel: ErrValidation}`
- [ ] `errors.As(err, &apiErr)` populates `apiErr.StatusCode` correctly
- [ ] `checkResponse(resp *http.Response) error` returns nil for 2xx, returns `*APIError` for 4xx/5xx with `Body` set to `sanitizeBody(responseBodyString)`
- [ ] `statusToSentinel(code int) error` maps 400→ErrValidation, 401→ErrUnauthorized, 403→ErrForbidden, 404→ErrNotFound, 406→ErrClient, 422→ErrPhone, 500→ErrServer, other≥400→ErrUnknown
- [ ] `newValidationError(msg string) error` returns `*APIError{Sentinel: ErrValidation, StatusCode: 400}` for pre-flight validation failures
- [ ] Unit tests in `errors_test.go` verify: Is/Unwrap/As chain, all status code mappings, newValidationError satisfies errors.Is(err, ErrValidation)
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `errors.go` with `package nalogo`
- [ ] Declare `ErrDomain = errors.New("nalogo")` and nine derived sentinels using `fmt.Errorf("%w: <description>", ErrDomain)`
- [ ] Define `APIError` struct with `Sentinel error`, `StatusCode int`, `Body string`; implement `Error()`, `Is(target error) bool`, `Unwrap() error`
- [ ] Implement `statusToSentinel(code int) error` switch statement
- [ ] Implement `checkResponse(resp *http.Response) error`: read and close body; if status < 400 return nil; set body via `sanitizeBody`; return `&APIError{Sentinel: statusToSentinel(code), StatusCode: code, Body: maskedBody}`
- [ ] Implement `newValidationError(msg string) *APIError` returning `&APIError{Sentinel: ErrValidation, StatusCode: 400, Body: msg}`
- [ ] Create `errors_test.go`; write tests for Is chain, As unwrap, all sentinel mappings, checkResponse on mock response

#### Blockers

- Step 2 (masking.go must exist — sanitizeBody called in checkResponse)

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| errors.Is not returning true through wrapping chain | High | Low | Write explicit test asserting errors.Is(newAPIError(ErrValidation), ErrDomain) == true |

#### Definition of Done

- [ ] `errors.go` exists with full sentinel hierarchy and APIError
- [ ] Unit tests written and `go test ./...` exits 0

---

### Step 6: transport.go — authTransport (RoundTripper with 401 refresh single-flight)

**Goal**: Implement `authTransport` as an `http.RoundTripper` that injects `Bearer` tokens, detects `401` responses, and performs at most one token refresh per concurrent 401 storm using `sync.Mutex` double-check. This is the highest-risk component in the library (AC-3, AC-4, AC-5).

**Complexity**: Medium
**Uncertainty**: Medium (double-check pattern must be correct under race conditions)
**Dependencies**: Steps 3 (TokenStore), 5 (errors — APIError for refresh failure)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/transport.go`

#### Success Criteria

- [ ] `HTTPDoer` interface declares `Do(*http.Request) (*http.Response, error)` (used for test injection of `authClient`)
- [ ] `authTransport` struct holds `base http.RoundTripper`, `store TokenStore`, `mu sync.Mutex`, `authDoer HTTPDoer` (the plain auth client)
- [ ] `authTransport.RoundTrip` injects `Authorization: Bearer <token>` from `store.Load()`
- [ ] On `401` response: acquires `mu`; calls `store.Load()` again; if stored token differs from the token used in original request, releases lock and retries with new token (double-check — another goroutine already refreshed)
- [ ] If stored token same as original (nobody refreshed yet): calls `authDoer.Do(POST /v1/auth/token)` with `{deviceInfo, refreshToken}`; on success calls `store.Save()`; releases lock; retries original request with new token
- [ ] If refresh returns non-200 or error: releases lock; returns `*APIError{Sentinel: ErrUnauthorized, StatusCode: 401}` (does NOT retry further)
- [ ] `authTransport` performs at most one refresh and one retry total per original request (not a loop)
- [ ] Unit test: 20 goroutines concurrently trigger 401 → verify `POST /v1/auth/token` called exactly once (AC-4)
- [ ] Unit test: refresh returns 401 → caller receives error satisfying `errors.Is(err, ErrUnauthorized)` (AC-5)
- [ ] `go test -race ./...` exits 0

#### Subtasks

- [ ] Create `transport.go` with `package nalogo`
- [ ] Define `HTTPDoer interface { Do(*http.Request) (*http.Response, error) }`
- [ ] Define `authTransport struct { base http.RoundTripper; store TokenStore; mu sync.Mutex; authDoer HTTPDoer; baseURL string }`
- [ ] Implement `RoundTrip(req *http.Request) (*http.Response, error)`: load token; clone request; set Authorization header; call `base.RoundTrip`; on 401 call `refreshAndRetry(ctx, req, currentToken)`
- [ ] Implement `refreshAndRetry(ctx context.Context, req *http.Request, curToken string) (*http.Response, error)`: acquire mu; defer mu.Unlock; load latest token; if `latest.Token != curToken` (double-check: already refreshed by peer) skip refresh, set new Bearer and retry; else POST /v1/auth/token with refreshToken; on success save and retry; on failure return ErrUnauthorized
- [ ] Create `transport_test.go`; write tests: single 401 triggers exactly one refresh; 20-goroutine race triggers exactly one POST to /auth/token (use httptest.Server + atomic counter); refresh failure surfaces ErrUnauthorized; no refresh triggered on 200 response

#### Blockers

- Step 3 (TokenStore interface must exist)
- Step 5 (APIError and ErrUnauthorized must exist)

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Double-check comparison uses pointer equality instead of token string equality | High | Medium | Compare `latest.Token != curToken` string values |
| Request body consumed before retry (http.Request.Body is io.ReadCloser) | High | Medium | Clone request body using `GetBody` or `io.NopCloser(bytes.NewReader(...))` before first RoundTrip; document that callers must set `GetBody` |
| Deadlock if RoundTrip calls itself recursively | High | Low | authTransport.base is never itself (plain transport); auth endpoints use authDoer (separate plain client) |

#### Definition of Done

- [ ] `transport.go` exists with HTTPDoer interface and authTransport
- [ ] Concurrency unit test (20 goroutines, single refresh) written and passes with `-race`
- [ ] `go test -race ./...` exits 0

---

### Step 7: options.go — functional options and config defaults

**Goal**: Define the `Option` type and all six `WithXxx` constructors so `client.New()` can be built next. This step is parallel-eligible with Step 6.

**Complexity**: Small
**Uncertainty**: Low
**Dependencies**: Steps 3 (TokenStore interface for WithTokenStore), 5 (errors for any early ErrNotAuthenticated ref)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/options.go`

#### Success Criteria

- [ ] `Option` is `type Option func(*config)`
- [ ] `config` struct contains: `baseURL string` (default `"https://lknpd.nalog.ru/api"`), `timeout time.Duration` (default `10s`), `deviceID string` (default: `generateDeviceID()` result), `store TokenStore` (default: `&MemoryStore{}`), `httpClient *http.Client` (default: nil — built by New()), `logger *slog.Logger` (default: `slog.Default()`)
- [ ] `WithBaseURL(u string) Option` sets `cfg.baseURL`
- [ ] `WithTimeout(d time.Duration) Option` sets `cfg.timeout`
- [ ] `WithDeviceID(id string) Option` sets `cfg.deviceID`
- [ ] `WithTokenStore(s TokenStore) Option` sets `cfg.store`
- [ ] `WithHTTPClient(c *http.Client) Option` sets `cfg.httpClient`
- [ ] `WithLogger(l *slog.Logger) Option` sets `cfg.logger`
- [ ] All options are observably applied when passed to `New()` (covered by AC-14 integration test)
- [ ] Unit tests in `options_test.go` verify each option sets the expected config field
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `options.go` with `package nalogo`
- [ ] Define unexported `config` struct with six fields and their zero values
- [ ] Implement `defaultConfig() config` returning struct with all defaults applied (including calling `generateDeviceID()` for deviceID, `&MemoryStore{}` for store, `slog.Default()` for logger)
- [ ] Define `type Option func(*config)` and implement all six `WithXxx` functions
- [ ] Create `options_test.go`; test that each WithXxx mutates the config field correctly

#### Blockers

- Step 3 (MemoryStore must exist for default store)
- Step 4 (generateDeviceID must exist for default deviceID)

#### Risks

None — this is a mechanical translation.

#### Definition of Done

- [ ] `options.go` exists with all six options
- [ ] Unit tests written and `go test ./...` exits 0

---

### Step 8: client.go — Client constructor and API module factories

**Goal**: Wire all foundation components into the `Client` struct and expose the five API-module factories. The `New()` constructor is the single entry point consumers use; `do()` is the internal HTTP helper shared by all module methods.

**Complexity**: Medium
**Uncertainty**: Low
**Dependencies**: Steps 6 (transport), 7 (options), 3 (tokenstore), 5 (errors)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/client.go`

#### Success Criteria

- [ ] `Client` struct holds `apiClient *http.Client`, `authClient *http.Client`, `store TokenStore`, `logger *slog.Logger`, `baseURL string`, `inn string` (populated by Authenticate/CreateAccessToken), `deviceID string`
- [ ] `New(opts ...Option) *Client` applies all options, creates `authTransport{base: http.DefaultTransport, store, authDoer: authClient}`, wraps it in `apiClient`, creates separate plain `authClient` with configured timeout
- [ ] `WithHTTPClient` option replaces the transport used by `apiClient` (the provided client's transport is used)
- [ ] `do(ctx, method, path string, body any) (*http.Response, error)` marshals body to JSON, calls `apiClient.Do(http.NewRequestWithContext(...))`, calls `checkResponse`
- [ ] `Income() *Income` returns `&Income{client: c}`
- [ ] `Receipt() (*Receipt, error)` returns `*APIError{ErrNotAuthenticated}` if `c.inn == ""`; else returns `&Receipt{client: c, inn: c.inn}`
- [ ] `Tax() *Tax`, `User() *User`, `PaymentType() *PaymentType` return respective types
- [ ] Unit tests in `client_test.go` verify: New() with all WithXxx options observably applied (AC-14), Receipt() returns ErrNotAuthenticated when not authenticated (AC-12)
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `client.go` with `package nalogo`
- [ ] Define `Client` struct with all fields listed above
- [ ] Implement `New(opts ...Option) *Client`: apply options to `defaultConfig()`; build `authClient = &http.Client{Timeout: cfg.timeout}`; build `authTrans = &authTransport{base: http.DefaultTransport, store: cfg.store, authDoer: authClient, baseURL: cfg.baseURL}`; if `cfg.httpClient != nil` use it directly as `apiClient` transport; else build `apiClient = &http.Client{Transport: authTrans, Timeout: cfg.timeout}`
- [ ] Implement `do(ctx context.Context, method, path string, body any) (*http.Response, error)`: marshal body if non-nil; create request with `http.NewRequestWithContext`; set `Content-Type: application/json` and `Accept: application/json`; call `c.apiClient.Do`; return raw response + nil error (caller calls `checkResponse`)
- [ ] Implement all five factory methods
- [ ] Create `client_test.go`; test option observability, Receipt() error when inn empty, context cancellation propagated (AC-15)

#### Blockers

- Step 6 (authTransport must exist)
- Step 7 (options/config must exist)
- Step 5 (errors — ErrNotAuthenticated, checkResponse)

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| authClient accidentally goes through authTransport (recursive refresh loop) | High | Low | authClient = &http.Client{} with no transport override (uses http.DefaultTransport directly); only apiClient uses authTrans |

#### Definition of Done

- [ ] `client.go` exists with Client, New(), do(), all five factories
- [ ] Unit tests written and `go test ./...` exits 0

---

### Step 9: auth.go — Authentication methods and DeviceInfo DTO

**Goal**: Implement all four authentication methods on `*Client` and the private `DeviceInfo` struct used in request payloads. All auth methods bypass `authTransport` (use `c.authClient`) to prevent recursive 401 loops.

**Complexity**: Medium
**Uncertainty**: Low
**Dependencies**: Step 8 (Client), Step 4 (money — generateDeviceID, AtomTime not used in auth but DeviceInfo is money.go-adjacent), Step 5 (errors)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/auth.go`

#### Success Criteria

- [ ] `CreateAccessToken(ctx, inn, password string) (string, error)` POSTs `{"username":inn,"password":password,"deviceInfo":{...}}` to `baseURL/v1/auth/lkfl` using `authClient`; stores result via `store.Save()`; caches `inn` on `c.inn`; returns raw JSON string
- [ ] `CreatePhoneChallenge(ctx, phone string) (*ChallengeResponse, error)` POSTs `{"phone":phone,"requireTpToBeActive":true}` to `baseURL/v2/auth/challenge/sms/start` using `authClient`; returns `*ChallengeResponse{ChallengeToken, ExpireDate, ExpireIn}`
- [ ] `CreateAccessTokenByPhone(ctx, phone, challengeToken, code string) (string, error)` POSTs `{"phone","code","challengeToken","deviceInfo"}` to `baseURL/v1/auth/challenge/sms/verify` using `authClient`; stores result and caches `inn`
- [ ] `Authenticate(ctx, tokenJSON string) error` parses JSON into `*TokenData`; calls `store.Save()`; sets `c.inn = data.Profile.INN`
- [ ] DeviceInfo wire format: `{"sourceType":"WEB","sourceDeviceId":deviceID,"appVersion":"1.0.0","metaDetails":{"userAgent":"Mozilla/5.0 ..."}}`
- [ ] All four methods use `c.authClient` (not `c.apiClient`/`authTransport`) — no Bearer header added on auth requests
- [ ] `Authenticate` called with token containing `profile.inn` sets `c.inn` so `Receipt()` factory succeeds (AC-2, AC-12)
- [ ] Unit tests in `auth_test.go` via httptest.Server cover: CreateAccessToken success persists token (AC-1), CreatePhoneChallenge returns challengeToken, CreateAccessTokenByPhone persists token (AC-2), Authenticate sets inn, CreateAccessToken on 401 returns ErrUnauthorized
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `auth.go` with `package nalogo`
- [ ] Define private `deviceInfo struct` and `marshalDeviceInfo(id string) []byte` producing the wire JSON (sourceType WEB, appVersion 1.0.0, metaDetails.userAgent Mozilla string)
- [ ] Define `ChallengeResponse struct { ChallengeToken string; ExpireDate string; ExpireIn int }` with JSON tags
- [ ] Define `authTokenRequest struct` with `Username`, `Password`, `DeviceInfo json.RawMessage`
- [ ] Define `challengeStartRequest struct` with `Phone`, `RequireTpToBeActive`
- [ ] Define `challengeVerifyRequest struct` with `Phone`, `Code`, `ChallengeToken`, `DeviceInfo json.RawMessage`
- [ ] Define `refreshTokenRequest struct` with `DeviceInfo json.RawMessage`, `RefreshToken string`
- [ ] Implement `CreateAccessToken`, `CreatePhoneChallenge`, `CreateAccessTokenByPhone`, `Authenticate` on `*Client` — each uses `c.authClient.Do(http.NewRequestWithContext(...))`; calls `checkResponse` directly (not via authTransport)
- [ ] Create `auth_test.go`; set up httptest.Server with fixture JSON; test all four methods + error cases

#### Blockers

- Step 8 (Client and authClient must exist)

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| devideInfo userAgent string differs from upstream | Low | Low | Copy exact string from Python dto/device.py |

#### Definition of Done

- [ ] `auth.go` exists with all four methods and DeviceInfo wire format
- [ ] Unit tests written and `go test ./...` exits 0

---

### Step 10: income.go — Income API with validation and decimal-precise totals

**Goal**: Implement the `Income` type with `Create`, `CreateMultipleItems`, and `Cancel`, including all DTOs, pre-flight validation, and decimal-precise `totalAmount` computation. This is the most complex API module.

**Complexity**: Large
**Uncertainty**: Medium (decimal precision behavior; wire-format of all fields must match FNS API byte-for-byte for Russian CancelComment values)
**Dependencies**: Step 8 (Client), Step 4 (money — MoneyAmount, AtomTime), Step 5 (errors — newValidationError)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/income.go`

#### Success Criteria

- [ ] `Income` type holds `client *Client`; `Income()` factory on Client returns `&Income{client: c}`
- [ ] `CancelComment` is `type CancelComment string`; `CancelCommentCancel = CancelComment("Чек сформирован ошибочно")`; `CancelCommentRefund = CancelComment("Возврат средств")`
- [ ] `IncomeServiceItem` struct has `Name string`, `Amount MoneyAmount`, `Quantity MoneyAmount` with camelCase JSON tags (`name`, `amount`, `quantity`)
- [ ] `IncomeClient` struct has `ContactPhone *string`, `DisplayName *string`, `IncomeType string` (default `"FROM_INDIVIDUAL"`), `INN *string` with camelCase JSON tags
- [ ] `IncomeRequest` wire JSON matches upstream Python `model_dump()` output: fields `operationTime`, `requestTime`, `services`, `totalAmount`, `client`, `paymentType`, `ignoreMaxTotalIncomeRestriction`
- [ ] `Create(ctx, name string, amount, quantity decimal.Decimal) (*IncomeResponse, error)` validates amount > 0 and quantity > 0 via `newValidationError` before any HTTP; delegates to `CreateMultipleItems`
- [ ] `CreateMultipleItems(ctx, services []IncomeServiceItem, opTime *time.Time, client *IncomeClient) (*IncomeResponse, error)` validates services non-empty; validates legal entity has INN and DisplayName; computes `totalAmount` using `shopspring/decimal` arithmetic (sum of `Amount.Decimal.Mul(Quantity.Decimal)` per item); serializes as `MoneyAmount`; POSTs to `/v1/income`
- [ ] `Cancel(ctx, receiptUUID string, comment CancelComment) (*CancelResponse, error)` returns `newValidationError` when `receiptUUID == ""`; trims whitespace; POSTs to `/v1/cancel`
- [ ] `CancelRequest` wire JSON: `operationTime`, `requestTime`, `comment` (Russian string), `receiptUuid`, `partnerCode`
- [ ] `totalAmount` for `{50000, qty=1}` + `{5000, qty=3}` = exactly `"65000"` (AC-6)
- [ ] `errors.Is(cancel("", ...), ErrValidation)` is true (AC-7); no HTTP request issued
- [ ] Legal entity missing INN: `errors.Is(err, ErrValidation)` is true (AC-8)
- [ ] Non-positive amount: `errors.Is(err, ErrValidation)` is true (AC-9)
- [ ] `CancelCommentRefund` wire value is exactly `"Возврат средств"` UTF-8 (AC-10)
- [ ] Unit tests cover all pre-flight validation cases; POST body content verified in httptest.Server
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `income.go` with `package nalogo`
- [ ] Define `CancelComment` type and two constants with Russian string values
- [ ] Define `IncomeType string` const values: `IncomeTypeFromIndividual = "FROM_INDIVIDUAL"`, `IncomeTypeFromLegalEntity = "FROM_LEGAL_ENTITY"`, `IncomeTypeFromForeignAgency = "FROM_FOREIGN_AGENCY"`
- [ ] Define `PaymentTypeIncome string` const: `PaymentTypeCash = "CASH"`, `PaymentTypeAccount = "ACCOUNT"`
- [ ] Define `IncomeServiceItem struct` with `Name string`, `Amount MoneyAmount`, `Quantity MoneyAmount` and JSON tags
- [ ] Define `IncomeClient struct` with `ContactPhone *string`, `DisplayName *string`, `IncomeType string`, `INN *string` and camelCase JSON tags; add `DefaultIncomeClient()` helper returning FROM_INDIVIDUAL client
- [ ] Define `IncomeRequest struct` with all wire fields; implement `totalAmountFrom(items []IncomeServiceItem) MoneyAmount` using decimal arithmetic
- [ ] Define `CancelRequest struct` with all wire fields
- [ ] Define `IncomeResponse struct { ApprovedReceiptUUID string \`json:"approvedReceiptUuid"\` }`
- [ ] Define `CancelResponse struct { IncomeInfo json.RawMessage \`json:"incomeInfo"\` }` (or typed struct matching fixture shape)
- [ ] Define `Income struct { client *Client }`
- [ ] Implement `Create`: validate amount > 0 and quantity > 0; build IncomeServiceItem; delegate to CreateMultipleItems
- [ ] Implement `CreateMultipleItems`: validate services non-empty; validate legal entity; compute totalAmount; build IncomeRequest; call `c.client.do(ctx, "POST", "/v1/income", request)`; decode response
- [ ] Implement `Cancel`: validate UUID non-empty; build CancelRequest; call `c.client.do(ctx, "POST", "/v1/cancel", request)`; decode response
- [ ] Create `income_test.go`; test all validation cases (no HTTP issued for pre-flight), POST body shapes, totalAmount precision (AC-6), Russian CancelComment wire value (AC-10)

#### Blockers

- Step 8 (Client.do() must exist)
- Step 4 (MoneyAmount, AtomTime must exist)
- Step 5 (newValidationError must exist)

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Decimal total loses precision (e.g. float64 intermediate) | High | Medium | Never convert Decimal to float; use Decimal.Add(a.Decimal.Mul(b.Decimal)) throughout |
| MoneyAmount.String() drops trailing zeros for whole numbers | Medium | Low | Unit test: MoneyAmount{65000}.MarshalJSON() == "65000" (AC-6) |

#### Definition of Done

- [ ] `income.go` exists with Income type, all three methods, all DTOs, CancelComment consts
- [ ] All pre-flight validation cases have unit tests
- [ ] `go test ./...` exits 0

---

### Step 11: receipt.go — Receipt API (pure PrintURL + JSON fetch)

**Goal**: Implement the `Receipt` type with `PrintURL` (pure, no I/O) and `JSON` (HTTP GET). The path structure has no `/v1/` prefix, which differs from all other API modules.

**Complexity**: Small
**Uncertainty**: Low
**Dependencies**: Step 8 (Client), Step 5 (errors)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/receipt.go`

#### Success Criteria

- [ ] `Receipt` struct holds `client *Client`, `inn string`
- [ ] `PrintURL(receiptUUID string) (string, error)` is pure (no context, no HTTP call); returns `*APIError{ErrNotAuthenticated}` if `r.inn == ""`; trims whitespace from UUID; returns `r.client.baseURL + "/receipt/" + r.inn + "/" + trimmedUUID + "/print"` — note: NO `/v1/` prefix
- [ ] `PrintURL("  uuid-1  ")` returns same as `PrintURL("uuid-1")` (whitespace trimmed — AC from Python test)
- [ ] `PrintURL("")` returns error satisfying `errors.Is(err, ErrValidation)` (empty UUID validation)
- [ ] `JSON(ctx, receiptUUID string) (json.RawMessage, error)` GETs `/receipt/{inn}/{uuid}/json` (no /v1/); trims UUID; validates non-empty; decodes response body as `json.RawMessage`
- [ ] `Receipt()` factory on Client returns `ErrNotAuthenticated` error when `c.inn == ""` (AC-12)
- [ ] Unit tests in `receipt_test.go` via httptest.Server: PrintURL composition (AC-11), PrintURL whitespace trimming, PrintURL empty UUID → ErrValidation, JSON success, JSON empty UUID → error, Receipt() without auth → ErrNotAuthenticated (AC-12)
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `receipt.go` with `package nalogo`
- [ ] Define `Receipt struct { client *Client; inn string }`
- [ ] Implement `PrintURL(receiptUUID string) (string, error)`: check `r.inn == ""` → ErrNotAuthenticated; trim UUID; check empty → ErrValidation; return `r.client.cfg.baseURL + "/receipt/" + r.inn + "/" + trimmedUUID + "/print"`
- [ ] Implement `JSON(ctx context.Context, receiptUUID string) (json.RawMessage, error)`: trim UUID; check empty → ErrValidation; call `c.client.do(ctx, "GET", "/receipt/"+r.inn+"/"+trimmedUUID+"/json", nil)`; decode body as json.RawMessage
- [ ] Create `receipt_test.go`; test all criteria above using httptest.Server

#### Blockers

- Step 8 (Client must exist and expose baseURL)

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Using /v1/receipt/... path instead of /receipt/... | High | Medium | Explicitly note: receipt paths have NO /v1/ prefix per architecture spec |

#### Definition of Done

- [ ] `receipt.go` exists with Receipt struct, PrintURL, JSON
- [ ] Unit tests written and `go test ./...` exits 0

---

### Step 12: user.go — User profile API

**Goal**: Implement the `User` type with the single `Get` method that fetches the authenticated user's profile. This is the simplest API module.

**Complexity**: Small
**Uncertainty**: Low
**Dependencies**: Step 8 (Client)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/user.go`

#### Success Criteria

- [ ] `User` struct holds `client *Client`
- [ ] `Get(ctx context.Context) (*UserResponse, error)` GETs `/v1/user`; decodes response into `*UserResponse`
- [ ] `UserResponse` struct has at minimum: `ID int64 \`json:"id"\``, `INN string \`json:"inn"\``, `DisplayName string \`json:"displayName"\``, `Email string \`json:"email"\``, `Phone string \`json:"phone"\`` (additional fields accepted as `json:",omitempty"`)
- [ ] Unit test in `user_test.go` via httptest.Server: successful Get returns populated UserResponse
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `user.go` with `package nalogo`
- [ ] Define `UserResponse` struct with INN, DisplayName, Email, Phone, ID fields and JSON tags
- [ ] Define `User struct { client *Client }`
- [ ] Implement `Get(ctx) (*UserResponse, error)`: call `c.client.do`; decode JSON; return
- [ ] Create `user_test.go`; load `testdata/user.json` fixture; verify UserResponse.INN populated

#### Blockers

- Step 8

#### Risks

None — trivial GET wrapper.

#### Definition of Done

- [ ] `user.go` exists with User type and Get
- [ ] Unit test written and `go test ./...` exits 0

---

### Step 13: payment.go — PaymentType API

**Goal**: Implement `PaymentType` with `Table` (GET all) and `Favorite` (client-side filter for `favorite:true`). `Favorite` mirrors Python's `next((pt for pt in payment_types if pt.get("favorite")), None)`.

**Complexity**: Small
**Uncertainty**: Low
**Dependencies**: Step 8 (Client)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/payment.go`

#### Success Criteria

- [ ] `PaymentType` struct holds `client *Client`
- [ ] `Table(ctx) ([]*PaymentTypeEntry, error)` GETs `/v1/payment-type/table`; decodes response as `[]*PaymentTypeEntry`
- [ ] `Favorite(ctx) (*PaymentTypeEntry, error)` calls `Table(ctx)`; returns first entry where `entry.Favorite == true`; returns `nil, nil` if none
- [ ] `PaymentTypeEntry` struct has at minimum `Favorite bool \`json:"favorite"\`` (other fields accepted via `map[string]interface{}` or typed)
- [ ] Unit tests in `payment_test.go`: Table returns slice, Favorite returns first favorite entry, Favorite returns nil when none have favorite=true
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `payment.go` with `package nalogo`
- [ ] Define `PaymentTypeEntry` struct with `Favorite bool` and other relevant fields from `testdata/payment_types.json`
- [ ] Define `PaymentType struct { client *Client }`
- [ ] Implement `Table` and `Favorite`
- [ ] Create `payment_test.go`; load `testdata/payment_types.json` fixture; test both methods

#### Blockers

- Step 8

#### Risks

None.

#### Definition of Done

- [ ] `payment.go` exists with PaymentType, Table, Favorite
- [ ] Unit tests written and `go test ./...` exits 0

---

### Step 14: taxes.go — Tax API

**Goal**: Implement `Tax` with three methods: `Get` (current tax info), `History` (POST with optional OKTMO), `Payments` (POST with optional OKTMO + onlyPaid flag).

**Complexity**: Small
**Uncertainty**: Low
**Dependencies**: Step 8 (Client)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/taxes.go`

#### Success Criteria

- [ ] `Tax` struct holds `client *Client`
- [ ] `Get(ctx) (*TaxResponse, error)` GETs `/v1/taxes`
- [ ] `History(ctx, oktmo *string) (*TaxHistoryResponse, error)` POSTs `{"oktmo":oktmo}` to `/v1/taxes/history` (null oktmo when nil)
- [ ] `Payments(ctx, oktmo *string, onlyPaid bool) (*TaxPaymentsResponse, error)` POSTs `{"oktmo":oktmo,"onlyPaid":onlyPaid}` to `/v1/taxes/payments`
- [ ] Response types `TaxResponse`, `TaxHistoryResponse`, `TaxPaymentsResponse` decode the fixture shapes (may be `json.RawMessage` wrapper if upstream Python uses raw dicts)
- [ ] Unit tests in `taxes_test.go` via httptest.Server: all three methods with fixture JSON
- [ ] `go test ./...` exits 0

#### Subtasks

- [ ] Create `taxes.go` with `package nalogo`
- [ ] Define `TaxResponse`, `TaxHistoryResponse`, `TaxPaymentsResponse` structs (or `json.RawMessage` wrappers matching testdata shapes)
- [ ] Define `Tax struct { client *Client }`
- [ ] Implement `Get`, `History`, `Payments`
- [ ] Create `taxes_test.go`; load fixture files; verify response decoding

#### Blockers

- Step 8

#### Risks

None.

#### Definition of Done

- [ ] `taxes.go` exists with Tax type and three methods
- [ ] Unit tests written and `go test ./...` exits 0

---

### Step 15: testdata/ fixtures and doc.go expansion

**Goal**: Create the ten canonical JSON fixture files grounded in upstream Python test fixture shapes and expand `doc.go` with complete package-level GoDoc and usage examples for `Client`, `Income`, `Receipt`.

**Complexity**: Small
**Uncertainty**: Low
**Dependencies**: Steps 9–14 (all API modules complete — fixture shapes informed by finalized DTOs)

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/testdata/auth_token.json`
- `/Users/aleksei/repos/nalogo-go/testdata/phone_challenge.json`
- `/Users/aleksei/repos/nalogo-go/testdata/income_create.json`
- `/Users/aleksei/repos/nalogo-go/testdata/income_cancel.json`
- `/Users/aleksei/repos/nalogo-go/testdata/receipt_json.json`
- `/Users/aleksei/repos/nalogo-go/testdata/payment_types.json`
- `/Users/aleksei/repos/nalogo-go/testdata/taxes.json`
- `/Users/aleksei/repos/nalogo-go/testdata/taxes_history.json`
- `/Users/aleksei/repos/nalogo-go/testdata/taxes_payments.json`
- `/Users/aleksei/repos/nalogo-go/testdata/user.json`
- `/Users/aleksei/repos/nalogo-go/doc.go` (updated)

#### Success Criteria

- [ ] `testdata/auth_token.json` contains `token`, `refreshToken`, `tokenExpireIn`, `refreshTokenExpiresIn`, `profile.id`, `profile.inn`, `profile.displayName`, `profile.email`, `profile.phone` — matching upstream Python `sample_token_response` fixture
- [ ] `testdata/phone_challenge.json` contains `challengeToken`, `expireDate`, `expireIn` — matching upstream `challenge_response` fixture
- [ ] `testdata/income_create.json` contains `approvedReceiptUuid`
- [ ] `testdata/income_cancel.json` contains `incomeInfo.cancellationInfo.comment` equal to `"Возврат средств"` (UTF-8)
- [ ] `testdata/receipt_json.json` structure matches `receipt_json_response` from Python test
- [ ] `testdata/payment_types.json` is a JSON array with at least one entry containing `"favorite": true` and one with `"favorite": false`
- [ ] `testdata/taxes.json`, `taxes_history.json`, `taxes_payments.json` contain valid JSON matching TaxResponse/TaxHistoryResponse/TaxPaymentsResponse shapes
- [ ] `testdata/user.json` contains `id`, `inn`, `displayName`, `email`, `phone` fields
- [ ] `doc.go` package comment describes the library and contains `Example_client`, `Example_income`, `Example_receipt` testable example functions
- [ ] `go test ./...` exits 0 (examples compile)

#### Subtasks

- [ ] Create `testdata/` directory under `/Users/aleksei/repos/nalogo-go/`
- [ ] Write each of the 10 JSON fixture files using exact field names from upstream Python tests (`sample_token_response`, `challenge_response`, `income_response`, `cancel_response`, `receipt_json_response`) and sensible values for tax/user/payment_types fixtures
- [ ] Update `doc.go`: replace stub comment with full package description; add `func Example()`, `func ExampleIncome_Create()`, `func ExampleReceipt_PrintURL()` with `// Output:` comments where applicable
- [ ] Verify examples compile via `go test ./...`

#### Blockers

- Steps 9–14 (need finalized struct/JSON tag names to write matching fixtures)

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Fixture field names diverge from Go struct JSON tags | Medium | Low | Copy field names directly from struct JSON tags after Steps 9-14 |

#### Definition of Done

- [ ] All 10 fixture files exist and contain valid JSON
- [ ] `doc.go` contains package description and three usage examples
- [ ] `go test ./...` exits 0

---

### Step 16: nalogo_test.go — Hermetic integration tests (all ACs)

**Goal**: Write a single `nalogo_test.go` covering all 20 acceptance criteria using `httptest.NewServer` with fixture-backed handlers. This is the primary quality gate for the entire library.

**Complexity**: Large
**Uncertainty**: Low (all components exist; test structure is mechanical)
**Dependencies**: Steps 9–15

#### Expected Output

- `/Users/aleksei/repos/nalogo-go/nalogo_test.go`

#### Success Criteria

- [ ] `newTestServer(t, routes map[string]http.HandlerFunc) (*httptest.Server, *Client)` helper creates an httptest.Server and a Client pointing at it; used by every test case
- [ ] AC-1: `TestCreateAccessToken_PersistsToken` — loads `auth_token.json`, calls `CreateAccessToken`, verifies token written to FileStore and `inn` set on client
- [ ] AC-2: `TestSMSTwoStepFlow` — loads `phone_challenge.json` + `auth_token.json`, calls `CreatePhoneChallenge` then `CreateAccessTokenByPhone`, verifies token persisted
- [ ] AC-3: `TestRefresh_SingleRetry` — fixture endpoint returns 401 once then 200; verify caller sees 200 after one refresh round-trip
- [ ] AC-4: `TestRefreshSingleFlight_Concurrency` — 20 goroutines fire simultaneously; atomic counter on `/v1/auth/token` handler; assert counter == 1 after all goroutines complete
- [ ] AC-5: `TestRefreshFailure_SurfacesErrUnauthorized` — both income and refresh endpoints return 401; `errors.Is(err, ErrUnauthorized)` true; `errors.As(err, &apiErr)` sets `apiErr.StatusCode = 401`
- [ ] AC-6: `TestDecimalPrecise_TotalAmount` — capture POST body; assert `totalAmount == "65000"` for `{50000,qty=1}` + `{5000,qty=3}`
- [ ] AC-7: `TestCancel_EmptyUUID_PreFlight` — no fixture registered; `errors.Is(Cancel(ctx,""), ErrValidation)` true; server call count == 0
- [ ] AC-8: `TestLegalEntity_MissingINN_PreFlight` — no fixture; `errors.Is(err, ErrValidation)` true; server call count == 0
- [ ] AC-9: `TestNonPositiveAmount_PreFlight` — no fixture; `errors.Is(err, ErrValidation)` true; server call count == 0
- [ ] AC-10: `TestCancelComment_RussianWireValue` — capture POST body; assert `comment == "Возврат средств"` as UTF-8 bytes
- [ ] AC-11: `TestPrintURL_Pure` — server call count == 0 after `PrintURL("uuid-1")`; result equals expected URL
- [ ] AC-12: `TestPrintURL_NotAuthenticated` — unauthenticated client; `errors.Is(err, ErrNotAuthenticated)` true
- [ ] AC-13: `TestErrorHierarchy_AllStatusCodes` — table-driven: fixtures for 400, 401, 403, 404, 406, 422, 500, 418; verify sentinel and `errors.As` for each
- [ ] AC-14: `TestFunctionalOptions_AllApplied` — verify WithBaseURL routes to custom server, WithDeviceID in auth payload, WithHTTPClient transport used, WithTokenStore receives Load/Save, WithLogger receives records, WithTimeout enforced
- [ ] AC-15: `TestContextCancellation` — fixture sleeps 500ms; context deadline 50ms; call returns within 100ms with error wrapping `context.DeadlineExceeded`
- [ ] AC-16: `TestSlogMasking_NoSensitiveLeaks` — attach `slog.NewJSONHandler` to buffer; make requests with token/inn data; verify buffer contains no plaintext values for masked fields
- [ ] AC-17 (coverage): covered by coverage gate step (Step 17)
- [ ] AC-18: `TestModuleIdentity` — read `go.mod`; assert `module github.com/AlexZzz/nalogo` and `go 1.26`
- [ ] AC-19 (hermetic): all tests use httptest.Server and pass with `GOPROXY=off` set in test env
- [ ] AC-20: `TestIdiomaticGoAPI` — static: `Receipt()` returns `(*Receipt, error)`; `CreateAccessToken` first param is `context.Context`; spot-check via reflect or compilation test
- [ ] `go test -race ./...` exits 0

#### Subtasks

- [ ] Create `nalogo_test.go` with `package nalogo_test`
- [ ] Implement `newTestServer` helper function
- [ ] Implement `loadFixture(t, name string) []byte` reading from `testdata/` using `os.ReadFile`
- [ ] Write `TestCreateAccessToken_PersistsToken` (AC-1): use FileStore in t.TempDir(); verify file written
- [ ] Write `TestSMSTwoStepFlow` (AC-2)
- [ ] Write `TestRefresh_SingleRetry` (AC-3): two-request sequence (401 then 200); httptest handler uses `sync/atomic` counter
- [ ] Write `TestRefreshSingleFlight_Concurrency` (AC-4): `t.Parallel()`; 20 goroutines; assert atomic counter == 1
- [ ] Write `TestRefreshFailure_SurfacesErrUnauthorized` (AC-5)
- [ ] Write `TestDecimalPrecise_TotalAmount` (AC-6): capture request body bytes; assert exact string
- [ ] Write `TestCancel_EmptyUUID_PreFlight` (AC-7)
- [ ] Write `TestLegalEntity_MissingINN_PreFlight` (AC-8)
- [ ] Write `TestNonPositiveAmount_PreFlight` (AC-9)
- [ ] Write `TestCancelComment_RussianWireValue` (AC-10): bytes.Contains check
- [ ] Write `TestPrintURL_Pure` (AC-11): counter on server; assert 0 requests
- [ ] Write `TestPrintURL_NotAuthenticated` (AC-12)
- [ ] Write `TestErrorHierarchy_AllStatusCodes` (AC-13): table-driven
- [ ] Write `TestFunctionalOptions_AllApplied` (AC-14)
- [ ] Write `TestContextCancellation` (AC-15)
- [ ] Write `TestSlogMasking_NoSensitiveLeaks` (AC-16)
- [ ] Write `TestModuleIdentity` (AC-18)
- [ ] Run `go test -race ./...` and fix any failures

#### Blockers

- Steps 9–15 (all modules and fixtures must exist)

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Concurrency test (AC-4) is flaky due to goroutine scheduling | Medium | Low | Use sync.WaitGroup with explicit barrier; increase goroutine count to 20 for reliable collision |
| AC-15 timeout test is flaky on slow CI | Low | Low | Use 500ms sleep / 50ms deadline ratio of 10x; assert returns within 200ms |

#### Definition of Done

- [ ] `nalogo_test.go` exists covering all ACs listed above
- [ ] `go test -race ./...` exits 0
- [ ] No test depends on `lknpd.nalog.ru` or any external DNS

---

### Step 17: Coverage gate — verify ≥ 85% and polish

**Goal**: Run the full test suite with coverage profiling; verify the package meets the ≥ 85% target (upstream ~88%); address any uncovered paths; run static analysis.

**Complexity**: Small
**Uncertainty**: Low (if Steps 1-16 followed TDD discipline, coverage target should be met naturally)
**Dependencies**: Step 16

#### Expected Output

- `coverage.out` (ephemeral — not committed)
- Any gap-filling tests added to `nalogo_test.go` or module `_test.go` files

#### Success Criteria

- [ ] `go test -coverprofile=coverage.out ./...` exits 0
- [ ] `go tool cover -func=coverage.out | grep total` reports ≥ 85.0%
- [ ] `go vet ./...` exits 0 with no warnings
- [ ] `staticcheck ./...` exits 0 (or equivalent: `golangci-lint run`)
- [ ] `go test -race ./...` exits 0 (no race conditions)
- [ ] All Definition of Done items from the task-level DoD are met

#### Subtasks

- [ ] Run `make coverage`; capture output
- [ ] Identify uncovered lines via `go tool cover -html=coverage.out`
- [ ] Write targeted tests for any uncovered branches (error paths, edge cases) until ≥ 85%
- [ ] Run `go vet ./...`; fix any reported issues
- [ ] Run `staticcheck ./...` or `golangci-lint run`; fix any issues
- [ ] Final `go test -race ./...` clean run

#### Blockers

- Step 16

#### Risks

| Risk | Impact | Likelihood | Mitigation |
|------|--------|------------|------------|
| Coverage under 85% due to untested error paths in transport | Medium | Low | Step 6 and 16 both include transport error tests; gap analysis in this step catches remainder |

#### Definition of Done

- [ ] `go test -coverprofile=coverage.out ./...` reports ≥ 85.0% total coverage
- [ ] `go vet ./...` clean
- [ ] `go test -race ./...` clean

---

## Implementation Summary

| Step | Goal | Output | Complexity | Dependencies |
|------|------|--------|------------|--------------|
| 1 | go.mod deps + Makefile | `go.mod`, `go.sum`, `Makefile` | S | — |
| 2 | Masking helpers | `masking.go` | S | 1 |
| 3 | Token store interface + impls | `tokenstore.go` | M | 1 |
| 4 | MoneyAmount + AtomTime + DeviceID | `money.go` | S | 1 |
| 5 | Error hierarchy + checkResponse | `errors.go` | M | 1, 2 |
| 6 | authTransport (single-flight refresh) | `transport.go` | M | 3, 5 |
| 7 | Functional options + config | `options.go` | S | 3, 4, 5 |
| 8 | Client constructor + factories | `client.go` | M | 6, 7 |
| 9 | Auth methods + DeviceInfo | `auth.go` | M | 8 |
| 10 | Income API + validation + DTOs | `income.go` | L | 8, 4, 5 |
| 11 | Receipt API (pure + HTTP) | `receipt.go` | S | 8, 5 |
| 12 | User API | `user.go` | S | 8 |
| 13 | PaymentType API | `payment.go` | S | 8 |
| 14 | Tax API | `taxes.go` | S | 8 |
| 15 | 10 JSON fixtures + doc.go expand | `testdata/*.json`, `doc.go` | S | 9–14 |
| 16 | Integration tests (all ACs) | `nalogo_test.go` | L | 9–15 |
| 17 | Coverage gate + static analysis | — | S | 16 |

**Total Steps**: 17
**Critical Path**: 1 → 2 → 5 → 6 → 8 → 9 → 16 → 17 (authTransport refresh logic is longest chain)
**Parallel Opportunities**:
- Steps 2, 3, 4 can all start after Step 1 (no inter-dependencies)
- Steps 6 and 7 can run in parallel after Step 5
- Steps 12, 13, 14 can all run in parallel after Step 8
- Steps 10 and 11 can run in parallel after Step 8

---

## Risks and Blockers Summary

### High Priority

| Risk / Blocker | Impact | Likelihood | Mitigation |
|----------------|--------|------------|------------|
| authTransport double-check comparison bug (pointer vs string) | High | Medium | Compare `latest.Token != curToken` strings; unit test with 20-goroutine race (AC-4) |
| http.Request.Body consumed before retry in authTransport | High | Medium | Use `GetBody` callback or buffer body before RoundTrip; document constraint |
| MoneyAmount.MarshalJSON produces unquoted number | High | Medium | Unit test in Step 4: json.Marshal(MoneyAmount{100.50}) must produce `"100.50"` with surrounding quotes |
| AtomTime format uses +00:00 instead of Z | High | Low | Pin test to known UTC time; assert exact string |
| Receipt path includes /v1/ prefix (should be absent) | High | Medium | Explicitly test URL composition in Step 11; note deviation from other API paths |
| Decimal totalAmount loses precision | High | Low | Never convert to float64; test AC-6 case explicitly |

### Medium Priority

| Risk / Blocker | Impact | Likelihood | Mitigation |
|----------------|--------|------------|------------|
| shopspring/decimal v1.4.0 not tagged | Low | Low | Use latest stable (v1.3.1 fallback) |
| Coverage under 85% | Medium | Low | TDD discipline in steps 2-14 + gap analysis in step 17 |
| Concurrency test flakiness | Low | Low | 20-goroutine barrier + atomic counter is reliable at this scale |

---

## Definition of Done (Task Level)

- [ ] All 17 implementation steps completed
- [ ] All 20 acceptance criteria (AC-1 through AC-20) verified by tests
- [ ] `go test -coverprofile=coverage.out ./...` reports ≥ 85.0%
- [ ] `go test -race ./...` exits 0 (no race detector warnings)
- [ ] `go vet ./...` exits 0
- [ ] `staticcheck ./...` (or `golangci-lint run`) exits 0
- [ ] `go.mod` declares `module github.com/AlexZzz/nalogo`, `go 1.26`
- [ ] `LICENSE` is MIT
- [ ] `doc.go` contains package-level GoDoc and usage examples for `Client`, `Income`, `Receipt`
- [ ] No test depends on `lknpd.nalog.ru` or any external DNS
- [ ] No CLI / demo / web artifact committed to repository
- [ ] `go build ./...` exits 0
