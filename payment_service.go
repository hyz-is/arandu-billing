package billing

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/database/model"
	"github.com/hyz-is/arandu-billing/internal/everytenant"
)

// missing says the error is a row that is not there.
func missing(err error) bool {
	return errors.Is(err, model.ErrModelNotFound) || errors.Is(err, ErrNotFound)
}

// HandleNotification is what a gateway's post to its notification address
// does: the provider named reads the request, the invoice it names is looked
// up across every account, the gateway is asked again where the payment
// stands, and only then is anything confirmed -- once, however many times the
// gateway posts.
//
// It answers the invoice as it stands afterwards. A request the provider does
// not recognise wraps ErrBadNotification; an invoice that is not there, or a
// provider that is not configured, is ErrNotFound.
func (s *BillingService) HandleNotification(ctx context.Context, providerName string, r *http.Request) (*Invoice, error) {
	provider := s.providers[providerName]
	if provider == nil {
		return nil, ErrNotFound
	}
	n, err := provider.ParseNotification(r)
	if err != nil {
		if errors.Is(err, ErrBadNotification) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrBadNotification, err)
	}
	if strings.TrimSpace(n.InvoiceID) == "" && strings.TrimSpace(n.Reference) == "" {
		return nil, fmt.Errorf("%w: it names no invoice", ErrBadNotification)
	}
	probe, err := security.Authorize(ctx, s.policy, systemSubject(lookupTenant), PaymentsConfirm, Account{})
	if err != nil {
		return nil, err
	}
	lookup := everytenant.Invoices(s.db).Where("provider", "=", providerName)
	if id := strings.TrimSpace(n.InvoiceID); id != "" {
		lookup = lookup.WhereKey(id)
	} else {
		lookup = lookup.Where("provider_ref", "=", strings.TrimSpace(n.Reference))
	}
	row, err := lookup.FirstOrFail(ctx, probe)
	if err != nil {
		return nil, notFound(err)
	}
	who := systemSubject(row.TenantID)
	g, err := security.Authorize(ctx, s.policy, who, PaymentsConfirm, Account{TenantID: row.TenantID})
	if err != nil {
		return nil, err
	}
	inv, err := Invoices(s.db).WhereKey(row.ID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	return s.settle(ctx, g, who, provider, inv, &n)
}

// RefreshInvoice asks the gateway again where one of the account's invoices
// stands, and confirms it when it is paid: what the page the gateway sends the
// customer back to does, so a payment counts even before the notification
// arrives.
func (s *BillingService) RefreshInvoice(ctx context.Context, actor security.Subject, id string) (*Invoice, error) {
	g, err := security.Authorize(ctx, s.policy, actor, InvoicesView, Account{TenantID: actor.Tenant})
	if err != nil {
		return nil, err
	}
	inv, err := Invoices(s.db).WhereKey(id).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if inv.Status == InvoicePaid {
		return inv, nil
	}
	provider := s.providers[inv.Provider]
	if provider == nil {
		return inv, nil
	}
	who := systemSubject(inv.TenantID)
	sg, err := security.Authorize(ctx, s.policy, who, PaymentsConfirm, Account{TenantID: inv.TenantID})
	if err != nil {
		return nil, err
	}
	return s.settle(ctx, sg, who, provider, inv, nil)
}

// settle asks the provider where an invoice's payment stands and acts on the
// answer: paid with the invoice's total confirms it, failed marks it failed,
// anything else records what the gateway said.
func (s *BillingService) settle(ctx context.Context, g security.Grant, who security.Subject, provider Provider, inv *Invoice, n *Notification) (*Invoice, error) {
	ref := inv.ProviderRef
	if n != nil && n.Reference != "" && ref == "" {
		ref = n.Reference
	}
	st, err := provider.QueryStatus(ctx, StatusQuery{InvoiceID: inv.ID, Reference: ref, Total: inv.Total, Notification: n})
	if err != nil {
		return nil, err
	}
	if st.Reference == "" {
		st.Reference = ref
	}
	switch st.State {
	case StatePaid:
		if inv.Status == InvoicePaid {
			return inv, nil
		}
		if st.Amount != inv.Total {
			if err := s.note(ctx, g, inv, "amount_mismatch:"+st.Raw, st.Reference); err != nil {
				return nil, err
			}
			return nil, ErrAmountMismatch
		}
		paid, _, err := s.confirm(ctx, g, who, inv, st, provider.Name(), "provider:"+provider.Name())
		return paid, err
	case StateFailed:
		if inv.Status != InvoiceOpen {
			return inv, s.note(ctx, g, inv, st.Raw, st.Reference)
		}
		now := s.now()
		claimed, err := Invoices(s.db).WhereKey(inv.ID).Where("status", "=", string(InvoiceOpen)).Update(ctx, g, map[string]any{
			"status": string(InvoiceFailed), "provider_status": clip(st.Raw, maxShortLen), "provider_ref": clip(st.Reference, maxIdentifierLen), "updated_at": now,
		})
		if err != nil {
			return nil, err
		}
		if claimed > 0 {
			inv.Status = InvoiceFailed
			if err := s.record(ctx, g, who, activityInvoiceFailed, "billing_invoice", inv.ID, map[string]any{"provider_status": st.Raw}); err != nil {
				return nil, err
			}
		}
		return inv, nil
	}
	return inv, s.note(ctx, g, inv, st.Raw, st.Reference)
}

// note keeps what the gateway last said about an invoice.
func (s *BillingService) note(ctx context.Context, g security.Grant, inv *Invoice, raw, ref string) error {
	raw, ref = clip(raw, maxShortLen), clip(ref, maxIdentifierLen)
	if raw == inv.ProviderStatus && (ref == "" || ref == inv.ProviderRef) {
		return nil
	}
	values := map[string]any{"provider_status": raw, "updated_at": s.now()}
	if ref != "" {
		values["provider_ref"] = ref
		inv.ProviderRef = ref
	}
	inv.ProviderStatus = raw
	_, err := Invoices(s.db).WhereKey(inv.ID).Update(ctx, g, values)
	return err
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// confirm is the one place an invoice becomes paid. It claims the invoice
// with a conditional update, so of two confirmations racing -- the
// notification and the customer's return -- one writes and the other finds it
// paid; the unique index on the payment's invoice_id holds the same line in
// the database. It answers the invoice, and whether this call was the one that
// confirmed it.
func (s *BillingService) confirm(ctx context.Context, g security.Grant, actor security.Subject, inv *Invoice, st PaymentStatus, providerName, confirmedBy string) (*Invoice, bool, error) {
	now := s.now()
	paidAt := st.PaidAt.UTC()
	if st.PaidAt.IsZero() {
		paidAt = now
	}
	first := false
	err := data.Transaction(ctx, s.db, func(ctx context.Context) error {
		claimed, err := Invoices(s.db).WhereKey(inv.ID).Where("status", "!=", string(InvoicePaid)).Update(ctx, g, map[string]any{
			"status": string(InvoicePaid), "paid_at": paidAt, "provider_status": clip(st.Raw, maxShortLen),
			"provider_ref": clip(st.Reference, maxIdentifierLen), "updated_at": now,
		})
		if err != nil {
			return err
		}
		if claimed == 0 {
			return nil
		}
		first = true
		inv.Status, inv.PaidAt, inv.ProviderStatus, inv.ProviderRef, inv.UpdatedAt = InvoicePaid, &paidAt, clip(st.Raw, maxShortLen), clip(st.Reference, maxIdentifierLen), now

		payment, err := Payments(s.db).New()
		if err != nil {
			return err
		}
		if payment.ID, err = newID(); err != nil {
			return err
		}
		payment.TenantID = data.Tenant(g)
		payment.InvoiceID = inv.ID
		payment.Provider = providerName
		payment.ProviderRef = clip(st.Reference, maxIdentifierLen)
		payment.Method = clip(st.Method, maxShortLen)
		payment.Installments = st.Installments
		if payment.Installments < 1 {
			payment.Installments = 1
		}
		payment.Amount = inv.Total
		payment.PaidAt = paidAt
		payment.ConfirmedBy = clip(confirmedBy, maxIdentifierLen)
		payment.CreatedAt, payment.UpdatedAt = now, now
		if _, err := payment.Save(ctx, g); err != nil {
			return err
		}

		plan, err := Plans(s.db).WhereKey(inv.PlanID).FirstOrFail(ctx, g)
		if err != nil {
			return notFound(err)
		}
		var coupon *Coupon
		if inv.CouponID != "" {
			if c, err := Coupons(s.db).WhereKey(inv.CouponID).FirstOrFail(ctx, g); err == nil {
				coupon = c
			} else if !missing(err) {
				return err
			}
		}
		if coupon != nil {
			usage, err := CouponUsages(s.db).New()
			if err != nil {
				return err
			}
			if usage.ID, err = newID(); err != nil {
				return err
			}
			usage.TenantID = data.Tenant(g)
			usage.CouponID, usage.InvoiceID, usage.PaymentID = coupon.ID, inv.ID, payment.ID
			usage.Discount = inv.Discount
			usage.RenewalNumber = inv.RenewalNumber
			usage.UsedAt = paidAt
			usage.CreatedAt, usage.UpdatedAt = now, now
			if _, err := usage.Save(ctx, g); err != nil {
				return err
			}
			if inv.Kind == InvoiceNew {
				if _, err := Coupons(s.db).WhereKey(coupon.ID).Increment(ctx, g, "uses", 1, nil); err != nil {
					return err
				}
			}
		}

		sub, event, err := s.extend(ctx, g, inv, plan, coupon, paidAt, now)
		if err != nil {
			return err
		}
		if err := s.record(ctx, g, actor, activityInvoicePaid, "billing_invoice", inv.ID, map[string]any{
			"total": inv.Total, "provider": providerName, "method": payment.Method, "subscription": sub.ID,
			"paid_until": sub.CurrentPeriodEnd.UTC().Format(time.RFC3339),
		}); err != nil {
			return err
		}
		invoice := NewInvoiceResource(*inv)
		subscription := NewSubscriptionResource(*sub)
		planResource := NewPlanResource(*plan)
		if err := s.emit(ctx, Event{Name: EventInvoicePaid, Tenant: inv.TenantID, At: now,
			Subscription: subscription, Plan: planResource, Invoice: &invoice, Grant: g}); err != nil {
			return err
		}
		return s.emit(ctx, Event{Name: event, Tenant: inv.TenantID, At: now,
			Subscription: subscription, Plan: planResource, Invoice: &invoice, Grant: g})
	})
	if err != nil {
		return nil, false, err
	}
	if !first {
		fresh, err := Invoices(s.db).WhereKey(inv.ID).FirstOrFail(ctx, g)
		if err != nil {
			return nil, false, notFound(err)
		}
		return fresh, false, nil
	}
	return inv, true, nil
}

// extend starts or extends the subscription a paid invoice is for, and says
// which event it was.
func (s *BillingService) extend(ctx context.Context, g security.Grant, inv *Invoice, plan *Plan, coupon *Coupon, paidAt, now time.Time) (*Subscription, EventName, error) {
	var sub *Subscription
	if inv.SubscriptionID != "" {
		found, err := Subscriptions(s.db).WhereKey(inv.SubscriptionID).FirstOrFail(ctx, g)
		if err != nil && !missing(err) {
			return nil, "", err
		}
		if err == nil {
			sub = found
		}
	}
	period := NextPeriod(sub, paidAt, inv.Months)
	if sub == nil {
		created, err := Subscriptions(s.db).New()
		if err != nil {
			return nil, "", err
		}
		if created.ID, err = newID(); err != nil {
			return nil, "", err
		}
		created.TenantID = data.Tenant(g)
		created.Reference = inv.Reference
		created.PlanID = plan.ID
		created.Status = SubscriptionActive
		created.CurrentPeriodStart, created.CurrentPeriodEnd = period.Start, period.End
		created.GraceDays = plan.GraceDays
		if coupon != nil {
			created.CouponID, created.CouponRenewalsLeft = coupon.ID, coupon.Renewals
		}
		created.Meta = inv.Meta
		if created.Meta == nil {
			created.Meta = Meta{}
		}
		created.CreatedBy = inv.CreatedBy
		created.CreatedAt, created.UpdatedAt = now, now
		if _, err := created.Save(ctx, g); err != nil {
			return nil, "", err
		}
		inv.SubscriptionID = created.ID
		if _, err := Invoices(s.db).WhereKey(inv.ID).Update(ctx, g, map[string]any{"subscription_id": created.ID}); err != nil {
			return nil, "", err
		}
		return created, EventActivated, nil
	}

	event := EventRenewed
	if sub.Ended() || sub.Status == SubscriptionTrialing {
		event = EventActivated
	}
	if sub.Ended() {
		sub.CouponID, sub.CouponRenewalsLeft = "", 0
		sub.CanceledAt, sub.EndedAt = nil, nil
		sub.GraceDays = plan.GraceDays
	}
	switch {
	case inv.Kind == InvoiceRenewal && coupon != nil && coupon.ID == sub.CouponID && sub.CouponRenewalsLeft > 0:
		sub.CouponRenewalsLeft--
	case coupon != nil && inv.Kind == InvoiceNew:
		sub.CouponID, sub.CouponRenewalsLeft = coupon.ID, coupon.Renewals
	case coupon != nil:
		sub.CouponID, sub.CouponRenewalsLeft = coupon.ID, coupon.Renewals
	}
	if inv.Kind == InvoiceRenewal {
		sub.Renewals++
	}
	sub.Status = SubscriptionActive
	sub.CurrentPeriodStart, sub.CurrentPeriodEnd = period.Start, period.End
	sub.CancelAtPeriodEnd = false
	sub.UpdatedAt = now
	if _, err := sub.Save(ctx, g); err != nil {
		return nil, "", err
	}
	return sub, event, nil
}

// ManualPayment is what the admin records when a payment arrived outside a
// gateway: a Pix seen on the bank statement, a transfer.
type ManualPayment struct {
	// Method is how it was paid: "pix", "transfer".
	Method string
	// Reference is the bank's identifier: the Pix end-to-end id.
	Reference string
	// PaidAt is when it was paid; zero is now.
	PaidAt time.Time
}

// ConfirmManually is the admin recording that one account's invoice was paid.
// It confirms exactly as a gateway's notification does: once.
func (s *BillingService) ConfirmManually(ctx context.Context, actor security.Subject, id string, in ManualPayment) (*Invoice, error) {
	if strings.TrimSpace(in.Method) == "" {
		in.Method = ManualProviderName
	}
	if len(in.Method) > maxShortLen || len(in.Reference) > maxIdentifierLen {
		return nil, validation.Errors{"reference": {message("too_long")}}
	}
	probe, err := security.Authorize(ctx, s.policy, actor, InvoicesManage, Account{})
	if err != nil {
		return nil, err
	}
	row, err := everytenant.Invoices(s.db).WhereKey(id).FirstOrFail(ctx, probe)
	if err != nil {
		return nil, notFound(err)
	}
	who := actingIn(actor, row.TenantID)
	g, err := security.Authorize(ctx, s.policy, who, InvoicesManage, Account{TenantID: row.TenantID})
	if err != nil {
		return nil, err
	}
	inv, err := Invoices(s.db).WhereKey(row.ID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if inv.Status == InvoicePaid {
		return inv, nil
	}
	paid, _, err := s.confirm(ctx, g, who, inv, PaymentStatus{
		State: StatePaid, Amount: inv.Total, Method: strings.TrimSpace(in.Method),
		Reference: strings.TrimSpace(in.Reference), PaidAt: in.PaidAt, Raw: "manual",
	}, ManualProviderName, actor.ID)
	return paid, err
}

// VoidInvoice is the admin canceling one account's open invoice. A paid
// invoice is never voided here.
func (s *BillingService) VoidInvoice(ctx context.Context, actor security.Subject, id string) (*Invoice, error) {
	probe, err := security.Authorize(ctx, s.policy, actor, InvoicesManage, Account{})
	if err != nil {
		return nil, err
	}
	row, err := everytenant.Invoices(s.db).WhereKey(id).FirstOrFail(ctx, probe)
	if err != nil {
		return nil, notFound(err)
	}
	who := actingIn(actor, row.TenantID)
	g, err := security.Authorize(ctx, s.policy, who, InvoicesManage, Account{TenantID: row.TenantID})
	if err != nil {
		return nil, err
	}
	inv, err := Invoices(s.db).WhereKey(row.ID).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if inv.Status == InvoicePaid {
		return nil, validation.Errors{"invoice": {message("invoice_paid")}}
	}
	if inv.Status == InvoiceCanceled {
		return inv, nil
	}
	now := s.now()
	claimed, err := Invoices(s.db).WhereKey(inv.ID).Where("status", "!=", string(InvoicePaid)).Update(ctx, g, map[string]any{
		"status": string(InvoiceCanceled), "updated_at": now,
	})
	if err != nil {
		return nil, err
	}
	if claimed == 0 {
		return nil, validation.Errors{"invoice": {message("invoice_paid")}}
	}
	inv.Status, inv.UpdatedAt = InvoiceCanceled, now
	return inv, s.record(ctx, g, who, activityInvoiceCanceled, "billing_invoice", inv.ID, nil)
}

// Invoice is one of the asking account's invoices.
func (s *BillingService) Invoice(ctx context.Context, actor security.Subject, id string) (*Invoice, error) {
	g, err := security.Authorize(ctx, s.policy, actor, InvoicesView, Account{TenantID: actor.Tenant})
	if err != nil {
		return nil, err
	}
	inv, err := Invoices(s.db).WhereKey(id).FirstOrFail(ctx, g)
	return inv, notFound(err)
}

// Instructions are what the invoice's provider tells the customer on the
// invoice page, empty for a provider with a page of its own.
func (s *BillingService) instructions(inv *Invoice) string {
	if p, ok := s.providers[inv.Provider].(instructor); ok {
		return p.PaymentInstructions()
	}
	return ""
}
