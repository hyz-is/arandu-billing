// Package everytenant is the billing read without the tenant filter: the
// admin's lists of every account's subscriptions and invoices, the lookup of
// the invoice a gateway's notification names, and the clock's search for the
// periods that ended -- each after the billing policy allowed it.
//
// It is internal so no application can import it. Its tables are Global, which
// is how Hesape spells a table with no tenant filter, and any Grant at all
// would read every tenant through them; the package that may reach them is
// the one whose only callers have asked the policy first. Their rows are
// copied into the billing package's own types, and nothing here writes: every
// write runs through the tenant-scoped models, under a Grant for the row's own
// tenant.
package everytenant

import (
	"time"

	"github.com/arandu-io/hesape/database/model"
)

// Subscription is a row of billing_subscriptions, read across tenants.
type Subscription struct {
	model.Model

	ID                 string     `db:"id"`
	TenantID           string     `db:"tenant_id"`
	Reference          string     `db:"reference"`
	PlanID             string     `db:"plan_id"`
	Status             string     `db:"status"`
	CurrentPeriodStart time.Time  `db:"current_period_start"`
	CurrentPeriodEnd   time.Time  `db:"current_period_end"`
	TrialEndsAt        *time.Time `db:"trial_ends_at"`
	CancelAtPeriodEnd  int64      `db:"cancel_at_period_end"`
	CanceledAt         *time.Time `db:"canceled_at"`
	EndedAt            *time.Time `db:"ended_at"`
	GraceDays          int        `db:"grace_days"`
	CouponID           string     `db:"coupon_id"`
	CouponRenewalsLeft int        `db:"coupon_renewals_left"`
	Renewals           int        `db:"renewals"`
	CreatedBy          string     `db:"created_by"`
	CreatedAt          time.Time  `db:"created_at"`
	UpdatedAt          time.Time  `db:"updated_at"`
}

var subscriptionTable = model.NewTable(model.TableSpec{
	Name:      "billing_subscriptions",
	New:       func() model.Entity { return new(Subscription) },
	ManualKey: true,
	Global:    true,
})

// Invoice is a row of billing_invoices, read across tenants.
type Invoice struct {
	model.Model

	ID              string     `db:"id"`
	TenantID        string     `db:"tenant_id"`
	SubscriptionID  string     `db:"subscription_id"`
	PlanID          string     `db:"plan_id"`
	PlanName        string     `db:"plan_name"`
	Reference       string     `db:"reference"`
	Kind            string     `db:"kind"`
	Status          string     `db:"status"`
	Months          int        `db:"months"`
	Subtotal        int64      `db:"subtotal"`
	Discount        int64      `db:"discount"`
	Total           int64      `db:"total"`
	CouponID        string     `db:"coupon_id"`
	CouponCode      string     `db:"coupon_code"`
	RenewalNumber   int        `db:"renewal_number"`
	MaxInstallments int        `db:"max_installments"`
	Provider        string     `db:"provider"`
	ProviderRef     string     `db:"provider_ref"`
	ProviderStatus  string     `db:"provider_status"`
	CheckoutURL     string     `db:"checkout_url"`
	PaidAt          *time.Time `db:"paid_at"`
	CreatedBy       string     `db:"created_by"`
	CreatedAt       time.Time  `db:"created_at"`
	UpdatedAt       time.Time  `db:"updated_at"`
}

var invoiceTable = model.NewTable(model.TableSpec{
	Name:      "billing_invoices",
	New:       func() model.Entity { return new(Invoice) },
	ManualKey: true,
	Global:    true,
})

// arandu:begin custom
// arandu:end custom
