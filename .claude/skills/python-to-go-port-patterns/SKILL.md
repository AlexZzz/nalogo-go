---
name: Python-to-Go Port Patterns (nalogo)
description: Idiomatic Go patterns for porting the rusik636/nalogo Python async library (httpx + Pydantic v2) to a synchronous Go 1.26 library (github.com/AlexZzz/nalogo).
topics: go, python-to-go, pydantic, net/http, errors, slog, decimal, token-store, functional-options, httptest
created: 2026-05-05
updated: 2026-05-05
scratchpad: .specs/scratchpad/61ce35b5.md
---

# Python-to-Go Port Patterns (nalogo)

## Overview

This skill covers idiomatic Go translations for the rusik636/nalogo Python library — an async httpx+Pydantic v2 client for the Russian "Moy Nalog" self-employed tax API (lknpd.nalog.ru). It addresses ten concrete design questions grounded in the actual upstream source at `/Users/aleksei/repos/nalogo-upstream/`.

---

## Key Concepts

- **No async/await**: Python asyncio → synchronous Go; concurrency available via goroutines by caller choice
- **Pydantic v2 → tagged structs**: `Field(alias=...)` → `json:"camelCase"`, validators → method-level checks
- **httpx.AsyncClient → net/http + RoundTripper**: one long-lived `*http.Client` with custom transport for auth
- **Exception hierarchy → sentinel errors + typed struct**: `errors.Is/As` replaces `isinstance`
- **TokenStore interface**: replaces Python's `storage_path: str|None` optional file path
- **shopspring/decimal**: exact replacement for Python's `decimal.Decimal` for monetary fields

---

## Documentation & References

| Resource | Description | Link |
|----------|-------------|------|
| Go encoding/json | Struct tags, custom marshaling | https://pkg.go.dev/encoding/json |
| net/http RoundTripper | Transport middleware pattern | https://pkg.go.dev/net/http#RoundTripper |
| errors pkg | errors.Is, errors.As, %w wrapping | https://pkg.go.dev/errors |
| log/slog | Structured logging, LogValuer | https://pkg.go.dev/log/slog |
| shopspring/decimal | Arbitrary-precision decimal for Go | https://github.com/shopspring/decimal |
| httptest | Test server for HTTP fixture replay | https://pkg.go.dev/net/http/httptest |
| go-playground/validator | Struct validation tags (NOT recommended here) | https://github.com/go-playground/validator |

---

## Recommended Libraries & Tools

| Name | Purpose | Maturity | Notes |
|------|---------|----------|-------|
| shopspring/decimal v1.4.0 | Monetary decimal arithmetic | Stable | MIT; implements json.Marshaler but outputs unquoted — wrap for string output |
| net/http (stdlib) | HTTP client | Stable | Use RoundTripper for auth middleware |
| log/slog (stdlib Go 1.21+) | Structured logging | Stable | LogValuer for masked types |
| encoding/json (stdlib) | JSON encode/decode | Stable | Custom MarshalJSON for AtomTime, MoneyAmount |
| httptest (stdlib) | Integration test HTTP server | Stable | Serves testdata/ JSON fixtures |

### Not Recommended

- **go-playground/validator**: reflection overhead, struct-tag syntax unfamiliar to API consumers; upstream has only 4 field validators — hand-roll them at method call boundaries instead.
- **sql.NullString / sql.NullBool**: serialize as `{"String":"","Valid":false}` — wrong JSON shape for this API.

---

## Patterns & Best Practices

### Pattern 1: Pydantic Field(alias) → json struct tags + pointer optionals

**When to use**: All request/response DTOs and domain structs.

**Rule**: Use `*T` (pointer) for any Python field typed `T | None`. Use `json:"omitempty"` only on request structs where the upstream Python passes `None` to omit the key; on response structs, keep fields without `omitempty` so missing JSON keys decode to nil naturally.

**Special case — mixed type** (`register_available: bool|str|None`): use `json.RawMessage`.

```go
// upstream: register_available: bool | str | None = Field(None, alias="registerAvailable")
RegisterAvailable json.RawMessage `json:"registerAvailable"`

// upstream: last_name: str | None = Field(None, alias="lastName")
LastName *string `json:"lastName"`

// upstream: id: int = Field(...) — required, non-optional
ID int `json:"id"`
```

**AtomDateTime** — upstream serializes as ISO-8601 with milliseconds and literal "Z":

```go
type AtomTime struct{ time.Time }

func (t AtomTime) MarshalJSON() ([]byte, error) {
    return []byte(`"` + t.UTC().Format("2006-01-02T15:04:05.000Z") + `"`), nil
}

func AtomTimeNow() AtomTime { return AtomTime{time.Now().UTC()} }
```

**Validation** — hand-rolled at method boundary, not struct tags:

```go
func validateINN(inn string) error {
    if inn == "" { return fmt.Errorf("INN cannot be empty") }
    if !isDigits(inn) { return fmt.Errorf("INN must contain only digits") }
    if len(inn) != 10 && len(inn) != 12 { return fmt.Errorf("INN must be 10 or 12 digits") }
    return nil
}
```

---

### Pattern 2: Exception hierarchy → sentinel errors + typed APIError

**When to use**: All HTTP response error handling; callers check with `errors.Is`.

**Trade-offs**: Typed struct gives callers access to `StatusCode` and `Body`; sentinel vars enable `errors.Is` without type assertions in common cases.

```go
// errors.go
var (
    ErrDomain       = errors.New("nalogo: domain error")
    ErrValidation   = fmt.Errorf("%w: validation (400)", ErrDomain)
    ErrUnauthorized = fmt.Errorf("%w: unauthorized (401)", ErrDomain)
    ErrForbidden    = fmt.Errorf("%w: forbidden (403)", ErrDomain)
    ErrNotFound     = fmt.Errorf("%w: not found (404)", ErrDomain)
    ErrClient       = fmt.Errorf("%w: client error (406)", ErrDomain)
    ErrPhone        = fmt.Errorf("%w: phone error (422)", ErrDomain)
    ErrServer       = fmt.Errorf("%w: server error (500)", ErrDomain)
    ErrUnknown      = fmt.Errorf("%w: unknown error", ErrDomain)
)

type APIError struct {
    Sentinel   error
    StatusCode int
    Body       string // sanitized (tokens masked)
}

func (e *APIError) Error() string { return fmt.Sprintf("%v: status=%d body=%s", e.Sentinel, e.StatusCode, e.Body) }
func (e *APIError) Is(target error) bool { return target == e.Sentinel || target == ErrDomain }
func (e *APIError) Unwrap() error { return e.Sentinel }

// checkResponse mirrors upstream raise_for_status()
func checkResponse(resp *http.Response) error {
    if resp.StatusCode < 400 { return nil }
    body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
    safe := sanitizeBody(string(body))
    sentinel := statusToSentinel(resp.StatusCode)
    return &APIError{Sentinel: sentinel, StatusCode: resp.StatusCode, Body: safe}
}
```

Caller pattern:
```go
if errors.Is(err, nalogo.ErrUnauthorized) { /* re-authenticate */ }
var apiErr *nalogo.APIError
if errors.As(err, &apiErr) { log.Println(apiErr.StatusCode) }
```

---

### Pattern 3: HTTP transport — RoundTripper for auth + 401 refresh

**When to use**: Main API client. Auth endpoints (login, refresh) use a plain `*http.Client` without the transport — mirrors upstream's separate `httpx.AsyncClient` for auth calls.

**Trade-offs**: RoundTripper is more composable than a wrapper struct with explicit retry logic. sync.Mutex (not sync/atomic) is correct because refresh requires both a read (get refreshToken) and a write (store new token) as an atomic unit.

```go
type HTTPDoer interface {
    Do(*http.Request) (*http.Response, error)
}

type authTransport struct {
    base      http.RoundTripper // wraps http.DefaultTransport
    store     TokenStore
    mu        sync.Mutex
    authClient *http.Client    // plain client for refresh calls
    baseURL   string
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
    // 1. Inject Bearer from store
    tok, _ := t.store.Load(req.Context())
    if tok != nil && tok.Token != "" {
        req = req.Clone(req.Context())
        req.Header.Set("Authorization", "Bearer "+tok.Token)
    }

    resp, err := t.base.RoundTrip(req)
    if err != nil { return nil, err }

    // 2. On 401: refresh once (mutex prevents concurrent refresh storms)
    if resp.StatusCode == http.StatusUnauthorized {
        resp.Body.Close()
        if newTok := t.refreshToken(req.Context(), tok); newTok != nil {
            req = req.Clone(req.Context())
            req.Header.Set("Authorization", "Bearer "+newTok.Token)
            return t.base.RoundTrip(req)
        }
    }
    return resp, nil
}

func (t *authTransport) refreshToken(ctx context.Context, cur *TokenData) *TokenData {
    t.mu.Lock()
    defer t.mu.Unlock()
    // check store wasn't already refreshed by another goroutine
    if cur == nil || cur.RefreshToken == "" { return nil }
    // POST /auth/token with plain authClient
    // update store on success
    // return new TokenData or nil
    return nil // stub
}
```

---

### Pattern 4: Functional options for client constructor

**When to use**: `nalogo.New(opts ...Option)` — the public entry point.

```go
type Option func(*config)

type config struct {
    baseURL    string
    timeout    time.Duration
    deviceID   string
    store      TokenStore
    httpClient HTTPDoer // for testing injection
}

func WithBaseURL(u string) Option        { return func(c *config) { c.baseURL = u } }
func WithTimeout(d time.Duration) Option { return func(c *config) { c.timeout = d } }
func WithDeviceID(id string) Option      { return func(c *config) { c.deviceID = id } }
func WithTokenStore(s TokenStore) Option { return func(c *config) { c.store = s } }
func WithHTTPClient(h HTTPDoer) Option   { return func(c *config) { c.httpClient = h } }

func New(opts ...Option) *Client {
    cfg := &config{
        baseURL:  "https://lknpd.nalog.ru/api",
        timeout:  10 * time.Second,
        deviceID: generateDeviceID(),
        store:    &MemoryStore{},
    }
    for _, o := range opts { o(cfg) }
    // build internal *http.Client with authTransport
    return &Client{cfg: cfg, /* ... */}
}
```

---

### Pattern 5: context.Context first-arg convention

**When to use**: Every exported method that touches the network.

```go
// All network methods
func (c *Client) CreateAccessToken(ctx context.Context, inn, password string) (*TokenData, error)
func (c *Client) CreatePhoneChallenge(ctx context.Context, phone string) (*ChallengeResponse, error)
func (i *Income) Create(ctx context.Context, name string, amount, quantity decimal.Decimal) (*IncomeResponse, error)
func (r *Receipt) JSON(ctx context.Context, receiptUUID string) (json.RawMessage, error)
func (t *Tax) History(ctx context.Context, oktmo *string) (*TaxHistoryResponse, error)

// Pure methods (no network) do NOT take context
func (r *Receipt) PrintURL(receiptUUID string) (string, error)
```

All HTTP requests:
```go
req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
// NEVER: http.NewRequest(...)
```

---

### Pattern 6: slog sensitive-field masking

**When to use**: Error logging in `checkResponse()` and `authTransport`. Normal-flow logging of sensitive values (INN, token).

**Strategy A — sanitize helpers** (for error path logging):
```go
var bodyMaskREs = []*regexp.Regexp{
    regexp.MustCompile(`("token":\s*")[^"]*(")`),
    regexp.MustCompile(`("refreshToken":\s*")[^"]*(")`),
    regexp.MustCompile(`("password":\s*")[^"]*(")`),
    regexp.MustCompile(`("secret":\s*")[^"]*(")`),
}
var urlMaskREs = []*regexp.Regexp{
    regexp.MustCompile(`(token=)[^&]*`),
    regexp.MustCompile(`(key=)[^&]*`),
    regexp.MustCompile(`(secret=)[^&]*`),
}

func sanitizeBody(s string) string {
    for _, re := range bodyMaskREs { s = re.ReplaceAllString(s, "${1}***${2}") }
    return s
}

var sensitiveHeaders = map[string]bool{
    "authorization": true, "x-api-key": true, "cookie": true, "set-cookie": true,
}
func sanitizeHeaders(h http.Header) map[string]string {
    out := make(map[string]string, len(h))
    for k, v := range h {
        if sensitiveHeaders[strings.ToLower(k)] { out[k] = "***" } else { out[k] = strings.Join(v, ",") }
    }
    return out
}
```

**Strategy B — slog.LogValuer** (for structured logging of sensitive domain values):
```go
type MaskedString string
func (m MaskedString) LogValue() slog.Value { return slog.StringValue("***") }

// Usage:
slog.Info("authenticating", "inn", MaskedString(inn))
// Logs: inn=***
```

Use Strategy A in error structs (called on every error), Strategy B for deliberate log statements.

---

### Pattern 7: TokenStore interface

**When to use**: All token persistence. Default `MemoryStore` (zero config), optional `FileStore` (matches upstream `storage_path`).

```go
type TokenData struct {
    Token                 string       `json:"token"`
    RefreshToken          string       `json:"refreshToken"`
    TokenExpireIn         string       `json:"tokenExpireIn"`
    RefreshTokenExpiresIn *string      `json:"refreshTokenExpiresIn"`
    Profile               *UserProfile `json:"profile,omitempty"`
}

type UserProfile struct {
    ID          int    `json:"id"`
    INN         string `json:"inn"`
    DisplayName string `json:"displayName"`
    Email       string `json:"email,omitempty"`
    Phone       string `json:"phone"`
}

type TokenStore interface {
    Save(ctx context.Context, t *TokenData) error
    Load(ctx context.Context) (*TokenData, error)
    Clear(ctx context.Context) error
}

// MemoryStore — default, not persisted
type MemoryStore struct { mu sync.RWMutex; data *TokenData }

func (m *MemoryStore) Save(_ context.Context, t *TokenData) error {
    m.mu.Lock(); defer m.mu.Unlock(); m.data = t; return nil
}
func (m *MemoryStore) Load(_ context.Context) (*TokenData, error) {
    m.mu.RLock(); defer m.mu.RUnlock(); return m.data, nil
}
func (m *MemoryStore) Clear(_ context.Context) error {
    m.mu.Lock(); defer m.mu.Unlock(); m.data = nil; return nil
}

// FileStore — matches upstream storage_path behavior
type FileStore struct{ path string }

func NewFileStore(path string) *FileStore { return &FileStore{path: path} }

func (f *FileStore) Save(_ context.Context, t *TokenData) error {
    if err := os.MkdirAll(filepath.Dir(f.path), 0700); err != nil { return err }
    b, err := json.MarshalIndent(t, "", "  ")
    if err != nil { return err }
    return os.WriteFile(f.path, b, 0600)
}
func (f *FileStore) Load(_ context.Context) (*TokenData, error) {
    b, err := os.ReadFile(f.path)
    if errors.Is(err, os.ErrNotExist) { return nil, nil } // silently absent
    if err != nil { return nil, nil }                      // silently ignore, like upstream
    var t TokenData
    if err := json.Unmarshal(b, &t); err != nil { return nil, nil } // silently ignore
    return &t, nil
}
func (f *FileStore) Clear(_ context.Context) error { return os.Remove(f.path) }
```

---

### Pattern 8: Package layout — single `nalogo` package

**Decision**: Single package `nalogo` (monopkg). Sub-packages are not recommended.

**Rationale**:
- Upstream has ~10 source files; sub-packages add import friction without isolation benefit
- All API sub-modules (Income, Receipt, Tax, etc.) are accessed via `Client` factory methods — the facade pattern already organizes the API surface
- `Receipt` needs `INN` from `TokenData` (an auth concern); `Income` returns `approvedReceiptUuid` used by `Receipt` — shared types would cross sub-package boundaries constantly
- Go convention (net/http, database/sql) is one large package, not micro-packages per concern

**File organization within `nalogo/`**:
```
nalogo/
  client.go        // Client type, New(), Income()/Receipt()/Tax()/User()/PaymentType() factories
  auth.go          // CreateAccessToken, CreatePhoneChallenge, CreateAccessTokenByPhone, Authenticate
  income.go        // Income type, Create, CreateMultipleItems, Cancel
  receipt.go       // Receipt type, PrintURL (pure), JSON (HTTP)
  tax.go           // Tax type, Get, History, Payments
  user.go          // User type, Get
  payment_type.go  // PaymentType type, Table, Favorite
  errors.go        // APIError, Err* sentinels, checkResponse(), sanitize helpers
  token.go         // TokenData, TokenStore interface, MemoryStore, FileStore
  dto.go           // All request/response structs, enums (IncomeType, PaymentType, CancelCommentType)
  http.go          // authTransport, HTTPDoer interface, default headers, device info
  log.go           // MaskedString, sanitizeBody, sanitizeHeaders, sanitizeURL
  nalogo_test.go   // package-level integration tests
  testdata/        // JSON fixtures
```

---

### Pattern 9: Test fixtures with httptest.Server

**When to use**: All integration tests. No live API calls.

**File structure**:
```
testdata/
  auth_token.json          // {token, refreshToken, tokenExpireIn, profile:{inn,...}}
  phone_challenge.json     // {challengeToken, expireDate, expireIn}
  income_create.json       // {approvedReceiptUuid}
  income_cancel.json       // cancellation response
  receipt_json.json        // receipt detail
  payment_types.json       // []PaymentType
  taxes.json               // current tax info
  taxes_history.json       // history records
  taxes_payments.json      // payment records
  user.json                // UserType response
```

**Test server helper**:
```go
func mustReadFixture(t *testing.T, name string) []byte {
    t.Helper()
    data, err := os.ReadFile(filepath.Join("testdata", name))
    if err != nil { t.Fatalf("fixture %s: %v", name, err) }
    return data
}

func fixtureHandler(t *testing.T, fixture string) http.HandlerFunc {
    data := mustReadFixture(t, fixture)
    return func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        w.Write(data)
    }
}

func newTestServer(t *testing.T) *httptest.Server {
    t.Helper()
    mux := http.NewServeMux()
    mux.HandleFunc("POST /api/v1/auth/lkfl", fixtureHandler(t, "auth_token.json"))
    mux.HandleFunc("POST /api/v2/auth/challenge/sms/start", fixtureHandler(t, "phone_challenge.json"))
    mux.HandleFunc("POST /api/v1/auth/challenge/sms/verify", fixtureHandler(t, "auth_token.json"))
    mux.HandleFunc("POST /api/v1/auth/token", fixtureHandler(t, "auth_token.json"))
    mux.HandleFunc("POST /api/v1/income", fixtureHandler(t, "income_create.json"))
    mux.HandleFunc("POST /api/v1/cancel", fixtureHandler(t, "income_cancel.json"))
    mux.HandleFunc("GET /api/v1/user", fixtureHandler(t, "user.json"))
    // ...
    srv := httptest.NewServer(mux)
    t.Cleanup(srv.Close)
    return srv
}

// Usage in test:
func TestIncomeCreate(t *testing.T) {
    srv := newTestServer(t)
    client := nalogo.New(nalogo.WithBaseURL(srv.URL+"/api"))
    // authenticate, then call income...
}
```

**401 refresh test** — use a stateful handler that returns 401 first, then 200:
```go
attempts := 0
mux.HandleFunc("POST /api/v1/income", func(w http.ResponseWriter, r *http.Request) {
    attempts++
    if attempts == 1 { w.WriteHeader(http.StatusUnauthorized); return }
    fixtureHandler(t, "income_create.json")(w, r)
})
```

---

### Pattern 10: Decimal arithmetic with shopspring/decimal

**When to use**: All monetary fields (amount, quantity, totalAmount). Never use float64 for money.

**Installation**:
```bash
go get github.com/shopspring/decimal@v1.4.0
```

**Force string JSON output** (upstream serializes amounts as strings, not numbers):

```go
import "github.com/shopspring/decimal"

// shopspring's default JSON marshaling outputs unquoted number: 100.00
// Upstream outputs quoted string: "100.00"
// Wrap to override:

type MoneyAmount struct{ decimal.Decimal }

func (m MoneyAmount) MarshalJSON() ([]byte, error) {
    return []byte(`"` + m.String() + `"`), nil
}
func (m *MoneyAmount) UnmarshalJSON(b []byte) error {
    s := strings.Trim(string(b), `"`)
    d, err := decimal.NewFromString(s)
    if err != nil { return err }
    m.Decimal = d
    return nil
}

// IncomeServiceItem
type IncomeServiceItem struct {
    Name     string      `json:"name"`
    Amount   MoneyAmount `json:"amount"`
    Quantity MoneyAmount `json:"quantity"`
}

func (i IncomeServiceItem) TotalAmount() decimal.Decimal {
    return i.Amount.Mul(i.Quantity.Decimal)
}

// Sum across items (mirrors Python sum(item.get_total_amount() for item in services))
func totalAmount(items []IncomeServiceItem) decimal.Decimal {
    total := decimal.Zero
    for _, it := range items { total = total.Add(it.TotalAmount()) }
    return total
}
```

**DeviceID generation** — mirrors Python `str(uuid.uuid4()).replace("-", "")[:21].lower()`:
```go
func generateDeviceID() string {
    id := strings.ReplaceAll(uuid.New().String(), "-", "")
    if len(id) > 21 { id = id[:21] }
    return strings.ToLower(id)
}
// go get github.com/google/uuid@v1.6.0
```

---

## Upstream API Endpoint Reference

| Method | Path | Auth | Notes |
|--------|------|------|-------|
| POST | /api/v1/auth/lkfl | No | INN+password login |
| POST | /api/v2/auth/challenge/sms/start | No | Phone challenge start |
| POST | /api/v1/auth/challenge/sms/verify | No | Phone+SMS verify |
| POST | /api/v1/auth/token | No | Token refresh |
| POST | /api/v1/income | Bearer | Create income |
| POST | /api/v1/cancel | Bearer | Cancel receipt |
| GET | /api/v1/user | Bearer | User profile |
| GET | /api/v1/payment-type/table | Bearer | Payment types |
| GET | /api/v1/taxes | Bearer | Current tax |
| POST | /api/v1/taxes/history | Bearer | Tax history |
| POST | /api/v1/taxes/payments | Bearer | Tax payments |
| GET | /receipt/{inn}/{uuid}/json | Bearer | Receipt JSON |
| — | /receipt/{inn}/{uuid}/print | — | Receipt print URL (pure, no HTTP) |

---

## Common Pitfalls & Solutions

| Issue | Impact | Solution |
|-------|--------|----------|
| float64 for monetary amounts | High — precision loss (0.1+0.2≠0.3) | Use shopspring/decimal with MoneyAmount wrapper |
| http.NewRequest without context | Medium — request cannot be cancelled | Always http.NewRequestWithContext |
| Sharing *http.Client for auth + API requests | Medium — auth transport intercepts login call, infinite loop | Use separate plain http.Client for auth endpoints |
| json:"omitempty" on response structs | Medium — hides null API fields | Omit omitempty on response types; use only on request types |
| Concurrent token refresh without mutex | High — refresh stampede on parallel 401s | Lock before check-then-refresh-then-store |
| sql.Null* for optional API fields | High — wrong JSON shape (object instead of null/string) | Use pointer types (*string, *bool, *time.Time) |
| shopspring default JSON (unquoted) vs upstream (quoted string) | High — API rejects unquoted decimal | Use MoneyAmount wrapper with custom MarshalJSON |
| AtomDateTime "+00:00" suffix | Medium — upstream always uses "Z" | Hardcode "Z" in format string, not time.RFC3339 |

---

## Recommendations

1. **Single nalogo package, ~12 source files**: matches upstream organization, avoids cross-package type dependencies
2. **Pointer types for all optional fields**: `*string`, `*bool`, `*time.Time` — nil = JSON null = Python None
3. **RoundTripper auth middleware**: composable, testable via `WithHTTPClient(mock)` option
4. **Hand-rolled field validators**: 4 validators in upstream don't justify go-playground/validator dependency
5. **MoneyAmount wrapper over shopspring/decimal**: forces string JSON output to match API wire format
6. **FileStore + MemoryStore for TokenStore**: FileStore at optional path, MemoryStore default (like upstream's `storage_path=None`)
7. **testdata/ JSON fixtures + httptest.Server**: no live API calls in tests; stateful handler for 401 refresh test
8. **MaskedString slog.LogValuer**: zero-cost masking for structured log fields containing INN/token
9. **AtomTime wrapper**: dedicated type for the "Z"-suffix datetime format; do not use time.RFC3339 (outputs "+00:00")
10. **Separate plain http.Client for auth endpoints**: prevents auth transport from intercepting its own login/refresh calls

---

## Sources & Verification

| Source | Type | Last Verified |
|--------|------|---------------|
| /Users/aleksei/repos/nalogo-upstream/nalogo/*.py | Primary (upstream source) | 2026-05-05 |
| /Users/aleksei/repos/nalogo-upstream/nalogo/dto/*.py | Primary (upstream source) | 2026-05-05 |
| /Users/aleksei/repos/nalogo-upstream/tests/*.py | Primary (upstream tests) | 2026-05-05 |
| https://pkg.go.dev/net/http | Official Go docs | 2026-05-05 |
| https://pkg.go.dev/encoding/json | Official Go docs | 2026-05-05 |
| https://github.com/shopspring/decimal | Library source | 2026-05-05 |
| https://pkg.go.dev/log/slog | Official Go docs (Go 1.21+) | 2026-05-05 |

---

## Changelog

| Date | Changes |
|------|---------|
| 2026-05-05 | Initial creation for task: port-nalogo-python-to-go |
