// Package subscriptions runs subscriptions: starting them with their first invoice, changing and canceling them, and
// renewing them when a billing period ends.
package subscriptions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"example.com/tally/internal/billing"
	"example.com/tally/internal/clock"
	"example.com/tally/internal/payments"
	"example.com/tally/internal/store"
)

// Errors.
var (
	ErrPlanInactive = errors.New("the plan is not active")
	ErrCanceled     = errors.New("the subscription is canceled")
)

// Service runs subscriptions.
type Service struct {
	Store     *store.Store
	Payments  *payments.Service
	Clock     clock.Clock
	Publisher payments.Publisher // nil: no events
}

// CreateInput is a new subscription.
type CreateInput struct {
	CustomerID      string
	PlanID          string
	Quantity        int // 0 means 1
	DiscountPercent int
}

// Create starts a subscription now. With a plan that has a trial, the subscription is trialing until the trial ends, and
// its first period starts then. Otherwise its first period starts now; for a licensed plan that period is invoiced at once
// and charged to the customer's default payment method, if it has one (a declined charge makes the subscription past due).
func (s *Service) Create(ctx context.Context, orgID string, in CreateInput) (store.Subscription, error) {
	plan, ok := s.Store.Plan(orgID, in.PlanID)
	if !ok {
		return store.Subscription{}, store.ErrNotFound
	}
	if !plan.Active {
		return store.Subscription{}, ErrPlanInactive
	}
	if in.Quantity == 0 {
		in.Quantity = 1
	}
	now := s.Clock.Now()
	sub := store.Subscription{OrgID: orgID, CustomerID: in.CustomerID, PlanID: plan.ID, Quantity: in.Quantity, DiscountPercent: in.DiscountPercent,
		StartedAt: now, CurrentPeriodStart: now}
	if plan.TrialDays > 0 {
		end := now.AddDate(0, 0, plan.TrialDays)
		sub.Status, sub.TrialEnd, sub.CurrentPeriodEnd = store.Trialing, &end, end
	} else {
		sub.Status, sub.CurrentPeriodEnd = store.Active, billing.AddInterval(now, plan.Interval)
	}
	sub, err := s.Store.CreateSubscription(sub, now)
	if err != nil {
		return store.Subscription{}, err
	}
	s.publish(ctx, orgID, "subscription.created", sub)
	if sub.Status == store.Active && plan.UsageType == store.Licensed {
		paid, err := s.invoicePeriod(ctx, sub, plan, sub.CurrentPeriodStart, sub.CurrentPeriodEnd)
		if err != nil {
			return sub, err
		}
		if !paid {
			if sub, err = s.setStatus(ctx, sub, store.PastDue); err != nil {
				return sub, err
			}
		}
	}
	return sub, nil
}

// UpdateInput changes a subscription; nil fields stay as they are.
type UpdateInput struct {
	PlanID   *string
	Quantity *int
}

// Update changes a subscription's plan or quantity. The change applies at once and is billed from the next period on.
func (s *Service) Update(ctx context.Context, orgID, id string, in UpdateInput) (store.Subscription, error) {
	if in.PlanID != nil {
		p, ok := s.Store.Plan(orgID, *in.PlanID)
		if !ok {
			return store.Subscription{}, store.ErrNotFound
		}
		if !p.Active {
			return store.Subscription{}, ErrPlanInactive
		}
	}
	sub, err := s.Store.UpdateSubscription(orgID, id, func(sub *store.Subscription) error {
		if sub.Status == store.Canceled {
			return ErrCanceled
		}
		if in.PlanID != nil {
			sub.PlanID = *in.PlanID
		}
		if in.Quantity != nil {
			sub.Quantity = *in.Quantity
		}
		return nil
	})
	if err != nil {
		return store.Subscription{}, err
	}
	s.publish(ctx, orgID, "subscription.updated", sub)
	return sub, nil
}

// Cancel cancels a subscription now, or at the end of its current period when atPeriodEnd is set. Canceling a canceled
// subscription changes nothing.
func (s *Service) Cancel(ctx context.Context, orgID, id string, atPeriodEnd bool) (store.Subscription, error) {
	now := s.Clock.Now()
	sub, err := s.Store.UpdateSubscription(orgID, id, func(sub *store.Subscription) error {
		if sub.Status == store.Canceled {
			return nil
		}
		if atPeriodEnd {
			sub.CancelAtPeriodEnd = true
			return nil
		}
		sub.Status, sub.CanceledAt = store.Canceled, &now
		return nil
	})
	if err != nil {
		return store.Subscription{}, err
	}
	if sub.Status == store.Canceled {
		s.publish(ctx, orgID, "subscription.canceled", sub)
	} else {
		s.publish(ctx, orgID, "subscription.updated", sub)
	}
	return sub, nil
}

// RenewDue renews every subscription, of every organization, whose current period has ended. A subscription set to cancel
// at the end of its period is canceled. Otherwise its next period starts and is invoiced: a licensed plan bills the new
// period in advance, a metered plan bills the usage of the period that ended. A declined charge makes the subscription past
// due. RenewDue returns how many subscriptions it renewed or canceled.
func (s *Service) RenewDue(ctx context.Context) (int, error) {
	now := s.Clock.Now()
	n := 0
	for _, sub := range s.Store.DueSubscriptions(now) {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		if err := s.renew(ctx, sub, now); err != nil {
			return n, fmt.Errorf("renew %s: %w", sub.ID, err)
		}
		n++
	}
	return n, nil
}

func (s *Service) renew(ctx context.Context, sub store.Subscription, now time.Time) error {
	plan, ok := s.Store.Plan(sub.OrgID, sub.PlanID)
	if !ok {
		return store.ErrNotFound
	}
	if sub.CancelAtPeriodEnd {
		end := sub.CurrentPeriodEnd
		sub, err := s.Store.UpdateSubscription(sub.OrgID, sub.ID, func(x *store.Subscription) error {
			x.Status, x.CanceledAt = store.Canceled, &end
			return nil
		})
		if err == nil {
			s.publish(ctx, sub.OrgID, "subscription.canceled", sub)
		}
		return err
	}
	start := now
	end := billing.AddInterval(start, plan.Interval)
	paid := true
	var err error
	switch {
	case plan.UsageType == store.Metered && sub.Status != store.Trialing:
		paid, err = s.invoicePeriod(ctx, sub, plan, sub.CurrentPeriodStart, sub.CurrentPeriodEnd)
	case plan.UsageType == store.Licensed:
		paid, err = s.invoicePeriod(ctx, sub, plan, start, end)
	}
	if err != nil {
		return err
	}
	sub, err = s.Store.UpdateSubscription(sub.OrgID, sub.ID, func(x *store.Subscription) error {
		x.CurrentPeriodStart, x.CurrentPeriodEnd = start, end
		if paid {
			x.Status = store.Active
		} else {
			x.Status = store.PastDue
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.publish(ctx, sub.OrgID, "subscription.updated", sub)
	return nil
}

// invoicePeriod invoices one period of a subscription and charges the invoice to the customer's default payment method.
// It reports false when the charge was declined. An invoice that needs no charge is marked paid; one whose customer has no
// payment method stays open and counts as paid here (it is paid by hand).
func (s *Service) invoicePeriod(ctx context.Context, sub store.Subscription, plan store.Plan, start, end time.Time) (bool, error) {
	var lines []store.Line
	period := start.Format("Jan 2, 2006") + " to " + end.Format("Jan 2, 2006")
	switch plan.UsageType {
	case store.Licensed:
		if plan.UnitAmount > 0 {
			lines = append(lines, store.Line{Description: plan.Name + ", " + period, Quantity: int64(sub.Quantity), UnitAmount: plan.UnitAmount})
		}
	case store.Metered:
		if used := s.Store.UsageTotal(sub.OrgID, sub.ID, start, end); used > 0 {
			lines = append(lines, store.Line{Description: plan.Name + " usage, " + period, Quantity: used, UnitAmount: plan.UnitAmount})
		}
	}
	ps, pe := start, end
	inv, err := s.Store.CreateInvoice(store.Invoice{OrgID: sub.OrgID, CustomerID: sub.CustomerID, SubscriptionID: sub.ID, Currency: plan.Currency,
		Lines: lines, DiscountPercent: sub.DiscountPercent, PeriodStart: &ps, PeriodEnd: &pe}, s.Clock.Now())
	if err != nil {
		return false, err
	}
	s.publish(ctx, sub.OrgID, "invoice.created", map[string]any{"invoice_id": inv.ID, "subscription_id": sub.ID})
	_, _, err = s.Payments.Pay(ctx, sub.OrgID, inv.ID, "")
	var decline *payments.DeclineError
	switch {
	case err == nil, errors.Is(err, payments.ErrNoPaymentMethod):
		return true, nil
	case errors.As(err, &decline):
		return false, nil
	default:
		return false, err
	}
}

func (s *Service) setStatus(ctx context.Context, sub store.Subscription, st store.SubStatus) (store.Subscription, error) {
	sub, err := s.Store.UpdateSubscription(sub.OrgID, sub.ID, func(x *store.Subscription) error {
		x.Status = st
		return nil
	})
	if err == nil {
		s.publish(ctx, sub.OrgID, "subscription.updated", sub)
	}
	return sub, err
}

func (s *Service) publish(ctx context.Context, orgID, typ string, data any) {
	if s.Publisher != nil {
		_ = s.Publisher.Publish(ctx, orgID, typ, data)
	}
}
