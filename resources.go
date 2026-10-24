package billing

import (
	"strconv"
	"time"
)

// The response snapshots: what may leave the package, copied out of a row so
// they hold no connection and expose no Model internals. A JSON answer, an
// Event and a screen are built from these.

// PlanResource is one plan as it may leave.
type PlanResource struct {
	ID, Slug, Name, Description      string
	Features                         []string
	Price                            int64
	IntervalMonths, TrialDays        int
	GraceDays, InstallmentsFrom      int
	InstallmentsCap, MaxInstallments int
	Meta                             map[string]string
	Active                           bool
	Position                         int
	CreatedAt, UpdatedAt             time.Time
}

// NewPlanResource copies a plan.
func NewPlanResource(p Plan) PlanResource {
	return PlanResource{
		ID: p.ID, Slug: p.Slug, Name: p.Name, Description: p.Description,
		Features: append([]string(nil), p.Features...), Price: p.Price,
		IntervalMonths: p.IntervalMonths, TrialDays: p.TrialDays, GraceDays: p.GraceDays,
		InstallmentsFrom: p.InstallmentsFrom, InstallmentsCap: p.InstallmentsCap,
		MaxInstallments: p.Installments(), Meta: copyMeta(p.Meta), Active: bool(p.Active),
		Position: p.Position, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
}

// ToArray is the plan's fields, by name.
func (r PlanResource) ToArray() map[string]any {
	return map[string]any{
		"id": r.ID, "slug": r.Slug, "name": r.Name, "description": r.Description,
		"features": r.Features, "price": r.Price, "interval_months": r.IntervalMonths,
		"trial_days": r.TrialDays, "grace_days": r.GraceDays, "installments_from": r.InstallmentsFrom,
		"installments_cap": r.InstallmentsCap, "max_installments": r.MaxInstallments,
		"meta": r.Meta, "active": r.Active, "position": r.Position,
	}
}

// With is nothing beside the plan.
func (PlanResource) With() map[string]any { return nil }

// CouponResource is one coupon as it may leave.
type CouponResource struct {
	ID, Code, Description, PlanID string
	Kind                          CouponKind
	Value, MinPurchase            int64
	MaxUses, Uses, Renewals       int
	ValidFrom, ValidUntil         *time.Time
	Active                        bool
}

// NewCouponResource copies a coupon.
func NewCouponResource(c Coupon) CouponResource {
	return CouponResource{
		ID: c.ID, Code: c.Code, Description: c.Description, PlanID: c.PlanID, Kind: c.Kind,
		Value: c.Value, MinPurchase: c.MinPurchase, MaxUses: c.MaxUses, Uses: c.Uses,
		Renewals: c.Renewals, ValidFrom: c.ValidFrom, ValidUntil: c.ValidUntil, Active: bool(c.Active),
	}
}

// ToArray is the coupon's fields, by name.
func (r CouponResource) ToArray() map[string]any {
	return map[string]any{
		"id": r.ID, "code": r.Code, "description": r.Description, "plan_id": r.PlanID,
		"kind": string(r.Kind), "value": r.Value, "min_purchase": r.MinPurchase,
		"max_uses": r.MaxUses, "uses": r.Uses, "renewals": r.Renewals,
		"valid_from": timeText(r.ValidFrom), "valid_until": timeText(r.ValidUntil), "active": r.Active,
	}
}

// With is nothing beside the coupon.
func (CouponResource) With() map[string]any { return nil }

// SubscriptionResource is one subscription as it may leave.
type SubscriptionResource struct {
	ID, TenantID, Reference, PlanID      string
	Status                               SubscriptionStatus
	CurrentPeriodStart, CurrentPeriodEnd time.Time
	TrialEndsAt, CanceledAt, EndedAt     *time.Time
	CancelAtPeriodEnd                    bool
	GraceDays, Renewals                  int
	CouponID                             string
	CouponRenewalsLeft                   int
	Meta                                 map[string]string
	CreatedAt                            time.Time
}

// NewSubscriptionResource copies a subscription.
func NewSubscriptionResource(s Subscription) SubscriptionResource {
	return SubscriptionResource{
		ID: s.ID, TenantID: s.TenantID, Reference: s.Reference, PlanID: s.PlanID, Status: s.Status,
		CurrentPeriodStart: s.CurrentPeriodStart, CurrentPeriodEnd: s.CurrentPeriodEnd,
		TrialEndsAt: s.TrialEndsAt, CanceledAt: s.CanceledAt, EndedAt: s.EndedAt,
		CancelAtPeriodEnd: bool(s.CancelAtPeriodEnd), GraceDays: s.GraceDays, Renewals: s.Renewals,
		CouponID: s.CouponID, CouponRenewalsLeft: s.CouponRenewalsLeft, Meta: copyMeta(s.Meta),
		CreatedAt: s.CreatedAt,
	}
}

// ToArray is the subscription's fields, by name. The tenant stays out: the
// caller already knows its own, and the admin reads it on the screen.
func (r SubscriptionResource) ToArray() map[string]any {
	return map[string]any{
		"id": r.ID, "reference": r.Reference, "plan_id": r.PlanID, "status": string(r.Status),
		"current_period_start": r.CurrentPeriodStart.UTC().Format(time.RFC3339),
		"current_period_end":   r.CurrentPeriodEnd.UTC().Format(time.RFC3339),
		"trial_ends_at":        timeText(r.TrialEndsAt), "canceled_at": timeText(r.CanceledAt),
		"ended_at": timeText(r.EndedAt), "cancel_at_period_end": r.CancelAtPeriodEnd,
		"renewals": r.Renewals, "meta": r.Meta,
	}
}

// With is nothing beside the subscription.
func (SubscriptionResource) With() map[string]any { return nil }

// InvoiceResource is one invoice as it may leave.
type InvoiceResource struct {
	ID, TenantID, SubscriptionID, PlanID, PlanName, Reference string
	Kind                                                      InvoiceKind
	Status                                                    InvoiceStatus
	Months, RenewalNumber, MaxInstallments                    int
	Subtotal, Discount, Total                                 int64
	CouponCode, Provider, ProviderRef, CheckoutURL            string
	Meta                                                      map[string]string
	PaidAt                                                    *time.Time
	CreatedAt                                                 time.Time
}

// NewInvoiceResource copies an invoice.
func NewInvoiceResource(i Invoice) InvoiceResource {
	return InvoiceResource{
		ID: i.ID, TenantID: i.TenantID, SubscriptionID: i.SubscriptionID, PlanID: i.PlanID,
		PlanName: i.PlanName, Reference: i.Reference, Kind: i.Kind, Status: i.Status,
		Months: i.Months, RenewalNumber: i.RenewalNumber, MaxInstallments: i.MaxInstallments,
		Subtotal: i.Subtotal, Discount: i.Discount, Total: i.Total, CouponCode: i.CouponCode,
		Provider: i.Provider, ProviderRef: i.ProviderRef, CheckoutURL: i.CheckoutURL,
		Meta: copyMeta(i.Meta), PaidAt: i.PaidAt, CreatedAt: i.CreatedAt,
	}
}

// ToArray is the invoice's fields, by name.
func (r InvoiceResource) ToArray() map[string]any {
	return map[string]any{
		"id": r.ID, "subscription_id": r.SubscriptionID, "plan_id": r.PlanID, "plan_name": r.PlanName,
		"reference": r.Reference, "kind": string(r.Kind), "status": string(r.Status), "months": r.Months,
		"renewal_number": r.RenewalNumber, "max_installments": r.MaxInstallments,
		"subtotal": r.Subtotal, "discount": r.Discount, "total": r.Total, "coupon_code": r.CouponCode,
		"provider": r.Provider, "checkout_url": r.CheckoutURL, "paid_at": timeText(r.PaidAt),
		"created_at": r.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// With is nothing beside the invoice.
func (InvoiceResource) With() map[string]any { return nil }

// list is a page of snapshots as a JSON answer.
type list struct {
	items []map[string]any
	total int
}

// ToArray is the page's items.
func (l list) ToArray() map[string]any { return map[string]any{"items": l.items} }

// With is the total beside them.
func (l list) With() map[string]any { return map[string]any{"total": l.total} }

func copyMeta(m Meta) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func timeText(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UTC().Format(time.RFC3339)
}

func itoa(n int) string { return strconv.Itoa(n) }
