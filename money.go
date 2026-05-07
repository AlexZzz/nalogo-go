package nalogo

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// MoneyAmount wraps decimal.Decimal and serializes to/from a JSON quoted string
// (e.g. "100.50") as required by the FNS API.
type MoneyAmount struct {
	decimal.Decimal
}

func (m MoneyAmount) MarshalJSON() ([]byte, error) {
	// StringFixed(2) preserves trailing zeros required by FNS wire format (e.g. "100.50").
	s := m.Decimal.StringFixed(2)
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	b = append(b, s...)
	b = append(b, '"')
	return b, nil
}

func (m *MoneyAmount) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	d, err := decimal.NewFromString(s)
	if err != nil {
		return err
	}
	m.Decimal = d
	return nil
}

// NewMoneyAmount constructs a MoneyAmount from a decimal string (e.g. "100.50").
func NewMoneyAmount(s string) (MoneyAmount, error) {
	d, err := decimal.NewFromString(s)
	if err != nil {
		return MoneyAmount{}, err
	}
	return MoneyAmount{d}, nil
}

// MustMoneyAmount constructs a MoneyAmount from a decimal string and panics on error.
// For use in tests and compile-time constants only.
func MustMoneyAmount(s string) MoneyAmount {
	m, err := NewMoneyAmount(s)
	if err != nil {
		panic("nalogo: invalid MoneyAmount: " + s)
	}
	return m
}

// atomTimeLayout is the datetime format expected by the FNS API.
// Must use literal "Z" suffix, not "+00:00" (which time.RFC3339 would emit).
const atomTimeLayout = "2006-01-02T15:04:05.000Z"

// AtomTime wraps time.Time and serializes to/from the FNS ATOM datetime format.
type AtomTime struct {
	time.Time
}

func (a AtomTime) MarshalJSON() ([]byte, error) {
	s := a.UTC().Format(atomTimeLayout)
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	b = append(b, s...)
	b = append(b, '"')
	return b, nil
}

func (a *AtomTime) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	t, err := time.Parse(atomTimeLayout, s)
	if err != nil {
		// Fallback: try RFC3339 for responses that don't use the Z-suffix format
		t, err = time.Parse(time.RFC3339, s)
		if err != nil {
			return err
		}
	}
	a.Time = t
	return nil
}

// AtomTimeNow returns the current UTC time wrapped in AtomTime.
func AtomTimeNow() AtomTime {
	return AtomTime{time.Now().UTC()}
}

// generateDeviceID mirrors Python's uuid4()[:21].lower() — 21 lowercase hex chars.
func generateDeviceID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")[:21]
}
