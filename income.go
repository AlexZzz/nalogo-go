package nalogo

import (
	"context"
	"net/http"

	"github.com/shopspring/decimal"
)

// CancelComment is the cancellation reason wire value (Russian string required by FNS API).
type CancelComment = string

const (
	CancelCommentCancel = CancelComment("Чек сформирован ошибочно")
	CancelCommentRefund = CancelComment("Возврат средств")
)

// IncomeType mirrors upstream IncomeType enum.
type IncomeType = string

const (
	IncomeTypeFromIndividual    = IncomeType("FROM_INDIVIDUAL")
	IncomeTypeFromLegalEntity   = IncomeType("FROM_LEGAL_ENTITY")
	IncomeTypeFromForeignAgency = IncomeType("FROM_FOREIGN_AGENCY")
)

// IncomeServiceItem represents one line item in an income receipt.
type IncomeServiceItem struct {
	Name     string      `json:"name"`
	Amount   MoneyAmount `json:"amount"`
	Quantity MoneyAmount `json:"quantity"`
}

// IncomeClientInfo carries payer information for an income receipt.
// For individual clients (default), all fields are optional.
// For legal entities (IncomeTypeFromLegalEntity), INN and DisplayName are required.
type IncomeClientInfo struct {
	ContactPhone *string    `json:"contactPhone,omitempty"`
	DisplayName  *string    `json:"displayName,omitempty"`
	IncomeType   IncomeType `json:"incomeType"`
	INN          *string    `json:"inn,omitempty"`
}

// incomeRequest is the wire payload for POST /v1/income.
type incomeRequest struct {
	OperationTime                  AtomTime         `json:"operationTime"`
	RequestTime                    AtomTime         `json:"requestTime"`
	Services                       []incomeItemWire `json:"services"`
	TotalAmount                    MoneyAmount      `json:"totalAmount"`
	Client                         IncomeClientInfo `json:"client"`
	PaymentType                    string           `json:"paymentType"`
	IgnoreMaxTotalIncomeRestriction bool             `json:"ignoreMaxTotalIncomeRestriction"`
}

type incomeItemWire struct {
	Name     string      `json:"name"`
	Amount   MoneyAmount `json:"amount"`
	Quantity MoneyAmount `json:"quantity"`
}

// IncomeResponse is returned by Create and CreateMultipleItems.
type IncomeResponse struct {
	ApprovedReceiptUUID string `json:"approvedReceiptUuid"`
}

// cancelRequest is the wire payload for POST /v1/cancel.
type cancelRequest struct {
	OperationTime AtomTime      `json:"operationTime"`
	RequestTime   AtomTime      `json:"requestTime"`
	Comment       CancelComment `json:"comment"`
	ReceiptUUID   string        `json:"receiptUuid"`
	PartnerCode   *string       `json:"partnerCode,omitempty"`
}

// CancelResponse is returned by Cancel.
type CancelResponse struct {
	IncomeInfo map[string]any `json:"incomeInfo"`
}

// Income is the income-receipt API accessor.
type Income struct{ c *Client }

// Create issues a single-item income receipt.
func (a *Income) Create(ctx context.Context, name string, amount, quantity MoneyAmount) (*IncomeResponse, error) {
	return a.CreateMultipleItems(ctx, []IncomeServiceItem{{Name: name, Amount: amount, Quantity: quantity}}, AtomTimeNow(), nil)
}

// CreateMultipleItems issues an income receipt with one or more line items.
// operationTime is the time the service was rendered; pass AtomTimeNow() for "now".
// client is optional; pass nil for an individual payer (default).
func (a *Income) CreateMultipleItems(ctx context.Context, services []IncomeServiceItem, operationTime AtomTime, client *IncomeClientInfo) (*IncomeResponse, error) {
	if len(services) == 0 {
		return nil, newValidationError("services cannot be empty")
	}

	// Validate legal-entity client fields.
	if client != nil && client.IncomeType == IncomeTypeFromLegalEntity {
		if client.INN == nil || *client.INN == "" {
			return nil, newValidationError("client INN cannot be empty for legal entity")
		}
		if client.DisplayName == nil || *client.DisplayName == "" {
			return nil, newValidationError("client DisplayName cannot be empty for legal entity")
		}
	}

	// Compute total and build wire items.
	total := decimal.Zero
	wire := make([]incomeItemWire, len(services))
	for i, s := range services {
		if !s.Amount.IsPositive() {
			return nil, newValidationError("amount must be greater than 0")
		}
		if !s.Quantity.IsPositive() {
			return nil, newValidationError("quantity must be greater than 0")
		}
		total = total.Add(s.Amount.Decimal.Mul(s.Quantity.Decimal))
		wire[i] = incomeItemWire{Name: s.Name, Amount: s.Amount, Quantity: s.Quantity}
	}

	payer := IncomeClientInfo{IncomeType: IncomeTypeFromIndividual}
	if client != nil {
		payer = *client
	}

	req := incomeRequest{
		OperationTime:                  operationTime,
		RequestTime:                    AtomTimeNow(),
		Services:                       wire,
		TotalAmount:                    MoneyAmount{total},
		Client:                         payer,
		PaymentType:                    "CASH",
		IgnoreMaxTotalIncomeRestriction: false,
	}

	var resp IncomeResponse
	if err := a.c.do(ctx, a.c.apiClient, http.MethodPost, a.c.url1("income"), req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Cancel annuls an income receipt.
// comment must be one of CancelCommentCancel or CancelCommentRefund.
func (a *Income) Cancel(ctx context.Context, receiptUUID string, comment CancelComment) (*CancelResponse, error) {
	if receiptUUID == "" {
		return nil, newValidationError("receiptUUID cannot be empty")
	}
	if comment != CancelCommentCancel && comment != CancelCommentRefund {
		return nil, newValidationError("comment must be CancelCommentCancel or CancelCommentRefund")
	}

	req := cancelRequest{
		OperationTime: AtomTimeNow(),
		RequestTime:   AtomTimeNow(),
		Comment:       comment,
		ReceiptUUID:   receiptUUID,
	}

	var resp CancelResponse
	if err := a.c.do(ctx, a.c.apiClient, http.MethodPost, a.c.url1("cancel"), req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
