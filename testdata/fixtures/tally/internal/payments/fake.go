package payments

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Fake is the gateway of tests and the demo server. What it does depends on the payment method:
//
//	pm_card_declined            declined, code "card_declined"
//	pm_card_insufficient_funds  declined, code "insufficient_funds"
//	pm_gateway_down             fails with ErrUnavailable
//	any other "pm_..."          succeeds
//	anything else               declined, code "invalid_payment_method"
//
// Like the real gateway in its default mode, it takes every charge it is asked for: it does not merge repeated requests.
type Fake struct {
	// OnCharge, when set, is called at the start of every Charge, before its outcome is decided. Tests use it to hold a
	// charge while they do something else. Set it before the fake is used.
	OnCharge func(ChargeRequest)

	mu      sync.Mutex
	calls   []ChargeRequest
	charges []Charge
}

// Charge is a charge the fake took.
type Charge struct {
	Ref string
	ChargeRequest
}

// Charge implements Gateway.
func (f *Fake) Charge(ctx context.Context, req ChargeRequest) (ChargeResult, error) {
	if f.OnCharge != nil {
		f.OnCharge(req)
	}
	if err := ctx.Err(); err != nil {
		return ChargeResult{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, req)
	switch pm := req.PaymentMethod; {
	case pm == "pm_card_declined":
		return ChargeResult{}, &DeclineError{Code: "card_declined"}
	case pm == "pm_card_insufficient_funds":
		return ChargeResult{}, &DeclineError{Code: "insufficient_funds"}
	case pm == "pm_gateway_down":
		return ChargeResult{}, ErrUnavailable
	case !strings.HasPrefix(pm, "pm_"):
		return ChargeResult{}, &DeclineError{Code: "invalid_payment_method"}
	}
	c := Charge{Ref: fmt.Sprintf("ch_%06d", len(f.charges)+1), ChargeRequest: req}
	f.charges = append(f.charges, c)
	return ChargeResult{Ref: c.Ref}, nil
}

// Calls returns every charge request the fake received, including declined and failed ones.
func (f *Fake) Calls() []ChargeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ChargeRequest(nil), f.calls...)
}

// Charges returns the charges the fake took (the money that moved).
func (f *Fake) Charges() []Charge {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Charge(nil), f.charges...)
}
