// Package payments charges invoices through the payment gateway.
package payments

import (
	"context"
	"errors"
	"fmt"

	"example.com/tally/internal/billing"
	"example.com/tally/internal/clock"
	"example.com/tally/internal/money"
	"example.com/tally/internal/store"
)

// ChargeRequest asks the gateway to take money from a payment method.
type ChargeRequest struct {
	Amount        int64 // minor units
	Currency      money.Currency
	PaymentMethod string // "pm_..."
	Description   string
}

// ChargeResult is a successful charge.
type ChargeResult struct {
	Ref string // the gateway's reference, "ch_..."
}

// Gateway is the payment processor.
type Gateway interface {
	Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error)
}

// DeclineError is a charge the card's issuer refused. Nothing was taken.
type DeclineError struct {
	Code string // for example "card_declined", "insufficient_funds"
}

func (e *DeclineError) Error() string { return "card declined: " + e.Code }

// Errors.
var (
	// ErrUnavailable means the gateway could not be reached or failed; nothing was taken and the charge may be retried.
	ErrUnavailable     = errors.New("payment gateway unavailable")
	ErrAlreadyPaid     = errors.New("the invoice is already paid")
	ErrInvoiceVoid     = errors.New("the invoice is void")
	ErrNoPaymentMethod = errors.New("no payment method to charge")
)

// Publisher sends events to the organization's webhook endpoints (webhooks.Service).
type Publisher interface {
	Publish(ctx context.Context, orgID, eventType string, data any) error
}

// Service pays invoices.
type Service struct {
	Store     *store.Store
	Gateway   Gateway
	Clock     clock.Clock
	Publisher Publisher // nil: no events
}

// Pay charges an open invoice's total to paymentMethod, or to the customer's default payment method when it is empty, and
// marks the invoice paid. An invoice whose total is zero or less is marked paid without a charge. A declined charge is
// recorded as a failed payment and returned as a *DeclineError; when the gateway is unavailable nothing is recorded and the
// error wraps ErrUnavailable.
func (s *Service) Pay(ctx context.Context, orgID, invoiceID, paymentMethod string) (store.Invoice, store.Payment, error) {
	inv, ok := s.Store.Invoice(orgID, invoiceID)
	if !ok {
		return store.Invoice{}, store.Payment{}, store.ErrNotFound
	}
	switch inv.Status {
	case store.InvoicePaid:
		return inv, store.Payment{}, ErrAlreadyPaid
	case store.InvoiceVoid:
		return inv, store.Payment{}, ErrInvoiceVoid
	}
	total := billing.Compute(inv).Total
	if total <= 0 {
		inv, err := s.markPaid(ctx, inv, store.Payment{})
		return inv, store.Payment{}, err
	}
	if paymentMethod == "" {
		if c, ok := s.Store.Customer(orgID, inv.CustomerID); ok {
			paymentMethod = c.DefaultPaymentMethod
		}
	}
	if paymentMethod == "" {
		return inv, store.Payment{}, ErrNoPaymentMethod
	}
	res, err := s.Gateway.Charge(ctx, ChargeRequest{Amount: total, Currency: inv.Currency, PaymentMethod: paymentMethod, Description: "Invoice " + inv.ID})
	var decline *DeclineError
	switch {
	case errors.As(err, &decline):
		p, perr := s.Store.CreatePayment(store.Payment{OrgID: orgID, InvoiceID: inv.ID, Amount: total, Currency: inv.Currency, Status: store.PaymentFailed, FailureCode: decline.Code}, s.Clock.Now())
		if perr != nil {
			return inv, store.Payment{}, perr
		}
		s.publish(ctx, orgID, "invoice.payment_failed", map[string]any{"invoice_id": inv.ID, "payment_id": p.ID, "failure_code": decline.Code})
		return inv, p, err
	case err != nil:
		if errors.Is(err, ErrUnavailable) {
			return inv, store.Payment{}, err
		}
		return inv, store.Payment{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	p, err := s.Store.CreatePayment(store.Payment{OrgID: orgID, InvoiceID: inv.ID, Amount: total, Currency: inv.Currency, Status: store.PaymentSucceeded, GatewayRef: res.Ref}, s.Clock.Now())
	if err != nil {
		return inv, store.Payment{}, err
	}
	inv, err = s.markPaid(ctx, inv, p)
	return inv, p, err
}

func (s *Service) markPaid(ctx context.Context, inv store.Invoice, p store.Payment) (store.Invoice, error) {
	now := s.Clock.Now()
	inv, err := s.Store.UpdateInvoice(inv.OrgID, inv.ID, func(i *store.Invoice) error {
		i.Status = store.InvoicePaid
		i.PaidAt = &now
		return nil
	})
	if err != nil {
		return store.Invoice{}, err
	}
	s.publish(ctx, inv.OrgID, "invoice.paid", map[string]any{"invoice_id": inv.ID, "payment_id": p.ID, "amount": p.Amount, "currency": inv.Currency})
	return inv, nil
}

func (s *Service) publish(ctx context.Context, orgID, typ string, data any) {
	if s.Publisher != nil {
		_ = s.Publisher.Publish(ctx, orgID, typ, data)
	}
}
