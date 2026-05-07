package nalogo_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/AlexZzz/nalogo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fixture reads a file from testdata/ and returns its bytes.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	require.NoError(t, err, "missing fixture: testdata/%s", name)
	return b
}

// newTestServer creates an httptest.Server that routes requests by method+path
// using a map of "METHOD /path" -> handler func. Returns the server and a
// helper to build a Client pointing at it (with no authTransport — callers that
// need auth-transparent testing can pre-authenticate via Authenticate).
func newTestServer(t *testing.T, routes map[string]http.HandlerFunc) (*httptest.Server, func(...nalogo.Option) *nalogo.Client) {
	t.Helper()
	mux := http.NewServeMux()
	for key, fn := range routes {
		fn := fn
		mux.HandleFunc(key, fn)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	newClient := func(extra ...nalogo.Option) *nalogo.Client {
		opts := append([]nalogo.Option{
			nalogo.WithBaseURL(srv.URL),
			nalogo.WithHTTPClient(srv.Client()),
		}, extra...)
		return nalogo.New(opts...)
	}
	return srv, newClient
}

func jsonHandler(t *testing.T, fixtureName string) http.HandlerFunc {
	t.Helper()
	data := fixture(t, fixtureName)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write(data)
	}
}

// --- Auth tests ---

// AC-1: INN+password authentication stores token and sets INN.
func TestCreateAccessToken_Success(t *testing.T) {
	authData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v1/auth/lkfl": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(authData)
		},
	})

	store := &nalogo.MemoryStore{}
	c := newClient(nalogo.WithTokenStore(store))

	tokenJSON, err := c.CreateAccessToken(context.Background(), "123456789012", "password")
	require.NoError(t, err)

	var td map[string]any
	require.NoError(t, json.Unmarshal([]byte(tokenJSON), &td))
	assert.Equal(t, "sample_access_token", td["token"])
	assert.Equal(t, "123456789012", c.INN())

	saved, err := store.Load(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "sample_access_token", saved.Token)
}

// AC-2: SMS two-step flow stores token.
func TestSMSTwoStepFlow(t *testing.T) {
	challengeData := fixture(t, "phone_challenge.json")
	tokenData := fixture(t, "auth_token.json")

	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v2/auth/challenge/sms/start": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(challengeData)
		},
		"POST /v1/auth/challenge/sms/verify": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(tokenData)
		},
	})

	c := newClient()

	challenge, err := c.CreatePhoneChallenge(context.Background(), "79000000000")
	require.NoError(t, err)
	assert.Equal(t, "00000000-0000-0000-0000-000000000000", challenge.ChallengeToken)
	assert.Equal(t, 120, challenge.ExpireIn)

	tokenJSON, err := c.CreateAccessTokenByPhone(context.Background(), "79000000000", challenge.ChallengeToken, "123456")
	require.NoError(t, err)

	var td map[string]any
	require.NoError(t, json.Unmarshal([]byte(tokenJSON), &td))
	assert.Equal(t, "sample_access_token", td["token"])
}

// AC-3: 401 triggers exactly one refresh and one retry.
func TestRefreshOn401_SingleRetry(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	incomeData := fixture(t, "income_create.json")

	newTokenData := []byte(`{"token":"refreshed_token","refreshToken":"new_refresh","tokenExpireIn":"2025-01-01T00:00:00.000Z","refreshTokenExpiresIn":null,"profile":{"id":"1","inn":"123456789012"}}`)

	callCount := 0
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v1/income": func(w http.ResponseWriter, r *http.Request) {
			callCount++
			if callCount == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(incomeData)
		},
		"POST /v1/auth/token": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write(newTokenData)
		},
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	resp, err := c.Income().Create(context.Background(), "Service",
		nalogo.MustMoneyAmount("100"), nalogo.MustMoneyAmount("1"))
	require.NoError(t, err)
	assert.Equal(t, "test-receipt-uuid-123", resp.ApprovedReceiptUUID)
	assert.Equal(t, 2, callCount)
}

// AC-4: Concurrent 401s collapse into a single refresh call.
func TestRefreshSingleFlight_Concurrency(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	incomeData := fixture(t, "income_create.json")

	newTokenData := []byte(`{"token":"refreshed_token","refreshToken":"new_refresh","tokenExpireIn":"2025-01-01T00:00:00.000Z","refreshTokenExpiresIn":null,"profile":{"id":"1","inn":"123456789012"}}`)

	var refreshCalls int32
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v1/income": func(w http.ResponseWriter, r *http.Request) {
			// Always return 401 initially; after refresh, check token header
			auth := r.Header.Get("Authorization")
			if auth == "Bearer refreshed_token" {
				w.Header().Set("Content-Type", "application/json")
				w.Write(incomeData)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		},
		"POST /v1/auth/token": func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&refreshCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			w.Write(newTokenData)
		},
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	const goroutines = 20
	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			_, errs[i] = c.Income().Create(context.Background(), "Service",
				nalogo.MustMoneyAmount("100"), nalogo.MustMoneyAmount("1"))
		}()
	}
	wg.Wait()

	for _, err := range errs {
		assert.NoError(t, err)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&refreshCalls), "expected exactly 1 refresh call")
}

// AC-5: When both original endpoint and refresh return 401, ErrUnauthorized is surfaced.
func TestRefreshFailure_SurfacesErrUnauthorized(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")

	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v1/income": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		},
		"POST /v1/auth/token": func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
		},
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	_, err := c.Income().Create(context.Background(), "Service",
		nalogo.MustMoneyAmount("100"), nalogo.MustMoneyAmount("1"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, nalogo.ErrUnauthorized))
	var apiErr *nalogo.APIError
	assert.True(t, errors.As(err, &apiErr))
	assert.Equal(t, http.StatusUnauthorized, apiErr.StatusCode)
}

// AC-6: Decimal-precise totalAmount calculation (100.50*2 + 50.25*3 = 351.75, no float drift).
func TestDecimalPreciseTotalAmount(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	var capturedBody map[string]any

	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v1/income": func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.Write(fixture(t, "income_create.json"))
		},
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	services := []nalogo.IncomeServiceItem{
		{Name: "S1", Amount: nalogo.MustMoneyAmount("100.50"), Quantity: nalogo.MustMoneyAmount("2")},
		{Name: "S2", Amount: nalogo.MustMoneyAmount("50.25"), Quantity: nalogo.MustMoneyAmount("3")},
	}
	_, err := c.Income().CreateMultipleItems(context.Background(), services, nalogo.AtomTimeNow(), nil)
	require.NoError(t, err)
	assert.Equal(t, "351.75", capturedBody["totalAmount"])
}

// AC-7: Empty UUID in Cancel triggers pre-flight validation error (no HTTP).
func TestCancelEmptyUUID_PreflightValidation(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	called := false
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v1/cancel": func(w http.ResponseWriter, r *http.Request) { called = true },
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	_, err := c.Income().Cancel(context.Background(), "", nalogo.CancelCommentRefund)
	require.Error(t, err)
	assert.True(t, errors.Is(err, nalogo.ErrValidation))
	assert.False(t, called, "HTTP call must not be made")
}

// AC-8: Legal entity without INN triggers pre-flight validation.
func TestLegalEntityMissingINN_Validation(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v1/income": func(w http.ResponseWriter, _ *http.Request) {},
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	displayName := "LLC Test"
	client := &nalogo.IncomeClientInfo{IncomeType: nalogo.IncomeTypeFromLegalEntity, DisplayName: &displayName}
	_, err := c.Income().CreateMultipleItems(context.Background(),
		[]nalogo.IncomeServiceItem{{Name: "S", Amount: nalogo.MustMoneyAmount("100"), Quantity: nalogo.MustMoneyAmount("1")}},
		nalogo.AtomTimeNow(), client)
	require.Error(t, err)
	assert.True(t, errors.Is(err, nalogo.ErrValidation))
}

// AC-9: Non-positive amount/quantity triggers pre-flight validation.
func TestNonPositiveAmount_Validation(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{})
	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	_, err := c.Income().Create(context.Background(), "Service",
		nalogo.MustMoneyAmount("0"), nalogo.MustMoneyAmount("1"))
	require.Error(t, err)
	assert.True(t, errors.Is(err, nalogo.ErrValidation))
}

// AC-10: Cancel request sends Russian wire values byte-for-byte.
func TestCancelRussianWireValues(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	var capturedBody map[string]any

	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v1/cancel": func(w http.ResponseWriter, r *http.Request) {
			json.NewDecoder(r.Body).Decode(&capturedBody)
			w.Header().Set("Content-Type", "application/json")
			w.Write(fixture(t, "income_cancel.json"))
		},
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	_, err := c.Income().Cancel(context.Background(), "test-uuid", nalogo.CancelCommentRefund)
	require.NoError(t, err)
	assert.Equal(t, "Возврат средств", capturedBody["comment"])
}

// AC-11: PrintURL is pure — no HTTP call, returns correct URL.
func TestPrintURL_Pure(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	c := nalogo.New(nalogo.WithBaseURL("https://lknpd.nalog.ru/api"))
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	url, err := c.Receipt().PrintURL("test-uuid")
	require.NoError(t, err)
	assert.Equal(t, "https://lknpd.nalog.ru/api/receipt/123456789012/test-uuid/print", url)
}

// AC-12: PrintURL returns ErrNotAuthenticated when INN is not set.
func TestPrintURL_NotAuthenticated(t *testing.T) {
	c := nalogo.New()
	_, err := c.Receipt().PrintURL("test-uuid")
	require.Error(t, err)
	assert.True(t, errors.Is(err, nalogo.ErrNotAuthenticated))
}

// AC-13: Error hierarchy maps HTTP status codes to typed errors.
func TestErrorHierarchy(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		sentinel   error
	}{
		{"400", http.StatusBadRequest, nalogo.ErrValidation},
		{"401", http.StatusUnauthorized, nalogo.ErrUnauthorized},
		{"403", http.StatusForbidden, nalogo.ErrForbidden},
		{"404", http.StatusNotFound, nalogo.ErrNotFound},
		{"406", http.StatusNotAcceptable, nalogo.ErrClient},
		{"422", http.StatusUnprocessableEntity, nalogo.ErrPhone},
		{"500", http.StatusInternalServerError, nalogo.ErrServer},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tokenData := fixture(t, "auth_token.json")
			_, newClient := newTestServer(t, map[string]http.HandlerFunc{
				"GET /v1/user": func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.status)
				},
			})
			c := newClient()
			require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

			_, err := c.User().Get(context.Background())
			require.Error(t, err)
			assert.True(t, errors.Is(err, tc.sentinel), "expected %v, got %v", tc.sentinel, err)
			assert.True(t, errors.Is(err, nalogo.ErrDomain))
			var apiErr *nalogo.APIError
			assert.True(t, errors.As(err, &apiErr))
			assert.Equal(t, tc.status, apiErr.StatusCode)
		})
	}
}

// AC-14: Functional options configure client correctly.
func TestFunctionalOptions(t *testing.T) {
	store := &nalogo.MemoryStore{}
	c := nalogo.New(
		nalogo.WithBaseURL("https://custom.example.com/api"),
		nalogo.WithTimeout(5*1e9),
		nalogo.WithDeviceID("mydevice12345678901"),
		nalogo.WithTokenStore(store),
	)
	require.NotNil(t, c)
	// Verify INN is empty before auth
	assert.Equal(t, "", c.INN())
}

// AC-15: Context cancellation is propagated to HTTP requests.
func TestContextCancellation(t *testing.T) {
	started := make(chan struct{})
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"GET /v1/user": func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			w.WriteHeader(http.StatusServiceUnavailable)
		},
	})

	tokenData := fixture(t, "auth_token.json")
	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.User().Get(ctx)
		done <- err
	}()

	<-started
	cancel()

	err := <-done
	require.Error(t, err)
}

// AC-16: Slog masking: MaskedString always logs as "***".
func TestSlogMasking(t *testing.T) {
	m := nalogo.MaskedString("super-secret-inn-12345")
	assert.Equal(t, "***", m.LogValue().String())
}

// --- Additional functional tests ---

func TestReceiptJSON(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"GET /receipt/123456789012/test-receipt-uuid-123/json": jsonHandler(t, "receipt_json.json"),
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	result, err := c.Receipt().JSON(context.Background(), "test-receipt-uuid-123")
	require.NoError(t, err)
	assert.Equal(t, "test-receipt-uuid-123", result["id"])
}

func TestReceiptJSON_WhitespaceTrimmedUUID(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"GET /receipt/123456789012/test-receipt-uuid-123/json": jsonHandler(t, "receipt_json.json"),
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	result, err := c.Receipt().JSON(context.Background(), "  test-receipt-uuid-123  ")
	require.NoError(t, err)
	assert.Equal(t, "test-receipt-uuid-123", result["id"])
}

func TestUserGet(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"GET /v1/user": jsonHandler(t, "user.json"),
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	user, err := c.User().Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "123456789012", user.INN)
	assert.Equal(t, "Test User", user.DisplayName)
}

func TestPaymentTypeTable(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"GET /v1/payment-type/table": jsonHandler(t, "payment_types.json"),
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	entries, err := c.PaymentType().Table(context.Background())
	require.NoError(t, err)
	assert.Len(t, entries, 2)
	assert.Equal(t, "Тинькофф", entries[1].Name)
}

func TestPaymentTypeFavorite(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"GET /v1/payment-type/table": jsonHandler(t, "payment_types.json"),
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	fav, err := c.PaymentType().Favorite(context.Background())
	require.NoError(t, err)
	require.NotNil(t, fav)
	assert.Equal(t, "Тинькофф", fav.Name)
}

func TestTaxGet(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"GET /v1/taxes": jsonHandler(t, "taxes.json"),
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	result, err := c.Tax().Get(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "OK", result["status"])
}

func TestTaxHistory(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v1/taxes/history": jsonHandler(t, "taxes_history.json"),
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	result, err := c.Tax().History(context.Background(), "45000000")
	require.NoError(t, err)
	assert.NotNil(t, result["content"])
}

func TestTaxPayments(t *testing.T) {
	tokenData := fixture(t, "auth_token.json")
	_, newClient := newTestServer(t, map[string]http.HandlerFunc{
		"POST /v1/taxes/payments": jsonHandler(t, "taxes_payments.json"),
	})

	c := newClient()
	require.NoError(t, c.Authenticate(context.Background(), string(tokenData)))

	result, err := c.Tax().Payments(context.Background(), "45000000", false)
	require.NoError(t, err)
	assert.NotNil(t, result["content"])
}

func TestFileStore_SaveAndLoad(t *testing.T) {
	path := t.TempDir() + "/token.json"
	store := nalogo.NewFileStore(path)
	ctx := context.Background()

	td := &nalogo.TokenData{
		Token:        "tok",
		RefreshToken: "ref",
		Profile:      nalogo.UserProfile{INN: "123456789012"},
	}
	require.NoError(t, store.Save(ctx, td))

	loaded, err := store.Load(ctx)
	require.NoError(t, err)
	require.NotNil(t, loaded)
	assert.Equal(t, "tok", loaded.Token)
	assert.Equal(t, "123456789012", loaded.Profile.INN)
}

func TestFileStore_LoadMissingFile(t *testing.T) {
	store := nalogo.NewFileStore("/nonexistent/path/token.json")
	loaded, err := store.Load(context.Background())
	assert.NoError(t, err)
	assert.Nil(t, loaded)
}

func TestMoneyAmount_JSONRoundTrip(t *testing.T) {
	m := nalogo.MustMoneyAmount("100.50")
	b, err := m.MarshalJSON()
	require.NoError(t, err)
	assert.Equal(t, `"100.50"`, string(b))

	var m2 nalogo.MoneyAmount
	require.NoError(t, m2.UnmarshalJSON(b))
	assert.True(t, m.Decimal.Equal(m2.Decimal))
}

func TestAtomTime_JSONFormat(t *testing.T) {
	at := nalogo.AtomTimeNow()
	b, err := at.MarshalJSON()
	require.NoError(t, err)
	s := string(b)
	// Must end with Z" (Z suffix, not +00:00)
	assert.True(t, len(s) > 2 && s[len(s)-2:] == `Z"`, "expected Z suffix, got %s", s)
}
