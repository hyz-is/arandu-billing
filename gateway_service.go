package billing

import (
	"context"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/hyz-is/arandu-billing/internal/everytenant"
)

// What a gateway does on its own side -- renewing a subscription it holds,
// moving it to another state, changing the units it charges -- reaches this
// package as a notification, and is recorded here. None of it has a session
// behind it: every act runs as this package's system subject under the
// RenewalsRecord action, in the tenant of the subscription found by the
// gateway's identifier of it, and the gateway is asked again before money is
// believed.

// locateSubscription finds the subscription a gateway's identifier names,
// across every account, and answers the system subject of its account with the
// Grant RenewalsRecord gave it and the subscription as that account reads it.
func (s *BillingService) locateSubscription(ctx context.Context, providerName, ref string) (security.Grant, security.Subject, *Subscription, error) {
	probe, err := security.Authorize(ctx, s.policy, systemSubject(lookupTenant), RenewalsRecord, Account{})
	if err != nil {
		return security.Grant{}, security.Subject{}, nil, err
	}
	row, err := everytenant.Subscriptions(s.db).Where("provider", "=", providerName).Where("provider_ref", "=", ref).FirstOrFail(ctx, probe)
	if err != nil {
		return security.Grant{}, security.Subject{}, nil, notFound(err)
	}
	who := systemSubject(row.TenantID)
	g, err := security.Authorize(ctx, s.policy, who, RenewalsRecord, Account{TenantID: row.TenantID})
	if err != nil {
		return security.Grant{}, security.Subject{}, nil, err
	}
	sub, err := Subscriptions(s.db).WhereKey(row.ID).FirstOrFail(ctx, g)
	if err != nil {
		return security.Grant{}, security.Subject{}, nil, notFound(err)
	}
	return g, who, sub, nil
}

// SetProviderUnits is SetUnits for the system: the gateway that holds the
// subscription named by providerRef says it now charges for units units, and
// the subscription follows it. It is what a notification carrying
// Notification.Units does, and it answers ErrNotFound for a subscription no
// account holds under that gateway.
func (s *BillingService) SetProviderUnits(ctx context.Context, providerName, providerRef string, units int) (*Subscription, error) {
	if units < 1 || units > MaxUnits {
		return nil, validation.Errors{"units": {message("units_unavailable")}}
	}
	probe, err := security.Authorize(ctx, s.policy, systemSubject(lookupTenant), RenewalsRecord, Account{})
	if err != nil {
		return nil, err
	}
	row, err := everytenant.Subscriptions(s.db).Where("provider", "=", providerName).Where("provider_ref", "=", strings.TrimSpace(providerRef)).FirstOrFail(ctx, probe)
	if err != nil {
		return nil, notFound(err)
	}
	who := systemSubject(row.TenantID)
	g, err := security.Authorize(ctx, s.policy, who, RenewalsRecord, Account{TenantID: row.TenantID})
	if err != nil {
		return nil, err
	}
	sub, err := Subscriptions(s.db).WhereKey(row.ID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	return s.applyUnits(ctx, g, who, sub, units)
}

// subscriptionStated is what a notification about a subscription's state
// does: the subscription the gateway names is taken to the state it says, once
// -- the same state again changes nothing -- and the units it reports, when it
// reports any, are kept.
//
// past_due is taken from an active or trialing subscription alone; expired and
// canceled end a subscription that is still served and leave an ended one as it
// is; active serves a past due or ended subscription again, and only when the
// period it is paid until is still ahead. Anything else changes nothing.
func (s *BillingService) subscriptionStated(ctx context.Context, providerName string, n *Notification) error {
	g, who, sub, err := s.locateSubscription(ctx, providerName, n.SubscriptionRef)
	if err != nil {
		return err
	}
	now := s.now()
	var to SubscriptionStatus
	switch n.SubscriptionState {
	case SubscriptionPastDue:
		if sub.Status == SubscriptionActive || sub.Status == SubscriptionTrialing {
			to = SubscriptionPastDue
		}
	case SubscriptionExpired:
		if !sub.Ended() {
			to = SubscriptionExpired
		}
	case SubscriptionCanceled:
		if !sub.Ended() {
			to = SubscriptionCanceled
		}
	case SubscriptionActive:
		if (sub.Status == SubscriptionPastDue || sub.Ended()) && sub.CurrentPeriodEnd.After(now) {
			to = SubscriptionActive
		}
	}
	if to != "" {
		if err := s.move(ctx, g, who, sub, to, now); err != nil {
			return err
		}
	}
	if n.Units > 0 {
		if _, err := s.applyUnits(ctx, g, who, sub, n.Units); err != nil {
			return err
		}
	}
	return nil
}

// renewalPaidAhead says the period a subscription is paid until already covers
// a charge: it is active and paid for more than half a billing cycle ahead,
// which is where a renewal charged twice finds it and where a renewal that is
// due never does.
func renewalPaidAhead(sub *Subscription, months int, now time.Time) bool {
	if sub.Status != SubscriptionActive {
		return false
	}
	cycle := AddMonths(now, months).Sub(now)
	return sub.CurrentPeriodEnd.Sub(now) > cycle/2
}

// renewalCharged is what a notification does when the gateway charged the
// renewal of a subscription it holds and no invoice of this package has that
// charge: the invoice is issued here, with no checkout asked for, at the price
// of the subscription's cycle and units, and confirmed as any other -- once the
// gateway is asked and answers the amount the invoice charges.
//
// The charge's reference is what makes it happen once. The invoice is recorded
// under it with a unique ChargeRef, so of two notifications racing one issues
// the invoice and the other finds it. A subscription already paid for more
// than half a cycle ahead is not charged again: nothing is issued and the
// answer is a nil invoice.
func (s *BillingService) renewalCharged(ctx context.Context, provider Provider, n *Notification) (*Invoice, error) {
	name := provider.Name()
	g, who, sub, err := s.locateSubscription(ctx, name, n.SubscriptionRef)
	if err != nil {
		return nil, err
	}
	cg, err := security.Authorize(ctx, s.policy, who, PaymentsConfirm, Account{TenantID: sub.TenantID})
	if err != nil {
		return nil, err
	}
	plan, err := Plans(s.db).WhereKey(sub.PlanID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	now := s.now()
	months := sub.CycleMonthsFor(*plan)
	if renewalPaidAhead(sub, months, now) {
		return nil, nil
	}
	units := sub.Units
	if n.Units > 0 {
		if sub, err = s.applyUnits(ctx, g, who, sub, n.Units); err != nil {
			return nil, err
		}
		units = n.Units
	}

	renewal := !sub.Ended() && sub.Status != SubscriptionTrialing
	var coupon *Coupon
	if renewal && sub.CouponID != "" && sub.CouponRenewalsLeft > 0 {
		c, err := Coupons(s.db).WhereKey(sub.CouponID).FirstOrFail(ctx, g)
		if err == nil {
			coupon = c
		} else if !missing(err) {
			return nil, err
		}
	}
	quote, err := PriceUnits(*plan, months, units, coupon, renewal && coupon != nil, now)
	if err != nil {
		if coupon == nil {
			return nil, validation.Errors{"units": {message("units_unavailable")}}
		}
		return nil, couponRefusal(err)
	}

	kind, number := InvoiceNew, 1
	if renewal {
		kind, number = InvoiceRenewal, sub.Renewals+2
	}
	inv, err := Invoices(s.db).New()
	if err != nil {
		return nil, err
	}
	if inv.ID, err = newID(); err != nil {
		return nil, err
	}
	inv.TenantID = data.Tenant(g)
	inv.SubscriptionID = sub.ID
	inv.PlanID, inv.PlanName = plan.ID, plan.Name
	inv.Reference = sub.Reference
	inv.Kind, inv.Status = kind, InvoiceOpen
	inv.Months = months
	inv.Subtotal, inv.CycleDiscount, inv.Discount, inv.Total = quote.Subtotal, quote.CycleDiscount, quote.Discount, quote.Total
	inv.Units, inv.UnitsAmount = units, quote.UnitsAmount
	if coupon != nil {
		inv.CouponID, inv.CouponCode = coupon.ID, coupon.Code
	}
	inv.RenewalNumber = number
	inv.MaxInstallments = quote.MaxInstallments
	inv.Provider = name
	inv.ProviderRef = clip(n.Reference, maxIdentifierLen)
	inv.ChargeRef = clip(n.Reference, maxIdentifierLen)
	inv.Meta = Meta(copyMeta(sub.Meta))
	inv.CreatedBy = systemSubjectID
	inv.CreatedAt, inv.UpdatedAt = now, now
	if _, err := inv.Save(ctx, g); err != nil {
		// The unique charge says another notification of the same charge
		// issued the invoice first; it is the one to settle.
		issued, found := Invoices(s.db).Where("provider", "=", name).Where("charge_ref", "=", inv.ChargeRef).FirstOrFail(ctx, g)
		if found != nil {
			return nil, err
		}
		return s.settle(ctx, cg, who, provider, issued, n)
	}
	if err := s.record(ctx, g, who, activityInvoiceCreated, "billing_invoice", inv.ID, map[string]any{
		"plan": plan.Slug, "cycle": inv.Months, "cycle_discount": inv.CycleDiscount, "total": inv.Total, "discount": inv.Discount,
		"units": inv.Units, "units_amount": inv.UnitsAmount, "coupon": inv.CouponCode, "provider": name, "origin": "gateway",
	}); err != nil {
		return nil, err
	}
	return s.settle(ctx, cg, who, provider, inv, n)
}
