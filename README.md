# nalogo-go

[![Tests](https://github.com/AlexZzz/nalogo-go/actions/workflows/ci.yml/badge.svg)](https://github.com/AlexZzz/nalogo-go/actions/workflows/ci.yml)
[![Go Version](https://img.shields.io/badge/go-1.26+-00ADD8.svg)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Report Card](https://goreportcard.com/badge/github.com/AlexZzz/nalogo-go)](https://goreportcard.com/report/github.com/AlexZzz/nalogo-go)
[![Coverage](https://img.shields.io/badge/coverage-%E2%89%A585%25-brightgreen.svg)](https://github.com/AlexZzz/nalogo-go/actions/workflows/ci.yml)

Go-клиент для API ФНС «Мой налог» (lknpd.nalog.ru) — сервис для самозанятых.

Порт Python-библиотеки [rusik636/nalogo](https://github.com/rusik636/nalogo) с полным покрытием функциональности.

## Возможности

- Авторизация по ИНН + пароль
- Авторизация по SMS (challenge → verify)
- Автоматическое обновление access-токена по refresh-токену (single-flight, thread-safe)
- Регистрация дохода: одна позиция и несколько позиций
- Аннулирование чека
- Получение чека: JSON и URL печати
- Профиль пользователя
- Способы оплаты
- История начислений налогов

## Установка

```
go get github.com/AlexZzz/nalogo-go
```

## Быстрый старт

```go
import "github.com/AlexZzz/nalogo-go"

ctx := context.Background()

client := nalogo.New(
    nalogo.WithTokenStore(nalogo.NewFileStore("token.json")),
)

// Авторизация
_, err := client.CreateAccessToken(ctx, "123456789012", "password")

// Зарегистрировать доход
resp, err := client.Income().Create(ctx, "Консультация", nalogo.MustMoneyAmount("5000"), nalogo.MustMoneyAmount("1"))
fmt.Println(resp.ApprovedReceiptUUID)

// Аннулировать чек
_, err = client.Income().Cancel(ctx, resp.ApprovedReceiptUUID, nalogo.CancelCommentRefund)

// URL для печати чека
url, err := client.Receipt().PrintURL(resp.ApprovedReceiptUUID)
```

## Авторизация

### ИНН + пароль

```go
tokenJSON, err := client.CreateAccessToken(ctx, inn, password)
```

### SMS

```go
challenge, err := client.CreatePhoneChallenge(ctx, "+79991234567")
// пользователь вводит код из SMS
resp, err := client.CreateAccessTokenByPhone(ctx, "+79991234567", challenge.ChallengeToken, "123456")
```

### Восстановление сессии из сохранённого токена

```go
err := client.Authenticate(ctx, savedTokenJSON)
```

## Несколько позиций в чеке

```go
resp, err := client.Income().CreateMultipleItems(ctx,
    []nalogo.IncomeServiceItem{
        {Name: "Разработка", Amount: nalogo.MustMoneyAmount("10000"), Quantity: nalogo.MustMoneyAmount("1")},
        {Name: "Консультация", Amount: nalogo.MustMoneyAmount("2000"), Quantity: nalogo.MustMoneyAmount("2")},
    },
    nalogo.AtomTimeNow(),
    nil, // nil = физическое лицо
)
```

### Юридическое лицо или ИП

```go
inn := "7707083893"
name := "ООО Ромашка"
resp, err := client.Income().CreateMultipleItems(ctx, items, nalogo.AtomTimeNow(),
    &nalogo.IncomeClientInfo{
        IncomeType:  nalogo.IncomeTypeFromLegalEntity,
        INN:         &inn,
        DisplayName: &name,
    },
)
```

## Хранение токена

По умолчанию токен хранится в памяти (`MemoryStore`). Для сохранения между запусками:

```go
// Файл (права 0600, создаётся автоматически)
store := nalogo.NewFileStore("/var/lib/myapp/nalog-token.json")
client := nalogo.New(nalogo.WithTokenStore(store))
```

Можно реализовать собственное хранилище, удовлетворив интерфейс `TokenStore`:

```go
type TokenStore interface {
    Save(ctx context.Context, td *TokenData) error
    Load(ctx context.Context) (*TokenData, error)
    Clear(ctx context.Context) error
}
```

## Опции клиента

| Опция | По умолчанию | Описание |
|---|---|---|
| `WithBaseURL(url)` | `https://lknpd.nalog.ru/api` | Базовый URL API |
| `WithTimeout(d)` | 10s | Таймаут HTTP-запросов |
| `WithDeviceID(id)` | случайный UUID-21 | Идентификатор устройства |
| `WithTokenStore(s)` | `MemoryStore` | Хранилище токена |
| `WithHTTPClient(c)` | `http.DefaultTransport` | HTTP-транспорт (для тестов/прокси) |
| `WithLogger(l)` | `slog.Default()` | Логгер |

## Обработка ошибок

Все ошибки API оборачивают sentinel-ошибки, совместимые с `errors.Is`:

```go
var apiErr *nalogo.APIError
if errors.As(err, &apiErr) {
    fmt.Println(apiErr.StatusCode, apiErr.Body)
}

if errors.Is(err, nalogo.ErrUnauthorized) { /* 401 */ }
if errors.Is(err, nalogo.ErrNotAuthenticated) { /* токен не установлен */ }
if errors.Is(err, nalogo.ErrValidation) { /* неверные аргументы */ }
if errors.Is(err, nalogo.ErrDomain) { /* любая ошибка nalogo */ }
```

## Запуск тестов

```
make test          # все тесты
make coverage      # покрытие (≥85%)
make lint          # go vet
```

## Замечания

- API «Мой налог» неофициальное (получено реверсом), может меняться без предупреждения.
- Чувствительные поля (токены, пароли) автоматически маскируются в логах (`***`).

## License

MIT.
