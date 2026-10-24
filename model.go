package billing

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/arandu-io/hesape/database/model"
)

// Plan is one offer of the catalogue: what a customer subscribes to, for how
// many months, at what price.
//
// The catalogue is the platform's and not a customer's, so the table is
// Global: every tenant reads the same plans, and only the platform's admin
// writes them. The price, the months and every other number a business person
// changes live in these rows -- an application fills them from its own seeder
// or from the admin's screen, never from a constant in code.
//
// Plans, PlanQuery and PlanCollection are generated beside this file, in
// PlanQuery.go, by aru model:build.
type Plan struct {
	model.Model

	// ID is the identifier, written by the application.
	ID string `db:"id"`
	// Slug is the stable name an application refers to the plan by in its own
	// code and seeders: "camera-mensal". Unique across the catalogue.
	Slug string `db:"slug"`
	// Name is what a customer reads.
	Name string `db:"name"`
	// Description is the sentence under the name.
	Description string `db:"description"`
	// Features are the lines listed under the price, in order.
	Features StringList `db:"features"`
	// Price is what one period costs, in centavos of BRL.
	Price int64 `db:"price"`
	// IntervalMonths is how many months one payment buys.
	IntervalMonths int `db:"interval_months"`
	// TrialDays is how long a trial lasts. Zero offers none.
	TrialDays int `db:"trial_days"`
	// GraceDays is how long a subscription stays past due after its period
	// ends without a payment, before it expires. Zero expires it at once.
	GraceDays int `db:"grace_days"`
	// InstallmentsFrom is the smallest IntervalMonths that may be paid in
	// installments. Zero never offers installments.
	InstallmentsFrom int `db:"installments_from"`
	// InstallmentsCap is the most installments offered, never above
	// MaxInstallments. Zero means MaxInstallments.
	InstallmentsCap int `db:"installments_cap"`
	// Meta is what the consuming application attaches to the plan: the
	// cameras it covers, the credits it deposits. This package stores it and
	// hands it back in every event, and decides nothing from it.
	Meta Meta `db:"meta"`
	// Active says the plan is offered. An inactive plan keeps its
	// subscriptions and takes no new one.
	Active Flag `db:"active"`
	// Position orders the plans on the customer's screen, smallest first.
	Position int `db:"position"`
	// CreatedAt is when the row was written, in UTC.
	CreatedAt time.Time `db:"created_at"`
	// UpdatedAt is when the row last changed, in UTC.
	UpdatedAt time.Time `db:"updated_at"`
}

// planTable is the table Plan is a row of, declared once.
var planTable = model.NewTable(model.TableSpec{
	Name:      plansTable,
	New:       func() model.Entity { return new(Plan) },
	ManualKey: true,
	Global:    true,
})

// Coupon is a discount a customer types at checkout.
//
// Like the plans, the coupons are the platform's catalogue, Global, written by
// the platform's admin.
type Coupon struct {
	model.Model

	// ID is the identifier, written by the application.
	ID string `db:"id"`
	// Code is what the customer types, upper case. Unique.
	Code string `db:"code"`
	// Description is a note for the admin.
	Description string `db:"description"`
	// Kind is CouponPercent or CouponFixed.
	Kind CouponKind `db:"kind"`
	// Value is the discount: basis points for CouponPercent (1000 is 10%),
	// centavos for CouponFixed.
	Value int64 `db:"value"`
	// MaxUses is how many payments may use the coupon. Zero is unlimited.
	MaxUses int `db:"max_uses"`
	// Uses is how many payments have used it.
	Uses int `db:"uses"`
	// ValidFrom is the first instant it may be used. Nil is always.
	ValidFrom *time.Time `db:"valid_from"`
	// ValidUntil is the instant it stops being usable. Nil is never.
	ValidUntil *time.Time `db:"valid_until"`
	// Renewals is how many renewals after the first payment keep the discount.
	// Zero discounts the first payment alone.
	Renewals int `db:"renewals"`
	// MinPurchase is the smallest price, in centavos, the coupon applies to.
	MinPurchase int64 `db:"min_purchase"`
	// PlanID restricts the coupon to one plan. Empty is every plan.
	PlanID string `db:"plan_id"`
	// Active says the coupon is accepted. An inactive coupon still discounts
	// the renewals it was already granted to.
	Active Flag `db:"active"`
	// CreatedAt is when the row was written, in UTC.
	CreatedAt time.Time `db:"created_at"`
	// UpdatedAt is when the row last changed, in UTC.
	UpdatedAt time.Time `db:"updated_at"`
}

// couponTable is the table Coupon is a row of, declared once.
var couponTable = model.NewTable(model.TableSpec{
	Name:      couponsTable,
	New:       func() model.Entity { return new(Coupon) },
	ManualKey: true,
	Global:    true,
})

// Subscription is one account's plan, and the date it is paid until.
//
// It belongs to the tenant -- the customer's Organization -- written from the
// Grant of whoever subscribed. Reference is the consumer's name for what the
// subscription pays for inside the account, empty for the account itself.
type Subscription struct {
	model.Model

	// ID is the identifier, written by the application.
	ID string `db:"id"`
	// TenantID is the account the subscription belongs to.
	TenantID string `db:"tenant_id"`
	// Reference is what inside the account the subscription pays for, in the
	// consumer's own words ("camera:42"). Empty is the account itself.
	Reference string `db:"reference"`
	// PlanID is the plan subscribed to.
	PlanID string `db:"plan_id"`
	// Status is where the subscription stands.
	Status SubscriptionStatus `db:"status"`
	// CurrentPeriodStart is when the period being served began, in UTC.
	CurrentPeriodStart time.Time `db:"current_period_start"`
	// CurrentPeriodEnd is the instant the subscription is paid until -- "pago
	// até" -- in UTC: the end of the trial while it is trialing.
	CurrentPeriodEnd time.Time `db:"current_period_end"`
	// TrialEndsAt is when the trial ends, nil for a subscription that never
	// had one.
	TrialEndsAt *time.Time `db:"trial_ends_at"`
	// CancelAtPeriodEnd says the customer asked to stop: the subscription is
	// served until CurrentPeriodEnd and then canceled instead of renewed.
	CancelAtPeriodEnd Flag `db:"cancel_at_period_end"`
	// CanceledAt is when the cancellation was asked for, nil when it never was.
	CanceledAt *time.Time `db:"canceled_at"`
	// EndedAt is when the subscription stopped being served, nil while it is.
	EndedAt *time.Time `db:"ended_at"`
	// GraceDays is the plan's grace, copied when the subscription began, so a
	// plan changed later does not move a period already sold.
	GraceDays int `db:"grace_days"`
	// CouponID is the coupon whose discount the renewals keep, empty for none.
	CouponID string `db:"coupon_id"`
	// CouponRenewalsLeft is how many renewals still get that discount.
	CouponRenewalsLeft int `db:"coupon_renewals_left"`
	// Renewals is how many times it was renewed after the first payment.
	Renewals int `db:"renewals"`
	// Meta is what the consumer attached when it started the subscription.
	Meta Meta `db:"meta"`
	// CreatedBy is the user who started it.
	CreatedBy string `db:"created_by"`
	// CreatedAt is when the row was written, in UTC.
	CreatedAt time.Time `db:"created_at"`
	// UpdatedAt is when the row last changed, in UTC.
	UpdatedAt time.Time `db:"updated_at"`
}

// subscriptionTable is the table Subscription is a row of, scoped by
// tenant_id.
var subscriptionTable = model.NewTable(model.TableSpec{
	Name:      subscriptionsTable,
	New:       func() model.Entity { return new(Subscription) },
	ManualKey: true,
})

// Invoice is one charge: a plan's period, its discount, and what the provider
// was asked to collect.
//
// The ID is the order number a Provider is given, so a notification names the
// invoice it is about without naming the tenant: the tenant is read from the
// row.
type Invoice struct {
	model.Model

	// ID is the identifier, and the order number the provider is given.
	ID string `db:"id"`
	// TenantID is the account that owes it.
	TenantID string `db:"tenant_id"`
	// SubscriptionID is the subscription it renews or starts after a trial;
	// empty for a first payment, which creates the subscription.
	SubscriptionID string `db:"subscription_id"`
	// PlanID is the plan it charges for.
	PlanID string `db:"plan_id"`
	// PlanName is the plan's name when the invoice was issued.
	PlanName string `db:"plan_name"`
	// Reference is the subscription's Reference.
	Reference string `db:"reference"`
	// Kind is InvoiceNew or InvoiceRenewal.
	Kind InvoiceKind `db:"kind"`
	// Status is where the invoice stands.
	Status InvoiceStatus `db:"status"`
	// Months is how many months the payment buys.
	Months int `db:"months"`
	// Subtotal is the plan's price, in centavos.
	Subtotal int64 `db:"subtotal"`
	// Discount is the coupon's discount, in centavos.
	Discount int64 `db:"discount"`
	// Total is what is charged, in centavos.
	Total int64 `db:"total"`
	// CouponID is the coupon applied, empty for none.
	CouponID string `db:"coupon_id"`
	// CouponCode is the code applied, as typed, empty for none.
	CouponCode string `db:"coupon_code"`
	// RenewalNumber is which payment of the subscription this is: 1 for the
	// first, 2 for the first renewal.
	RenewalNumber int `db:"renewal_number"`
	// MaxInstallments is the most installments the provider may offer.
	MaxInstallments int `db:"max_installments"`
	// Provider is the name of the Provider that collects it.
	Provider string `db:"provider"`
	// ProviderRef is the provider's own identifier of the order or payment.
	ProviderRef string `db:"provider_ref"`
	// ProviderStatus is the last state the provider reported, as it said it.
	ProviderStatus string `db:"provider_status"`
	// CheckoutURL is where the customer pays, empty for a provider with no
	// page of its own.
	CheckoutURL string `db:"checkout_url"`
	// Meta is what the consumer attached to the checkout.
	Meta Meta `db:"meta"`
	// PaidAt is when the payment was confirmed, nil while open.
	PaidAt *time.Time `db:"paid_at"`
	// CreatedBy is the user who issued it.
	CreatedBy string `db:"created_by"`
	// CreatedAt is when the row was written, in UTC.
	CreatedAt time.Time `db:"created_at"`
	// UpdatedAt is when the row last changed, in UTC.
	UpdatedAt time.Time `db:"updated_at"`
}

// invoiceTable is the table Invoice is a row of, scoped by tenant_id.
var invoiceTable = model.NewTable(model.TableSpec{
	Name:      invoicesTable,
	New:       func() model.Entity { return new(Invoice) },
	ManualKey: true,
})

// Payment is the confirmation of one invoice. There is one per invoice at
// most, and the unique index on invoice_id is what makes a confirmation
// happen once however many notifications arrive.
type Payment struct {
	model.Model

	// ID is the identifier, written by the application.
	ID string `db:"id"`
	// TenantID is the account that paid.
	TenantID string `db:"tenant_id"`
	// InvoiceID is the invoice paid.
	InvoiceID string `db:"invoice_id"`
	// Provider is the provider that collected it, or ManualProviderName.
	Provider string `db:"provider"`
	// ProviderRef is the provider's identifier of the payment.
	ProviderRef string `db:"provider_ref"`
	// Method is how it was paid, in the provider's words: "credit_card",
	// "pix".
	Method string `db:"method"`
	// Installments is how many installments the customer chose.
	Installments int `db:"installments"`
	// Amount is what was paid, in centavos.
	Amount int64 `db:"amount"`
	// PaidAt is when it was paid, in UTC.
	PaidAt time.Time `db:"paid_at"`
	// ConfirmedBy is who confirmed it: "provider:<name>" or the admin's id.
	ConfirmedBy string `db:"confirmed_by"`
	// CreatedAt is when the row was written, in UTC.
	CreatedAt time.Time `db:"created_at"`
	// UpdatedAt is when the row last changed, in UTC.
	UpdatedAt time.Time `db:"updated_at"`
}

// paymentTable is the table Payment is a row of, scoped by tenant_id.
var paymentTable = model.NewTable(model.TableSpec{
	Name:      paymentsTable,
	New:       func() model.Entity { return new(Payment) },
	ManualKey: true,
})

// CouponUsage is one payment that used a coupon, with the discount it got.
type CouponUsage struct {
	model.Model

	// ID is the identifier, written by the application.
	ID string `db:"id"`
	// TenantID is the account that used it.
	TenantID string `db:"tenant_id"`
	// CouponID is the coupon used.
	CouponID string `db:"coupon_id"`
	// InvoiceID is the invoice it discounted.
	InvoiceID string `db:"invoice_id"`
	// PaymentID is the payment that confirmed that invoice.
	PaymentID string `db:"payment_id"`
	// Discount is what it took off, in centavos.
	Discount int64 `db:"discount"`
	// RenewalNumber is which payment of the subscription it discounted.
	RenewalNumber int `db:"renewal_number"`
	// UsedAt is when the payment was confirmed, in UTC.
	UsedAt time.Time `db:"used_at"`
	// CreatedAt is when the row was written, in UTC.
	CreatedAt time.Time `db:"created_at"`
	// UpdatedAt is when the row last changed, in UTC.
	UpdatedAt time.Time `db:"updated_at"`
}

// couponUsageTable is the table CouponUsage is a row of, scoped by tenant_id.
var couponUsageTable = model.NewTable(model.TableSpec{
	Name:      couponUsagesTable,
	New:       func() model.Entity { return new(CouponUsage) },
	ManualKey: true,
})

// arandu:begin custom

// The table names, declared once: the migrations and the queries read them
// from here, so a rename is one edit.
const (
	plansTable         = "billing_plans"
	couponsTable       = "billing_coupons"
	subscriptionsTable = "billing_subscriptions"
	invoicesTable      = "billing_invoices"
	paymentsTable      = "billing_payments"
	couponUsagesTable  = "billing_coupon_usages"
)

// Column widths, declared once for the migrations and the validation, so a
// value the form accepts is a value the column holds.
const (
	maxIdentifierLen = 64
	maxSlugLen       = 80
	maxNameLen       = 160
	maxCodeLen       = 40
	maxShortLen      = 40
	maxURLLen        = 2048
	maxTextLen       = 4000
)

// MaxInstallments is the most installments any plan may offer.
const MaxInstallments = 12

// MaxIntervalMonths is the longest period one payment may buy.
const MaxIntervalMonths = 120

// CouponKind is how a coupon's Value is read.
type CouponKind string

const (
	// CouponPercent takes Value basis points of the price off: 1000 is 10%.
	CouponPercent CouponKind = "percent"
	// CouponFixed takes Value centavos off, never more than the price.
	CouponFixed CouponKind = "fixed"
)

// SubscriptionStatus is where a subscription stands.
type SubscriptionStatus string

const (
	// SubscriptionTrialing is served without payment until TrialEndsAt.
	SubscriptionTrialing SubscriptionStatus = "trialing"
	// SubscriptionActive is paid until CurrentPeriodEnd.
	SubscriptionActive SubscriptionStatus = "active"
	// SubscriptionPastDue is past CurrentPeriodEnd, inside the plan's grace.
	SubscriptionPastDue SubscriptionStatus = "past_due"
	// SubscriptionCanceled was stopped by the customer at the end of a period,
	// or by the admin at once.
	SubscriptionCanceled SubscriptionStatus = "canceled"
	// SubscriptionExpired ended without a payment.
	SubscriptionExpired SubscriptionStatus = "expired"
)

// SubscriptionStatuses are every status, in the order a screen lists them.
var SubscriptionStatuses = []SubscriptionStatus{
	SubscriptionTrialing, SubscriptionActive, SubscriptionPastDue, SubscriptionCanceled, SubscriptionExpired,
}

// Serving says the subscription still gives access: trialing, active, or past
// due inside the grace.
func (s Subscription) Serving() bool {
	switch s.Status {
	case SubscriptionTrialing, SubscriptionActive, SubscriptionPastDue:
		return true
	}
	return false
}

// Ended says the subscription stopped and a payment starts a new period from
// the day it is made.
func (s Subscription) Ended() bool {
	return s.Status == SubscriptionCanceled || s.Status == SubscriptionExpired
}

// InvoiceKind says whether an invoice starts a subscription or renews one.
type InvoiceKind string

const (
	// InvoiceNew is the first payment of a subscription.
	InvoiceNew InvoiceKind = "new"
	// InvoiceRenewal extends a subscription that exists.
	InvoiceRenewal InvoiceKind = "renewal"
)

// InvoiceStatus is where an invoice stands.
type InvoiceStatus string

const (
	// InvoiceOpen waits for its payment.
	InvoiceOpen InvoiceStatus = "open"
	// InvoicePaid was confirmed, once.
	InvoicePaid InvoiceStatus = "paid"
	// InvoiceFailed was refused by the provider; a new checkout issues a new
	// invoice.
	InvoiceFailed InvoiceStatus = "failed"
	// InvoiceCanceled was voided by the admin or replaced.
	InvoiceCanceled InvoiceStatus = "canceled"
)

// InvoiceStatuses are every status, in the order a screen lists them.
var InvoiceStatuses = []InvoiceStatus{InvoiceOpen, InvoicePaid, InvoiceFailed, InvoiceCanceled}

// ErrNotFound is the answer for a record that is not there, or that belongs
// to another tenant: the two are one answer, so a probe learns nothing. It
// wraps model.ErrModelNotFound, which the router answers with 404.
var ErrNotFound = fmt.Errorf("billing: record not found: %w", model.ErrModelNotFound)

// Flag is a yes-or-no a column holds as an integer, read back from whatever an
// engine answers with, because the engines disagree about booleans.
type Flag bool

// Value writes the flag as the integer the column holds.
func (f Flag) Value() (driver.Value, error) {
	if f {
		return int64(1), nil
	}
	return int64(0), nil
}

// Scan reads the flag back. Anything it cannot read is refused rather than
// read as false.
func (f *Flag) Scan(value any) error {
	switch v := value.(type) {
	case nil:
		*f = false
	case bool:
		*f = Flag(v)
	case int64:
		*f = v != 0
	case int32:
		*f = v != 0
	case int:
		*f = v != 0
	case float64:
		*f = v != 0
	case []byte:
		return f.Scan(string(v))
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		if err != nil {
			return fmt.Errorf("billing: %q is not a yes-or-no", v)
		}
		*f = Flag(b)
	default:
		return fmt.Errorf("billing: a yes-or-no column answered with a %T", value)
	}
	return nil
}

// Meta is a set of names and values the consumer attaches, held as JSON text.
type Meta map[string]string

// Value writes the set as a JSON object; an empty set is "{}".
func (m Meta) Value() (driver.Value, error) {
	if len(m) == 0 {
		return "{}", nil
	}
	body, err := json.Marshal(map[string]string(m))
	return string(body), err
}

// Scan reads the set back from its JSON text.
func (m *Meta) Scan(value any) error {
	text, err := jsonText(value)
	if err != nil || text == "" {
		*m = Meta{}
		return err
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return fmt.Errorf("billing: a meta column does not hold a JSON object: %w", err)
	}
	*m = out
	return nil
}

// StringList is a list of lines held as a JSON array.
type StringList []string

// Value writes the list as a JSON array; an empty list is "[]".
func (l StringList) Value() (driver.Value, error) {
	if len(l) == 0 {
		return "[]", nil
	}
	body, err := json.Marshal([]string(l))
	return string(body), err
}

// Scan reads the list back from its JSON text.
func (l *StringList) Scan(value any) error {
	text, err := jsonText(value)
	if err != nil || text == "" {
		*l = nil
		return err
	}
	var out []string
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return fmt.Errorf("billing: a list column does not hold a JSON array: %w", err)
	}
	*l = out
	return nil
}

func jsonText(value any) (string, error) {
	switch v := value.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case []byte:
		return string(v), nil
	}
	return "", fmt.Errorf("billing: a JSON column answered with a %T", value)
}

// Installments is how many installments the plan may be paid in: one unless
// its period is at least InstallmentsFrom months, and then up to one per month
// of the period, never more than InstallmentsCap or MaxInstallments.
//
//	InstallmentsFrom 6, IntervalMonths 3  -> 1
//	InstallmentsFrom 6, IntervalMonths 6  -> 6
//	InstallmentsFrom 6, IntervalMonths 24 -> 12
func (p Plan) Installments() int {
	if p.InstallmentsFrom <= 0 || p.IntervalMonths < p.InstallmentsFrom {
		return 1
	}
	most := p.IntervalMonths
	limit := MaxInstallments
	if p.InstallmentsCap > 0 && p.InstallmentsCap < limit {
		limit = p.InstallmentsCap
	}
	if most > limit {
		most = limit
	}
	if most < 1 {
		return 1
	}
	return most
}

// Code normalizes a coupon code as it is stored and looked up: trimmed, upper
// case.
func Code(raw string) string { return strings.ToUpper(strings.TrimSpace(raw)) }

// arandu:end custom
