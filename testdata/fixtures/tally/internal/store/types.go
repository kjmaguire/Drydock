package store

import (
	"encoding/json"
	"time"

	"example.com/tally/internal/money"
)

// OrgPlan is the Tally plan an organization pays for.
type OrgPlan string

// Tally's own plans.
const (
	OrgFree     OrgPlan = "free"
	OrgTeam     OrgPlan = "team"
	OrgBusiness OrgPlan = "business"
)

// Valid reports whether p is a known plan.
func (p OrgPlan) Valid() bool { return p == OrgFree || p == OrgTeam || p == OrgBusiness }

// Org is a tenant: a company that bills its customers through Tally.
type Org struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Plan     OrgPlan `json:"plan"`
	TimeZone string  `json:"time_zone"` // IANA name, for example "Europe/Berlin"
	// TaxRateBP is the tax rate charged on the organization's invoices, in basis points (1% = 100).
	TaxRateBP int       `json:"tax_rate_bp"`
	CreatedAt time.Time `json:"created_at"`
}

// User is a person who can sign in. A user can belong to several organizations.
type User struct {
	ID        string    `json:"id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// Role is what a member may do in an organization.
type Role string

// Roles.
const (
	RoleOwner   Role = "owner"
	RoleAdmin   Role = "admin"
	RoleBilling Role = "billing"
	RoleMember  Role = "member"
	RoleViewer  Role = "viewer"
)

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	switch r {
	case RoleOwner, RoleAdmin, RoleBilling, RoleMember, RoleViewer:
		return true
	}
	return false
}

// Member is a user's membership of an organization.
type Member struct {
	OrgID     string    `json:"org_id"`
	UserID    string    `json:"user_id"`
	Role      Role      `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

// APIKey lets a member call the API. It acts as that member, with the member's current role.
type APIKey struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	UserID     string     `json:"user_id"`
	Name       string     `json:"name"`
	Token      string     `json:"token"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Invite asks someone to join an organization. Its token is sent to the invited address.
type Invite struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	Email      string     `json:"email"`
	Role       Role       `json:"role"`
	Token      string     `json:"token"`
	InvitedBy  string     `json:"invited_by"`
	CreatedAt  time.Time  `json:"created_at"`
	AcceptedAt *time.Time `json:"accepted_at,omitempty"`
	RevokedAt  *time.Time `json:"revoked_at,omitempty"`
}

// Customer is one of an organization's customers.
type Customer struct {
	ID    string `json:"id"`
	OrgID string `json:"org_id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	// DefaultPaymentMethod is charged for renewals ("pm_..."); empty means invoices wait to be paid by hand.
	DefaultPaymentMethod string            `json:"default_payment_method,omitempty"`
	Metadata             map[string]string `json:"metadata,omitempty"`
	CreatedAt            time.Time         `json:"created_at"`
}

// Interval is how often a plan bills.
type Interval string

// Intervals.
const (
	Monthly Interval = "month"
	Yearly  Interval = "year"
)

// UsageType says what a plan charges for.
type UsageType string

// Usage types.
const (
	// Licensed plans charge UnitAmount per seat (the subscription's quantity) at the start of each period.
	Licensed UsageType = "licensed"
	// Metered plans charge UnitAmount per unit of reported usage at the end of each period.
	Metered UsageType = "metered"
)

// Plan is something an organization sells to its customers.
type Plan struct {
	ID         string         `json:"id"`
	OrgID      string         `json:"org_id"`
	Name       string         `json:"name"`
	Currency   money.Currency `json:"currency"`
	UnitAmount int64          `json:"unit_amount"` // minor units
	Interval   Interval       `json:"interval"`
	UsageType  UsageType      `json:"usage_type"`
	TrialDays  int            `json:"trial_days"`
	Active     bool           `json:"active"`
	CreatedAt  time.Time      `json:"created_at"`
}

// SubStatus is a subscription's state.
type SubStatus string

// Subscription states.
const (
	Trialing SubStatus = "trialing"
	Active   SubStatus = "active"
	PastDue  SubStatus = "past_due"
	Canceled SubStatus = "canceled"
)

// Subscription is a customer's subscription to a plan.
type Subscription struct {
	ID              string    `json:"id"`
	OrgID           string    `json:"org_id"`
	CustomerID      string    `json:"customer_id"`
	PlanID          string    `json:"plan_id"`
	Quantity        int       `json:"quantity"`
	Status          SubStatus `json:"status"`
	DiscountPercent int       `json:"discount_percent"`
	// StartedAt is when the subscription began; its billing periods are counted from it.
	StartedAt          time.Time  `json:"started_at"`
	CurrentPeriodStart time.Time  `json:"current_period_start"`
	CurrentPeriodEnd   time.Time  `json:"current_period_end"`
	TrialEnd           *time.Time `json:"trial_end,omitempty"`
	CancelAtPeriodEnd  bool       `json:"cancel_at_period_end"`
	CanceledAt         *time.Time `json:"canceled_at,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// Line is one line of an invoice. A negative UnitAmount is a credit.
type Line struct {
	Description string `json:"description"`
	Quantity    int64  `json:"quantity"`
	UnitAmount  int64  `json:"unit_amount"`
}

// Amount is the line's quantity times its unit amount.
func (l Line) Amount() int64 { return l.Quantity * l.UnitAmount }

// InvoiceStatus is an invoice's state.
type InvoiceStatus string

// Invoice states.
const (
	InvoiceOpen InvoiceStatus = "open"
	InvoicePaid InvoiceStatus = "paid"
	InvoiceVoid InvoiceStatus = "void"
)

// Invoice is a bill to a customer.
type Invoice struct {
	ID              string         `json:"id"`
	OrgID           string         `json:"org_id"`
	CustomerID      string         `json:"customer_id"`
	SubscriptionID  string         `json:"subscription_id,omitempty"`
	Currency        money.Currency `json:"currency"`
	Lines           []Line         `json:"lines"`
	DiscountPercent int            `json:"discount_percent"`
	TaxRateBP       int            `json:"tax_rate_bp"`
	Status          InvoiceStatus  `json:"status"`
	PeriodStart     *time.Time     `json:"period_start,omitempty"`
	PeriodEnd       *time.Time     `json:"period_end,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
	PaidAt          *time.Time     `json:"paid_at,omitempty"`
	VoidedAt        *time.Time     `json:"voided_at,omitempty"`
}

// PaymentStatus is a payment attempt's result.
type PaymentStatus string

// Payment results.
const (
	PaymentSucceeded PaymentStatus = "succeeded"
	PaymentFailed    PaymentStatus = "failed"
)

// Payment is one attempt to charge an invoice.
type Payment struct {
	ID          string         `json:"id"`
	OrgID       string         `json:"org_id"`
	InvoiceID   string         `json:"invoice_id"`
	Amount      int64          `json:"amount"`
	Currency    money.Currency `json:"currency"`
	Status      PaymentStatus  `json:"status"`
	FailureCode string         `json:"failure_code,omitempty"`
	GatewayRef  string         `json:"gateway_ref,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

// UsageRecord is usage reported for a subscription to a metered plan.
type UsageRecord struct {
	ID             string    `json:"id"`
	OrgID          string    `json:"org_id"`
	SubscriptionID string    `json:"subscription_id"`
	Quantity       int64     `json:"quantity"`
	At             time.Time `json:"at"`
	CreatedAt      time.Time `json:"created_at"`
}

// WebhookEndpoint is a URL of the organization's that receives events.
type WebhookEndpoint struct {
	ID     string   `json:"id"`
	OrgID  string   `json:"org_id"`
	URL    string   `json:"url"`
	Events []string `json:"events"` // event types, or "*" for all
	// Secret signs the deliveries (webhooks.Sign).
	Secret         string    `json:"secret"`
	Enabled        bool      `json:"enabled"`
	DisabledReason string    `json:"disabled_reason,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
}

// Event is something that happened in an organization, sent to its webhook endpoints.
type Event struct {
	ID        string          `json:"id"`
	OrgID     string          `json:"org_id"`
	Type      string          `json:"type"`
	Data      json.RawMessage `json:"data"`
	CreatedAt time.Time       `json:"created_at"`
}

// DeliveryStatus is a webhook delivery's state.
type DeliveryStatus string

// Delivery states.
const (
	DeliveryPending   DeliveryStatus = "pending"
	DeliverySucceeded DeliveryStatus = "succeeded"
	DeliveryFailed    DeliveryStatus = "failed"
)

// Delivery is the sending of one event to one endpoint.
type Delivery struct {
	ID             string         `json:"id"`
	OrgID          string         `json:"org_id"`
	EndpointID     string         `json:"endpoint_id"`
	EventID        string         `json:"event_id"`
	Status         DeliveryStatus `json:"status"`
	Attempts       int            `json:"attempts"`
	NextAttemptAt  *time.Time     `json:"next_attempt_at,omitempty"`
	LastAttemptAt  *time.Time     `json:"last_attempt_at,omitempty"`
	ResponseStatus int            `json:"response_status,omitempty"`
	Error          string         `json:"error,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}
