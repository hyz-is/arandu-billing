package billing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/translation"
	"github.com/hyz-is/arandu-billing/internal/everytenant"
)

// SubscribeRequest is what an account asks for when it pays for a plan.
//
// There is no TenantID here and there must never be one: the account is the
// one on the Grant, which came from the session.
type SubscribeRequest struct {
	// PlanID is the plan to pay for.
	PlanID string
	// CouponCode is the coupon typed, empty for none.
	CouponCode string
	// CycleMonths is the billing cycle the subscriber chose, in months: one of
	// the plan's OfferedCycles. Zero is the plan's own IntervalMonths. The
	// invoice charges the plan's price for those months with the cycle's
	// discount, and the subscription is paid for that many months.
	CycleMonths int
	// Units is how many units (seats, members) to charge for, the plan's
	// included ones among them; zero is the subscription's own units, or only
	// the included ones for a subscription that does not exist yet. Units
	// above what the plan includes are priced at its UnitPrice, and refused
	// when it has none. A subscription still being served changes its units
	// with SetUnits, so a different number here is refused.
	Units int
	// Reference is what inside the account the subscription pays for, in the
	// application's words; empty is the account itself. It is set by the
	// application's own code, never read from a customer's form by this
	// package.
	Reference string
	// Meta is the application's facts about this checkout.
	Meta Meta
	// Provider is the Name of the provider to collect with; empty is the
	// default.
	Provider string
}

// Validate reports the errors per field.
func (r SubscribeRequest) Validate() validation.Errors {
	e := validation.Errors{}
	validation.Required(e, "plan_id", r.PlanID)
	validation.MaxLen(e, "plan_id", r.PlanID, maxIdentifierLen)
	validation.MaxLen(e, "coupon", r.CouponCode, maxCodeLen)
	validation.MaxLen(e, "reference", r.Reference, maxIdentifierLen)
	if r.CycleMonths < 0 || r.CycleMonths > MaxIntervalMonths {
		e.Add("cycle", message("between", translation.Replace{"min": "0", "max": itoa(MaxIntervalMonths)}))
	}
	if r.Units < 0 || r.Units > MaxUnits {
		e.Add("units", message("between", translation.Replace{"min": "0", "max": itoa(MaxUnits)}))
	}
	if len(r.Meta) > 40 {
		e.Add("meta", message("meta_count", translation.Replace{"max": "40"}))
	}
	return e
}

// CheckoutResult is an issued invoice and where the customer pays it.
type CheckoutResult struct {
	// Invoice is the invoice, open -- or paid already, when a coupon took the
	// whole price off.
	Invoice *Invoice
	// URL is the gateway's page; empty means the invoice page.
	URL string
}

// servingStatuses are the statuses a subscription still gives access in.
var servingStatuses = []any{string(SubscriptionTrialing), string(SubscriptionActive), string(SubscriptionPastDue)}

// Subscribe issues the invoice of a plan for the asking account and asks the
// gateway for its page. When the account already has a subscription to the
// plan, for the same reference, that is still served, the invoice extends it.
func (s *BillingService) Subscribe(ctx context.Context, actor security.Subject, in SubscribeRequest) (*CheckoutResult, error) {
	if errs := in.Validate(); errs.Any() {
		return nil, errs
	}
	g, err := security.Authorize(ctx, s.policy, actor, Subscribe, Account{TenantID: actor.Tenant})
	if err != nil {
		return nil, err
	}
	plan, err := Plans(s.db).WhereKey(in.PlanID).Where("active", "=", 1).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	current, err := Subscriptions(s.db).Where("plan_id", "=", plan.ID).Where("reference", "=", strings.TrimSpace(in.Reference)).
		WhereIn("status", servingStatuses).OrderByDesc("current_period_end").FirstOrFail(ctx, g)
	if err != nil && !missing(err) {
		return nil, err
	}
	if err != nil {
		current = nil
	}
	return s.issue(ctx, g, actor, plan, current, in)
}

// Renew issues the invoice of a subscription's next period, in the billing
// cycle the subscription already has. A subscription that still has renewals
// of its coupon gets the discount again; one that ended is paid for as a new
// period from the day it is paid.
func (s *BillingService) Renew(ctx context.Context, actor security.Subject, subscriptionID, provider string) (*CheckoutResult, error) {
	g, err := security.Authorize(ctx, s.policy, actor, Subscribe, Account{TenantID: actor.Tenant})
	if err != nil {
		return nil, err
	}
	sub, err := Subscriptions(s.db).WhereKey(subscriptionID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	plan, err := Plans(s.db).WhereKey(sub.PlanID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if sub.Ended() && !bool(plan.Active) {
		return nil, validation.Errors{"plan_id": {message("plan_inactive")}}
	}
	return s.issue(ctx, g, actor, plan, sub, SubscribeRequest{PlanID: plan.ID, CycleMonths: sub.CycleMonthsFor(*plan), Units: sub.Units, Reference: sub.Reference, Meta: sub.Meta, Provider: provider})
}

// RenewCycle is Renew in a billing cycle chosen now: the change takes effect
// with this invoice and, once it is paid, every renewal after it keeps the new
// cycle; the period already paid for is never changed. Zero months keeps the
// subscription's own cycle, and a cycle the plan stopped offering is refused
// until another is chosen.
func (s *BillingService) RenewCycle(ctx context.Context, actor security.Subject, subscriptionID, provider string, cycleMonths int) (*CheckoutResult, error) {
	if cycleMonths < 0 || cycleMonths > MaxIntervalMonths {
		return nil, validation.Errors{"cycle": {message("between", translation.Replace{"min": "0", "max": itoa(MaxIntervalMonths)})}}
	}
	g, err := security.Authorize(ctx, s.policy, actor, Subscribe, Account{TenantID: actor.Tenant})
	if err != nil {
		return nil, err
	}
	sub, err := Subscriptions(s.db).WhereKey(subscriptionID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	plan, err := Plans(s.db).WhereKey(sub.PlanID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if sub.Ended() && !bool(plan.Active) {
		return nil, validation.Errors{"plan_id": {message("plan_inactive")}}
	}
	if cycleMonths == 0 {
		cycleMonths = sub.CycleMonthsFor(*plan)
	}
	return s.issue(ctx, g, actor, plan, sub, SubscribeRequest{PlanID: plan.ID, CycleMonths: cycleMonths, Units: sub.Units, Reference: sub.Reference, Meta: sub.Meta, Provider: provider})
}

// issue prices the period, writes the invoice, and asks the provider for its
// page. A price the coupon took to zero is confirmed at once.
func (s *BillingService) issue(ctx context.Context, g security.Grant, actor security.Subject, plan *Plan, sub *Subscription, in SubscribeRequest) (*CheckoutResult, error) {
	now := s.now()
	providerName := in.Provider
	if providerName == "" {
		providerName = s.fallback
	}
	provider := s.providers[providerName]
	if provider == nil {
		return nil, validation.Errors{"provider": {message("provider_unknown")}}
	}

	renewal := sub != nil && !sub.Ended() && sub.Status != SubscriptionTrialing
	var coupon *Coupon
	code := Code(in.CouponCode)
	switch {
	case code != "":
		c, err := Coupons(s.db).Where("code", "=", code).FirstOrFail(ctx, g)
		if err != nil {
			if missing(err) {
				return nil, validation.Errors{"coupon": {message("coupon_unknown")}}
			}
			return nil, err
		}
		coupon = c
		renewal = false
	case renewal && sub.CouponID != "" && sub.CouponRenewalsLeft > 0:
		c, err := Coupons(s.db).WhereKey(sub.CouponID).FirstOrFail(ctx, g)
		if err == nil {
			coupon = c
		} else if !missing(err) {
			return nil, err
		}
	}
	cycle, err := plan.Cycle(in.CycleMonths)
	if err != nil {
		return nil, validation.Errors{"cycle": {message("cycle_unavailable")}}
	}
	units := in.Units
	if sub != nil && !sub.Ended() && sub.Status != SubscriptionTrialing && units != 0 && units != sub.Units {
		return nil, validation.Errors{"units": {message("units_unavailable")}}
	}
	if units == 0 && sub != nil {
		units = sub.Units
	}
	quote, err := PriceUnits(*plan, cycle.Months, units, coupon, renewal && coupon != nil, now)
	if err != nil {
		if errors.Is(err, ErrUnitsUnavailable) {
			return nil, validation.Errors{"units": {message("units_unavailable")}}
		}
		return nil, couponRefusal(err)
	}

	kind, number, subID := InvoiceNew, 1, ""
	if sub != nil {
		subID = sub.ID
		if !sub.Ended() && sub.Status != SubscriptionTrialing {
			kind, number = InvoiceRenewal, sub.Renewals+2
		}
	}
	reference := strings.TrimSpace(in.Reference)

	// A checkout asked twice -- a double click, a back button -- answers the
	// open invoice it already issued rather than a second one.
	reused, err := Invoices(s.db).Where("status", "=", string(InvoiceOpen)).Where("plan_id", "=", plan.ID).
		Where("subscription_id", "=", subID).Where("reference", "=", reference).Where("months", "=", cycle.Months).Where("units", "=", units).Where("total", "=", quote.Total).
		Where("coupon_code", "=", couponCodeOf(coupon)).Where("provider", "=", providerName).
		Where("checkout_url", "!=", "").Where("created_at", ">=", now.Add(-24*time.Hour)).
		OrderByDesc("created_at").FirstOrFail(ctx, g)
	if err == nil {
		return &CheckoutResult{Invoice: reused, URL: reused.CheckoutURL}, nil
	} else if !missing(err) {
		return nil, err
	}

	inv, err := Invoices(s.db).New()
	if err != nil {
		return nil, err
	}
	if inv.ID, err = newID(); err != nil {
		return nil, err
	}
	inv.TenantID = data.Tenant(g)
	inv.SubscriptionID = subID
	inv.PlanID, inv.PlanName = plan.ID, plan.Name
	inv.Reference = reference
	inv.Kind, inv.Status = kind, InvoiceOpen
	inv.Months = cycle.Months
	inv.Subtotal, inv.CycleDiscount, inv.Discount, inv.Total = quote.Subtotal, quote.CycleDiscount, quote.Discount, quote.Total
	inv.Units, inv.UnitsAmount = units, quote.UnitsAmount
	if coupon != nil {
		inv.CouponID, inv.CouponCode = coupon.ID, coupon.Code
	}
	inv.RenewalNumber = number
	inv.MaxInstallments = quote.MaxInstallments
	inv.Provider = providerName
	inv.Meta = in.Meta
	if inv.Meta == nil {
		inv.Meta = Meta{}
	}
	inv.CreatedBy = actor.ID
	inv.CreatedAt, inv.UpdatedAt = now, now
	if _, err := inv.Save(ctx, g); err != nil {
		return nil, err
	}
	if err := s.record(ctx, g, actor, activityInvoiceCreated, "billing_invoice", inv.ID, map[string]any{
		"plan": plan.Slug, "cycle": inv.Months, "cycle_discount": inv.CycleDiscount, "total": inv.Total, "discount": inv.Discount,
		"units": inv.Units, "units_amount": inv.UnitsAmount, "coupon": inv.CouponCode, "provider": providerName,
	}); err != nil {
		return nil, err
	}

	if inv.Total == 0 {
		paid, _, err := s.confirm(ctx, g, actor, inv, PaymentStatus{State: StatePaid, Method: "coupon", Raw: "free"}, "coupon", actor.ID)
		if err != nil {
			return nil, err
		}
		return &CheckoutResult{Invoice: paid}, nil
	}

	customer := Customer{}
	if s.customer != nil {
		if customer, err = s.customer(ctx, actor); err != nil {
			return nil, err
		}
	}
	returnURL := ""
	if s.returnURL != nil {
		returnURL = s.returnURL(inv.ID)
	}
	meta := copyMeta(plan.Meta)
	for k, v := range inv.Meta {
		meta[k] = v
	}
	checkout, err := provider.CreateCheckout(ctx, CheckoutRequest{
		InvoiceID: inv.ID, Tenant: inv.TenantID,
		Description: s.labelsFor(ctx).describeInvoice(plan.Name, inv.Months),
		PlanID:      plan.ID, PlanSlug: plan.Slug, Months: inv.Months,
		Units: inv.Units, UnitsAmount: inv.UnitsAmount,
		Subtotal: inv.Subtotal, CycleDiscount: inv.CycleDiscount, Discount: inv.Discount, Total: inv.Total, CouponCode: inv.CouponCode,
		MaxInstallments: inv.MaxInstallments, Customer: customer, ReturnURL: returnURL, Meta: Meta(meta),
	})
	if err != nil {
		return nil, err
	}
	if checkout.URL != "" || checkout.Reference != "" {
		inv.CheckoutURL = checkout.URL
		inv.ProviderRef = checkout.Reference
		inv.UpdatedAt = s.now()
		if _, err := inv.Save(ctx, g); err != nil {
			return nil, err
		}
	}
	return &CheckoutResult{Invoice: inv, URL: checkout.URL}, nil
}

func couponCodeOf(c *Coupon) string {
	if c == nil {
		return ""
	}
	return c.Code
}

// SetUnits changes how many units (seats, members) one of the account's
// subscriptions is charged for from its next invoice on. It issues no invoice
// and collects nothing -- a gateway that holds the subscription prorates on its
// own side -- and an invoice already issued keeps the units it was priced
// with. The number is between 1 and MaxUnits and is refused above what the
// plan includes when the plan has no UnitPrice. The change is recorded as
// billing.subscription.units_changed with the units before and after.
func (s *BillingService) SetUnits(ctx context.Context, actor security.Subject, subscriptionID string, units int) (*Subscription, error) {
	if units < 1 || units > MaxUnits {
		return nil, validation.Errors{"units": {message("between", translation.Replace{"min": "1", "max": itoa(MaxUnits)})}}
	}
	g, err := security.Authorize(ctx, s.policy, actor, Subscribe, Account{TenantID: actor.Tenant})
	if err != nil {
		return nil, err
	}
	sub, err := Subscriptions(s.db).WhereKey(subscriptionID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	return s.applyUnits(ctx, g, actor, sub, units)
}

// applyUnits sets the units of a subscription already read under g, and
// records the change. Units the plan does not sell are refused, and a
// subscription already at that number is answered as it stands.
func (s *BillingService) applyUnits(ctx context.Context, g security.Grant, actor security.Subject, sub *Subscription, units int) (*Subscription, error) {
	plan, err := Plans(s.db).WhereKey(sub.PlanID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if _, err := UnitsPrice(*plan, 0, units); err != nil {
		return nil, validation.Errors{"units": {message("units_unavailable")}}
	}
	if sub.Units == units {
		return sub, nil
	}
	before := sub.Units
	now := s.now()
	claimed, err := Subscriptions(s.db).WhereKey(sub.ID).Where("units", "=", before).Update(ctx, g, map[string]any{"units": units, "updated_at": now})
	if err != nil {
		return nil, err
	}
	if claimed == 0 {
		// Somebody changed it first; answer what stands now.
		return Subscriptions(s.db).WhereKey(sub.ID).FirstOrFail(ctx, g)
	}
	sub.Units, sub.UpdatedAt = units, now
	return sub, s.record(ctx, g, actor, activityUnitsChanged, "billing_subscription", sub.ID, map[string]any{
		"before": before, "after": units,
	})
}

// StartTrial starts the trial of a plan for the asking account: once per plan
// and reference, and only for a plan that offers one.
func (s *BillingService) StartTrial(ctx context.Context, actor security.Subject, planID, reference string) (*Subscription, error) {
	g, err := security.Authorize(ctx, s.policy, actor, Subscribe, Account{TenantID: actor.Tenant})
	if err != nil {
		return nil, err
	}
	plan, err := Plans(s.db).WhereKey(planID).Where("active", "=", 1).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if plan.TrialDays <= 0 {
		return nil, validation.Errors{"plan_id": {message("no_trial")}}
	}
	reference = strings.TrimSpace(reference)
	if len(reference) > maxIdentifierLen {
		return nil, validation.Errors{"reference": {message("too_long")}}
	}
	used, err := Subscriptions(s.db).Where("plan_id", "=", plan.ID).Where("reference", "=", reference).Exists(ctx, g)
	if err != nil {
		return nil, err
	}
	if used {
		return nil, validation.Errors{"plan_id": {message("trial_used")}}
	}
	now := s.now()
	end := now.AddDate(0, 0, plan.TrialDays)
	sub, err := Subscriptions(s.db).New()
	if err != nil {
		return nil, err
	}
	if sub.ID, err = newID(); err != nil {
		return nil, err
	}
	sub.TenantID = data.Tenant(g)
	sub.Reference = reference
	sub.PlanID = plan.ID
	sub.Status = SubscriptionTrialing
	sub.CurrentPeriodStart, sub.CurrentPeriodEnd = now, end
	sub.TrialEndsAt = &end
	sub.GraceDays = plan.GraceDays
	sub.Meta = Meta{}
	sub.CreatedBy = actor.ID
	sub.CreatedAt, sub.UpdatedAt = now, now
	err = data.Transaction(ctx, s.db, func(ctx context.Context) error {
		if _, err := sub.Save(ctx, g); err != nil {
			return err
		}
		if err := s.record(ctx, g, actor, activitySubscriptionStarted, "billing_subscription", sub.ID, map[string]any{"plan": plan.Slug, "status": string(sub.Status)}); err != nil {
			return err
		}
		return s.emit(ctx, Event{Name: EventTrialStarted, Tenant: sub.TenantID, At: now,
			Subscription: NewSubscriptionResource(*sub), Plan: NewPlanResource(*plan), Grant: g})
	})
	if err != nil {
		return nil, err
	}
	return sub, nil
}

// CancelAtPeriodEnd asks one of the account's subscriptions to stop when the
// period it is paid for ends: it is served until then and not renewed.
func (s *BillingService) CancelAtPeriodEnd(ctx context.Context, actor security.Subject, id string) (*Subscription, error) {
	g, err := security.Authorize(ctx, s.policy, actor, SubscriptionsCancel, Account{TenantID: actor.Tenant})
	if err != nil {
		return nil, err
	}
	sub, err := Subscriptions(s.db).WhereKey(id).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if !sub.Serving() {
		return nil, validation.Errors{"subscription": {message("not_serving")}}
	}
	if sub.CancelAtPeriodEnd {
		return sub, nil
	}
	now := s.now()
	sub.CancelAtPeriodEnd = true
	sub.CanceledAt = &now
	sub.UpdatedAt = now
	if _, err := sub.Save(ctx, g); err != nil {
		return nil, err
	}
	return sub, s.record(ctx, g, actor, activityCancelScheduled, "billing_subscription", sub.ID, map[string]any{
		"until": sub.CurrentPeriodEnd.UTC().Format(time.RFC3339),
	})
}

// ResumeSubscription takes back a cancellation asked for, while the period is
// still being served.
func (s *BillingService) ResumeSubscription(ctx context.Context, actor security.Subject, id string) (*Subscription, error) {
	g, err := security.Authorize(ctx, s.policy, actor, SubscriptionsCancel, Account{TenantID: actor.Tenant})
	if err != nil {
		return nil, err
	}
	sub, err := Subscriptions(s.db).WhereKey(id).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if !sub.Serving() {
		return nil, validation.Errors{"subscription": {message("not_serving")}}
	}
	if !sub.CancelAtPeriodEnd {
		return sub, nil
	}
	sub.CancelAtPeriodEnd = false
	sub.CanceledAt = nil
	sub.UpdatedAt = s.now()
	if _, err := sub.Save(ctx, g); err != nil {
		return nil, err
	}
	return sub, s.record(ctx, g, actor, activityCancelWithdrawn, "billing_subscription", sub.ID, nil)
}

// CancelNow is the admin canceling one account's subscription at once.
func (s *BillingService) CancelNow(ctx context.Context, actor security.Subject, id string) (*Subscription, error) {
	probe, err := security.Authorize(ctx, s.policy, actor, SubscriptionsManage, Account{})
	if err != nil {
		return nil, err
	}
	row, err := everytenant.Subscriptions(s.db).WhereKey(id).FirstOrFail(ctx, probe)
	if err != nil {
		return nil, notFound(err)
	}
	g, err := security.Authorize(ctx, s.policy, actingIn(actor, row.TenantID), SubscriptionsManage, Account{TenantID: row.TenantID})
	if err != nil {
		return nil, err
	}
	sub, err := Subscriptions(s.db).WhereKey(row.ID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if sub.Ended() {
		return sub, nil
	}
	if err := s.move(ctx, g, actingIn(actor, row.TenantID), sub, SubscriptionCanceled, s.now()); err != nil {
		return nil, err
	}
	return sub, nil
}

// move puts a subscription in another status -- lapsed, ended, or served again
// when the gateway that holds it says so --, records it and tells the Listener,
// in one transaction.
func (s *BillingService) move(ctx context.Context, g security.Grant, actor security.Subject, sub *Subscription, to SubscriptionStatus, now time.Time) error {
	from := sub.Status
	plan, err := Plans(s.db).WhereKey(sub.PlanID).FirstOrFail(ctx, g)
	if err != nil {
		return notFound(err)
	}
	return data.Transaction(ctx, s.db, func(ctx context.Context) error {
		claimed, err := Subscriptions(s.db).WhereKey(sub.ID).Where("status", "=", string(from)).Update(ctx, g, map[string]any{
			"status": string(to), "updated_at": now,
		})
		if err != nil {
			return err
		}
		if claimed == 0 {
			// Somebody moved it first: a payment, or another sweep.
			return nil
		}
		sub.Status = to
		sub.UpdatedAt = now
		if to == SubscriptionCanceled || to == SubscriptionExpired {
			sub.EndedAt = &now
			if to == SubscriptionCanceled && sub.CanceledAt == nil {
				sub.CanceledAt = &now
			}
			if _, err := sub.Save(ctx, g); err != nil {
				return err
			}
		}
		if to == SubscriptionActive {
			sub.EndedAt, sub.CanceledAt, sub.CancelAtPeriodEnd = nil, nil, false
			if _, err := sub.Save(ctx, g); err != nil {
				return err
			}
		}
		if err := s.record(ctx, g, actor, activitySubscriptionMoved, "billing_subscription", sub.ID, map[string]any{
			"before": string(from), "after": string(to),
		}); err != nil {
			return err
		}
		name := EventExpired
		switch to {
		case SubscriptionPastDue:
			name = EventPastDue
		case SubscriptionCanceled:
			name = EventCanceled
		case SubscriptionActive:
			name = EventActivated
		}
		return s.emit(ctx, Event{Name: name, Tenant: sub.TenantID, At: now,
			Subscription: NewSubscriptionResource(*sub), Plan: NewPlanResource(*plan), Grant: g})
	})
}

// sweepBatch is how many subscriptions one read of SweepDue looks at.
const sweepBatch = 200

// SweepDue moves every subscription whose period ended by now, in every
// account: a trial or a period that ended goes past due, expires, or is
// canceled when its cancellation was asked for (see Due). It is what the
// module's scheduled task runs; it answers how many it moved.
func (s *BillingService) SweepDue(ctx context.Context, now time.Time) (int, error) {
	probe, err := security.Authorize(ctx, s.policy, systemSubject(lookupTenant), SubscriptionsSweep, Account{})
	if err != nil {
		return 0, err
	}
	now = now.UTC()
	moved, after := 0, ""
	for {
		q := everytenant.Subscriptions(s.db).WhereIn("status", servingStatuses).Where("current_period_end", "<=", now)
		if after != "" {
			q = q.Where("id", ">", after)
		}
		rows, err := q.OrderBy("id").Limit(sweepBatch).Get(ctx, probe)
		if err != nil {
			return moved, err
		}
		for _, row := range rows {
			after = row.ID
			n, err := s.sweepOne(ctx, row.TenantID, row.ID, now)
			if err != nil {
				return moved, err
			}
			moved += n
		}
		if len(rows) < sweepBatch {
			return moved, nil
		}
	}
}

// sweepOne moves one subscription, under a Grant for its own account.
func (s *BillingService) sweepOne(ctx context.Context, tenant, id string, now time.Time) (int, error) {
	who := systemSubject(tenant)
	g, err := security.Authorize(ctx, s.policy, who, SubscriptionsSweep, Account{TenantID: tenant})
	if err != nil {
		return 0, err
	}
	sub, err := Subscriptions(s.db).WhereKey(id).FirstOrFail(ctx, g)
	if err != nil {
		return 0, notFound(err)
	}
	t := Due(*sub, now)
	if !t.Moved {
		return 0, nil
	}
	if err := s.move(ctx, g, who, sub, t.To, now); err != nil {
		return 0, err
	}
	return 1, nil
}

// ServingSubscriptions are the asking account's subscriptions that still give
// access -- trialing, active, or past due inside the grace -- which is what an
// application asks before it opens what a plan pays for.
func (s *BillingService) ServingSubscriptions(ctx context.Context, actor security.Subject) ([]*Subscription, error) {
	g, err := security.Authorize(ctx, s.policy, actor, SubscriptionsView, Account{TenantID: actor.Tenant})
	if err != nil {
		return nil, err
	}
	return Subscriptions(s.db).WhereIn("status", servingStatuses).OrderBy("current_period_end").Limit(MaxPageSize).Get(ctx, g)
}

// SubscriptionsOf are the serving subscriptions of an account, for the
// application's own code acting under a Grant its own policy issued for that
// account -- a job, the engine's sync -- rather than for a session.
func (s *BillingService) SubscriptionsOf(ctx context.Context, g security.Grant) ([]*Subscription, error) {
	tenant := data.Tenant(g)
	if err := g.Check(g.Action()); err != nil || tenant == "" {
		return nil, security.ErrForbidden
	}
	sg, err := security.Authorize(ctx, s.policy, security.Subject{ID: "app:" + g.Subject().ID, Tenant: tenant}, SubscriptionsView, Account{TenantID: tenant})
	if err != nil {
		return nil, err
	}
	return Subscriptions(s.db).WhereIn("status", servingStatuses).OrderBy("current_period_end").Limit(MaxPageSize).Get(ctx, sg)
}

// couponRefusal is the rejected coupon field, with the reason in the
// customer's language.
func couponRefusal(err error) validation.Errors {
	switch err {
	case ErrCouponInactive:
		return validation.Errors{"coupon": {message("coupon_inactive")}}
	case ErrCouponNotStarted:
		return validation.Errors{"coupon": {message("coupon_not_started")}}
	case ErrCouponExpired:
		return validation.Errors{"coupon": {message("coupon_expired")}}
	case ErrCouponExhausted:
		return validation.Errors{"coupon": {message("coupon_exhausted")}}
	case ErrCouponWrongPlan:
		return validation.Errors{"coupon": {message("coupon_wrong_plan")}}
	case ErrCouponMinPurchase:
		return validation.Errors{"coupon": {message("coupon_min_purchase")}}
	}
	return validation.Errors{"coupon": {message("coupon_unknown")}}
}
