package feature_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"

	billing "github.com/hyz-is/arandu-billing"
)

func TestACheckoutChargesTheCatalogueWithTheCoupon(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.plan(t, billing.PlanRequest{Name: "Câmera semestral", Price: 60000, IntervalMonths: 6, InstallmentsFrom: 6})
	w.coupon(t, billing.CouponRequest{Code: "bemvindo", Kind: billing.CouponPercent, Value: 1000, Renewals: 1})

	r := w.subscribe(t, owner(acme), plan.ID, " BemVindo ")
	if r.URL != "https://gateway.example/pay/"+r.Invoice.ID {
		t.Fatalf("the customer was sent to %q", r.URL)
	}
	checkouts := w.gateway.Checkouts()
	if len(checkouts) != 1 {
		t.Fatalf("the gateway was asked %d times", len(checkouts))
	}
	c := checkouts[0]
	if c.InvoiceID != r.Invoice.ID || c.Tenant != acme || c.Subtotal != 60000 || c.Discount != 6000 || c.Total != 54000 ||
		c.MaxInstallments != 6 || c.CouponCode != "BEMVINDO" || c.Months != 6 {
		t.Fatalf("the gateway was asked to collect %+v", c)
	}
	if c.ReturnURL != "https://app.example/painel/faturas/"+r.Invoice.ID+"/retorno" {
		t.Errorf("the return address is %q", c.ReturnURL)
	}
	if !strings.Contains(c.Description, "Câmera semestral") || !strings.Contains(c.Description, "6 meses") {
		t.Errorf("the gateway's line reads %q", c.Description)
	}
	if r.Invoice.Status != billing.InvoiceOpen || r.Invoice.TenantID != acme || r.Invoice.ProviderRef != "ref-"+r.Invoice.ID {
		t.Errorf("the invoice is %+v", r.Invoice)
	}

	// Asking twice answers the invoice already issued.
	again := w.subscribe(t, owner(acme), plan.ID, "bemvindo")
	if again.Invoice.ID != r.Invoice.ID || len(w.gateway.Checkouts()) != 1 {
		t.Errorf("a second checkout issued invoice %s and asked the gateway %d times", again.Invoice.ID, len(w.gateway.Checkouts()))
	}
}

func TestARefusedCouponSaysWhy(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.plan(t, billing.PlanRequest{Price: 9900})
	other := w.plan(t, billing.PlanRequest{Name: "Outro", Price: 9900})
	w.coupon(t, billing.CouponRequest{Code: "SOOUTRO", Kind: billing.CouponFixed, Value: 1000, PlanID: other.ID})
	_, err := w.svc.Subscribe(context.Background(), owner(acme), billing.SubscribeRequest{PlanID: plan.ID, CouponCode: "sooutro"})
	var errs validation.Errors
	if !errors.As(err, &errs) || len(errs.Get("coupon")) == 0 {
		t.Fatalf("a coupon for another plan answered %v", err)
	}
	if got := w.module.Labels("pt-BR").Errors(errs).Get("coupon")[0]; got != "Este cupom não vale para este plano." {
		t.Errorf("the refusal reads %q", got)
	}
	if _, err := w.svc.Subscribe(context.Background(), owner(acme), billing.SubscribeRequest{PlanID: plan.ID, CouponCode: "NADA"}); !errors.As(err, &errs) {
		t.Errorf("an unknown coupon answered %v", err)
	}
}

func TestANotificationConfirmsOnceHoweverOftenItArrives(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.plan(t, billing.PlanRequest{Price: 60000, IntervalMonths: 6, InstallmentsFrom: 6})
	coupon := w.coupon(t, billing.CouponRequest{Code: "BEMVINDO", Kind: billing.CouponPercent, Value: 1000, Renewals: 1})
	r := w.subscribe(t, owner(acme), plan.ID, "BEMVINDO")
	w.gateway.Pay(r.Invoice.ID, 54000, 3)

	for i := 0; i < 3; i++ {
		if rec := w.notify(t, r.Invoice.ID); rec.Code != http.StatusOK {
			t.Fatalf("notification %d answered %d: %s", i, rec.Code, rec.Body)
		}
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments WHERE invoice_id = ?", r.Invoice.ID); n != 1 {
		t.Fatalf("%d payments for one invoice", n)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_coupon_usages WHERE coupon_id = ?", coupon.ID); n != 1 {
		t.Fatalf("%d coupon usages for one payment", n)
	}
	if n := w.count(t, "SELECT uses FROM billing_coupons WHERE id = ?", coupon.ID); n != 1 {
		t.Fatalf("the coupon counts %d uses", n)
	}
	if n := w.count(t, "SELECT installments FROM billing_payments WHERE invoice_id = ?", r.Invoice.ID); n != 3 {
		t.Errorf("the payment records %d installments", n)
	}
	if paid, active := w.events.count(billing.EventInvoicePaid), w.events.count(billing.EventActivated); paid != 1 || active != 1 {
		t.Fatalf("the listener heard %v", w.events.names())
	}
	subs, err := w.svc.ServingSubscriptions(context.Background(), member(acme))
	if err != nil || len(subs) != 1 {
		t.Fatalf("the account serves %d subscriptions: %v", len(subs), err)
	}
	s := subs[0]
	if s.Status != billing.SubscriptionActive || !s.CurrentPeriodEnd.Equal(billing.AddMonths(w.clock.Now(), 6)) ||
		s.CouponID != coupon.ID || s.CouponRenewalsLeft != 1 {
		t.Errorf("the subscription is %+v", s)
	}
	inv, err := w.svc.Invoice(context.Background(), member(acme), r.Invoice.ID)
	if err != nil || inv.Status != billing.InvoicePaid || inv.SubscriptionID != s.ID || inv.PaidAt == nil {
		t.Errorf("the invoice is %+v, %v", inv, err)
	}
	// The customer's return asks again and changes nothing.
	if _, err := w.svc.RefreshInvoice(context.Background(), member(acme), r.Invoice.ID); err != nil {
		t.Fatal(err)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments"); n != 1 {
		t.Errorf("the return confirmed again: %d payments", n)
	}
}

func TestANotificationIsBelievedOnlyAfterTheGatewayIsAsked(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.plan(t, billing.PlanRequest{Price: 9900})
	r := w.subscribe(t, owner(acme), plan.ID, "")

	// Nobody paid: the post is a forgery as far as the gateway knows.
	if rec := w.notify(t, r.Invoice.ID); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"open"`) {
		t.Fatalf("an unpaid notification answered %d %s", rec.Code, rec.Body)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments"); n != 0 {
		t.Fatal("a notification nobody paid confirmed the invoice")
	}
	// Paid less than the total.
	w.gateway.Pay(r.Invoice.ID, 100, 1)
	if rec := w.notify(t, r.Invoice.ID); rec.Code != http.StatusConflict {
		t.Fatalf("a short payment answered %d", rec.Code)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_subscriptions"); n != 0 {
		t.Fatal("a short payment started a subscription")
	}
	// Refused.
	w.gateway.Fail(r.Invoice.ID)
	if rec := w.notify(t, r.Invoice.ID); rec.Code != http.StatusOK {
		t.Fatalf("a refusal answered %d", rec.Code)
	}
	inv, _ := w.svc.Invoice(context.Background(), member(acme), r.Invoice.ID)
	if inv.Status != billing.InvoiceFailed || inv.ProviderStatus != "denied" {
		t.Errorf("a refused invoice is %s (%s)", inv.Status, inv.ProviderStatus)
	}
	if w.gateway.Queries() != 3 {
		t.Errorf("the gateway was asked %d times for three notifications", w.gateway.Queries())
	}
}

func TestAMalformedOrUnknownNotificationIsRefused(t *testing.T) {
	t.Parallel()
	w := install(t)
	if rec := w.notify(t, ""); rec.Code != http.StatusBadRequest {
		t.Errorf("a post naming nothing answered %d", rec.Code)
	}
	if rec := w.notify(t, "no-such-invoice"); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown invoice answered %d", rec.Code)
	}
	if rec := w.do(t, http.MethodPost, w.module.URLs().Notification("nobody"), "order=x", false); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown provider answered %d", rec.Code)
	}
	if rec := w.do(t, http.MethodPost, w.module.URLs().Notification(billing.ManualProviderName), "order=x", false); rec.Code != http.StatusBadRequest {
		t.Errorf("the manual provider took a notification: %d", rec.Code)
	}
}

func TestAListenerErrorRollsTheConfirmationBack(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.plan(t, billing.PlanRequest{Price: 9900})
	r := w.subscribe(t, owner(acme), plan.ID, "")
	w.gateway.Pay(r.Invoice.ID, 9900, 1)
	w.events.fail = errors.New("the application could not deliver")
	if rec := w.notify(t, r.Invoice.ID); rec.Code < 500 {
		t.Fatalf("a listener's failure answered %d; the gateway has to send again", rec.Code)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments"); n != 0 {
		t.Fatal("the payment stayed though the listener failed")
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices WHERE status = 'open'"); n != 1 {
		t.Fatal("the invoice did not go back to open")
	}
	if rec := w.notify(t, r.Invoice.ID); rec.Code != http.StatusOK {
		t.Fatalf("the gateway's second post answered %d", rec.Code)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments"); n != 1 {
		t.Fatalf("%d payments after the retry", n)
	}
}

func TestARenewalExtendsThePeriodAndSpendsTheCouponRenewals(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plan := w.plan(t, billing.PlanRequest{Price: 10000})
	w.coupon(t, billing.CouponRequest{Code: "DUASVEZES", Kind: billing.CouponFixed, Value: 2500, Renewals: 1})
	first := w.subscribe(t, owner(acme), plan.ID, "DUASVEZES")
	w.gateway.Pay(first.Invoice.ID, 7500, 1)
	w.notify(t, first.Invoice.ID)
	subs, _ := w.svc.ServingSubscriptions(ctx, member(acme))
	sub := subs[0]
	paidUntil := sub.CurrentPeriodEnd

	w.clock.Advance(20 * 24 * time.Hour)
	renewal, err := w.svc.Renew(ctx, owner(acme), sub.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if renewal.Invoice.Kind != billing.InvoiceRenewal || renewal.Invoice.Total != 7500 || renewal.Invoice.RenewalNumber != 2 {
		t.Fatalf("the renewal is %+v", renewal.Invoice)
	}
	w.gateway.Pay(renewal.Invoice.ID, 7500, 1)
	w.notify(t, renewal.Invoice.ID)
	subs, _ = w.svc.ServingSubscriptions(ctx, member(acme))
	if got := subs[0]; !got.CurrentPeriodEnd.Equal(billing.AddMonths(paidUntil, 1)) || got.CouponRenewalsLeft != 0 || got.Renewals != 1 {
		t.Fatalf("after the renewal: %+v (paid until %s before)", got, paidUntil)
	}
	if w.events.count(billing.EventRenewed) != 1 {
		t.Errorf("the listener heard %v", w.events.names())
	}

	third, err := w.svc.Renew(ctx, owner(acme), sub.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if third.Invoice.Discount != 0 || third.Invoice.Total != 10000 {
		t.Errorf("a renewal past the coupon's renewals still has a discount: %+v", third.Invoice)
	}
}

func TestOneAccountReachesNoOtherAccountsBilling(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plan := w.plan(t, billing.PlanRequest{Price: 9900})
	mine := w.subscribe(t, owner(acme), plan.ID, "")
	theirs := w.subscribe(t, owner(globex), plan.ID, "")
	w.gateway.Pay(theirs.Invoice.ID, 9900, 1)
	if rec := w.notify(t, theirs.Invoice.ID); rec.Code != http.StatusOK {
		t.Fatalf("the notification answered %d", rec.Code)
	}

	if _, err := w.svc.Invoice(ctx, member(acme), theirs.Invoice.ID); !errors.Is(err, billing.ErrNotFound) {
		t.Errorf("acme read globex's invoice: %v", err)
	}
	if _, err := w.svc.RefreshInvoice(ctx, member(acme), theirs.Invoice.ID); !errors.Is(err, billing.ErrNotFound) {
		t.Errorf("acme refreshed globex's invoice: %v", err)
	}
	globexSubs, _ := w.svc.ServingSubscriptions(ctx, member(globex))
	if len(globexSubs) != 1 {
		t.Fatalf("globex serves %d subscriptions", len(globexSubs))
	}
	if globexSubs[0].TenantID != globex {
		t.Errorf("the subscription was written for %q", globexSubs[0].TenantID)
	}
	if _, err := w.svc.Renew(ctx, owner(acme), globexSubs[0].ID, ""); !errors.Is(err, billing.ErrNotFound) {
		t.Errorf("acme renewed globex's subscription: %v", err)
	}
	if _, err := w.svc.CancelAtPeriodEnd(ctx, owner(acme), globexSubs[0].ID); !errors.Is(err, billing.ErrNotFound) {
		t.Errorf("acme canceled globex's subscription: %v", err)
	}
	page, err := w.svc.InvoicesPage(ctx, member(acme), billing.PageQuery{}, 25)
	if err != nil || page.Total != 1 || page.Rows[0].ID != mine.Invoice.ID {
		t.Errorf("acme's invoices are %+v, %v", page, err)
	}
	if acmeSubs, _ := w.svc.ServingSubscriptions(ctx, member(acme)); len(acmeSubs) != 0 {
		t.Errorf("acme serves globex's subscription")
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments WHERE tenant_id = ?", globex); n != 1 {
		t.Errorf("the payment was not written for globex")
	}
	all, err := w.svc.AllInvoicesPage(ctx, admin(), billing.PageQuery{}, 25)
	if err != nil || all.Total != 2 {
		t.Errorf("the admin reads %d invoices across accounts: %v", all.Total, err)
	}
	if _, err := w.svc.AllInvoicesPage(ctx, owner(acme), billing.PageQuery{}, 25); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("an account read every account's invoices: %v", err)
	}
	// A route takes no tenant from the request: the path names an invoice,
	// and acme's session reaches acme's alone.
	cookies := w.signIn(t, member(acme))
	if rec := w.do(t, http.MethodGet, w.module.URLs().Invoice(theirs.Invoice.ID)+"?tenant_id=globex", "", true, cookies...); rec.Code != http.StatusNotFound {
		t.Errorf("acme's session reached globex's invoice: %d", rec.Code)
	}
}

func TestTheSweepMovesThePeriodsThatEnded(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	graced := w.plan(t, billing.PlanRequest{Name: "Com carência", Price: 9900, GraceDays: 3})
	plain := w.plan(t, billing.PlanRequest{Name: "Sem carência", Price: 9900})
	for _, p := range []*billing.Plan{graced, plain} {
		r := w.subscribe(t, owner(acme), p.ID, "")
		w.gateway.Pay(r.Invoice.ID, 9900, 1)
		w.notify(t, r.Invoice.ID)
	}
	other := w.subscribe(t, owner(globex), plain.ID, "")
	w.gateway.Pay(other.Invoice.ID, 9900, 1)
	w.notify(t, other.Invoice.ID)
	globexSubs, _ := w.svc.ServingSubscriptions(ctx, member(globex))
	if _, err := w.svc.CancelAtPeriodEnd(ctx, owner(globex), globexSubs[0].ID); err != nil {
		t.Fatal(err)
	}

	if moved, err := w.svc.SweepDue(ctx, w.clock.Now()); err != nil || moved != 0 {
		t.Fatalf("a sweep before any end moved %d: %v", moved, err)
	}
	w.clock.Advance(32 * 24 * time.Hour)
	moved, err := w.svc.SweepDue(ctx, w.clock.Now())
	if err != nil || moved != 3 {
		t.Fatalf("the sweep moved %d: %v", moved, err)
	}
	if w.events.count(billing.EventPastDue) != 1 || w.events.count(billing.EventExpired) != 1 || w.events.count(billing.EventCanceled) != 1 {
		t.Fatalf("the listener heard %v", w.events.names())
	}
	if moved, _ := w.svc.SweepDue(ctx, w.clock.Now()); moved != 0 {
		t.Errorf("a second sweep moved %d again", moved)
	}
	w.clock.Advance(3 * 24 * time.Hour)
	if moved, _ := w.svc.SweepDue(ctx, w.clock.Now()); moved != 1 {
		t.Errorf("the grace's end moved %d", moved)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_subscriptions WHERE status IN ('expired','canceled') AND ended_at IS NOT NULL"); n != 3 {
		t.Errorf("%d subscriptions ended", n)
	}
	// An ended subscription paid again starts on the day it is paid.
	subs, _ := w.svc.SubscriptionsPage(ctx, member(acme), billing.PageQuery{Status: string(billing.SubscriptionExpired)}, 25)
	again, err := w.svc.Renew(ctx, owner(acme), subs.Rows[0].ID, "")
	if err != nil {
		t.Fatal(err)
	}
	w.gateway.Pay(again.Invoice.ID, 9900, 1)
	w.notify(t, again.Invoice.ID)
	serving, _ := w.svc.ServingSubscriptions(ctx, member(acme))
	if len(serving) != 1 || !serving[0].CurrentPeriodEnd.Equal(billing.AddMonths(w.clock.Now(), 1)) {
		t.Errorf("the subscription paid again serves %+v", serving)
	}
}

func TestATrialStartsOnceAndAPaymentFollowsIt(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plan := w.plan(t, billing.PlanRequest{Price: 9900, TrialDays: 7})
	sub, err := w.svc.StartTrial(ctx, owner(acme), plan.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != billing.SubscriptionTrialing || !sub.CurrentPeriodEnd.Equal(w.clock.Now().AddDate(0, 0, 7)) {
		t.Fatalf("the trial is %+v", sub)
	}
	if _, err := w.svc.StartTrial(ctx, owner(acme), plan.ID, ""); err == nil {
		t.Fatal("a second trial of one plan was started")
	}
	r := w.subscribe(t, owner(acme), plan.ID, "")
	if r.Invoice.SubscriptionID != sub.ID || r.Invoice.Kind != billing.InvoiceNew {
		t.Fatalf("the payment during the trial is %+v", r.Invoice)
	}
	w.gateway.Pay(r.Invoice.ID, 9900, 1)
	w.notify(t, r.Invoice.ID)
	serving, _ := w.svc.ServingSubscriptions(ctx, member(acme))
	if len(serving) != 1 || serving[0].Status != billing.SubscriptionActive ||
		!serving[0].CurrentPeriodEnd.Equal(billing.AddMonths(w.clock.Now().AddDate(0, 0, 7), 1)) {
		t.Errorf("the trial paid for serves %+v", serving)
	}
	if w.events.count(billing.EventTrialStarted) != 1 || w.events.count(billing.EventActivated) != 1 {
		t.Errorf("the listener heard %v", w.events.names())
	}
}

func TestTheAdminConfirmsByHandAndVoids(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plan := w.plan(t, billing.PlanRequest{Price: 9900})
	r, err := w.svc.Subscribe(ctx, owner(acme), billing.SubscribeRequest{PlanID: plan.ID, Provider: billing.ManualProviderName})
	if err != nil {
		t.Fatal(err)
	}
	if r.URL != "" {
		t.Fatalf("the manual provider sent the customer to %q", r.URL)
	}
	if _, err := w.svc.ConfirmManually(ctx, owner(acme), r.Invoice.ID, billing.ManualPayment{}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("the account confirmed its own payment: %v", err)
	}
	paid, err := w.svc.ConfirmManually(ctx, admin(), r.Invoice.ID, billing.ManualPayment{Method: "pix", Reference: "E123"})
	if err != nil || paid.Status != billing.InvoicePaid {
		t.Fatalf("the admin's confirmation: %+v, %v", paid, err)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments WHERE provider = 'manual' AND confirmed_by = ? AND tenant_id = ?", admin().ID, acme); n != 1 {
		t.Errorf("the manual payment was not recorded for acme by the admin")
	}
	if _, err := w.svc.ConfirmManually(ctx, admin(), r.Invoice.ID, billing.ManualPayment{}); err != nil {
		t.Errorf("a second confirmation failed instead of answering the paid invoice: %v", err)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments"); n != 1 {
		t.Errorf("%d payments after confirming twice", n)
	}
	if _, err := w.svc.VoidInvoice(ctx, admin(), r.Invoice.ID); err == nil {
		t.Error("a paid invoice was voided")
	}
	open, _ := w.svc.Subscribe(ctx, owner(globex), billing.SubscribeRequest{PlanID: plan.ID, Provider: billing.ManualProviderName})
	voided, err := w.svc.VoidInvoice(ctx, admin(), open.Invoice.ID)
	if err != nil || voided.Status != billing.InvoiceCanceled {
		t.Errorf("voiding: %+v, %v", voided, err)
	}
	subs, _ := w.svc.ServingSubscriptions(ctx, member(acme))
	canceled, err := w.svc.CancelNow(ctx, admin(), subs[0].ID)
	if err != nil || canceled.Status != billing.SubscriptionCanceled || canceled.EndedAt == nil {
		t.Errorf("the admin's cancellation: %+v, %v", canceled, err)
	}
	entries := w.count(t, "SELECT COUNT(*) FROM activity_log WHERE tenant_id = ? AND causer_id = ?", acme, admin().ID)
	if entries < 2 {
		t.Errorf("the admin's acts in acme's account left %d entries in acme's log", entries)
	}
}

func TestACouponThatTakesThePriceToZeroConfirmsAtOnce(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.plan(t, billing.PlanRequest{Price: 9900})
	w.coupon(t, billing.CouponRequest{Code: "CORTESIA", Kind: billing.CouponPercent, Value: 10000})
	r := w.subscribe(t, owner(acme), plan.ID, "CORTESIA")
	if r.Invoice.Status != billing.InvoicePaid || r.URL != "" || len(w.gateway.Checkouts()) != 0 {
		t.Fatalf("a free checkout: %+v %q, %d gateway calls", r.Invoice, r.URL, len(w.gateway.Checkouts()))
	}
	if w.events.count(billing.EventActivated) != 1 {
		t.Errorf("the listener heard %v", w.events.names())
	}
}

func TestTheListsPageSearchAndOrderInTheDatabase(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	for i := 0; i < 23; i++ {
		w.plan(t, billing.PlanRequest{Name: "Plano " + string(rune('A'+i)), Price: int64(1000 + i), Position: i})
	}
	page, err := w.svc.PlansPage(ctx, admin(), billing.PageQuery{Page: 2, PerPage: 10}, 25)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 23 || page.Pages != 3 || len(page.Rows) != 10 || page.Rows[0].Name != "Plano K" {
		t.Fatalf("page 2: total %d, pages %d, %d rows, first %q", page.Total, page.Pages, len(page.Rows), page.Rows[0].Name)
	}
	found, _ := w.svc.PlansPage(ctx, admin(), billing.PageQuery{Search: "Plano C"}, 25)
	if found.Total != 1 {
		t.Errorf("the search found %d", found.Total)
	}
	priciest, _ := w.svc.PlansPage(ctx, admin(), billing.PageQuery{Sort: "price", Dir: "desc", PerPage: 1}, 25)
	if priciest.Rows[0].Price != 1022 {
		t.Errorf("the most expensive plan is %d", priciest.Rows[0].Price)
	}
	if _, err := w.svc.PlansPage(ctx, admin(), billing.PageQuery{Sort: "drop table"}, 25); err == nil {
		t.Error("a column off the allowlist was accepted")
	}
}

func TestTheRoutesAnswerBehindASessionAndTheNotificationWithoutOne(t *testing.T) {
	t.Parallel()
	w := install(t)
	u := w.module.URLs()
	plan := w.plan(t, billing.PlanRequest{Price: 9900})

	if rec := w.do(t, http.MethodGet, u.Plans(), "", false); rec.Code != http.StatusFound && rec.Code != http.StatusSeeOther {
		t.Fatalf("a visitor with no session got %d", rec.Code)
	}
	cookies := w.signIn(t, member(acme))
	rec := w.do(t, http.MethodGet, u.Plans(), "", true, cookies...)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), plan.ID) {
		t.Fatalf("the catalogue answered %d %s", rec.Code, rec.Body)
	}
	if rec := w.do(t, http.MethodPost, u.Checkout(plan.ID), "coupon=", true, cookies...); rec.Code != http.StatusForbidden {
		t.Errorf("a member who pays for nothing checked out: %d", rec.Code)
	}
	if rec := w.do(t, http.MethodGet, u.AdminInvoices(), "", true, cookies...); rec.Code != http.StatusForbidden {
		t.Errorf("a member read the admin's invoices: %d", rec.Code)
	}
	ownerCookies := w.signIn(t, owner(acme))
	rec = w.do(t, http.MethodPost, u.Checkout(plan.ID), "coupon=", true, ownerCookies...)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), "https://gateway.example/pay/") {
		t.Fatalf("the owner's checkout answered %d %s", rec.Code, rec.Body)
	}
	rec = w.do(t, http.MethodPost, u.Checkout(plan.ID), "coupon=", false, ownerCookies...)
	if location := rec.Header().Get("Location"); !strings.HasPrefix(location, "https://gateway.example/pay/") {
		t.Errorf("a browser was sent to %q", location)
	}
	adminCookies := w.signIn(t, admin())
	if rec := w.do(t, http.MethodGet, u.AdminInvoices(), "", true, adminCookies...); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Errorf("the admin's invoices answered %d %s", rec.Code, rec.Body)
	}
	for _, path := range w.module.CSRFExemptPaths() {
		if !strings.HasPrefix(u.Notification("fake"), path) {
			t.Errorf("the notification address %s is not under the exemption %s", u.Notification("fake"), path)
		}
	}
	for _, route := range w.router.Routes() {
		if strings.HasPrefix(route.Pattern, "/_arandu") {
			t.Errorf("%s landed in the framework's namespace", route.Pattern)
		}
	}
}

func TestTheModuleDeclaresItsSchemaAndRefusesABadWiring(t *testing.T) {
	t.Parallel()
	w := install(t)
	names := []string{}
	for _, m := range w.module.Migrations() {
		names = append(names, m.GetName())
	}
	want := []string{
		"20261023_0001_create_billing_plans", "20261023_0002_create_billing_coupons",
		"20261023_0003_create_billing_subscriptions", "20261023_0004_create_billing_invoices",
		"20261023_0005_create_billing_payments", "20261023_0006_create_billing_coupon_usages",
	}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("the migrations are %v", names)
	}
	if _, err := billing.New(billing.Config{}, w.db, w.sessions); err == nil {
		t.Error("New accepted a module with no provider")
	}
	if _, err := billing.New(billing.Config{Providers: []billing.Provider{billing.ManualProvider{}}}, nil, w.sessions); err == nil {
		t.Error("New accepted no database")
	}
	if _, err := billing.New(billing.Config{Providers: []billing.Provider{billing.ManualProvider{}}}, w.db, nil); err == nil {
		t.Error("New accepted no session store")
	}
	if len(w.module.Schedule()) != 1 {
		t.Error("the module schedules no sweep")
	}
}

func TestNotificationsRacingConfirmOnce(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.plan(t, billing.PlanRequest{Price: 9900})
	r := w.subscribe(t, owner(acme), plan.ID, "")
	w.gateway.Pay(r.Invoice.ID, 9900, 1)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.notify(t, r.Invoice.ID)
		}()
	}
	wg.Wait()
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments"); n != 1 {
		t.Fatalf("%d payments after six racing notifications", n)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_subscriptions"); n != 1 {
		t.Fatalf("%d subscriptions after six racing notifications", n)
	}
	if w.events.count(billing.EventInvoicePaid) != 1 {
		t.Fatalf("the listener heard %v", w.events.names())
	}
}
