package feature_test

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"

	billing "github.com/hyz-is/arandu-billing"
)

// cyclePlan is a monthly plan of R$ 100,00 that also sells three months at 5%
// off and twelve at 15% off.
func (w *world) cyclePlan(t *testing.T) *billing.Plan {
	t.Helper()
	return w.plan(t, billing.PlanRequest{
		Name: "Zen", Price: 10000,
		Cycles: []billing.Cycle{{Months: 12, Discount: 1500}, {Months: 3, Discount: 500}},
	})
}

func (w *world) subscribeIn(t *testing.T, who security.Subject, planID string, months int) *billing.CheckoutResult {
	t.Helper()
	r, err := w.svc.Subscribe(context.Background(), who, billing.SubscribeRequest{PlanID: planID, CycleMonths: months})
	if err != nil {
		t.Fatalf("subscribing for %d months: %v", months, err)
	}
	return r
}

func (w *world) pay(t *testing.T, inv *billing.Invoice) {
	t.Helper()
	w.gateway.Pay(inv.ID, inv.Total, 1)
	if rec := w.notify(t, inv.ID); rec.Code != http.StatusOK {
		t.Fatalf("the notification answered %d", rec.Code)
	}
}

func (w *world) serving(t *testing.T, tenant string) *billing.Subscription {
	t.Helper()
	subs, err := w.svc.ServingSubscriptions(context.Background(), member(tenant))
	if err != nil || len(subs) != 1 {
		t.Fatalf("serving subscriptions: %v, %v", subs, err)
	}
	return subs[0]
}

func TestACycleIsInvoicedForItsMonthsWithItsDiscountAndBuysThatManyMonths(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.cyclePlan(t)

	r := w.subscribeIn(t, owner(acme), plan.ID, 3)
	inv := r.Invoice
	if inv.Months != 3 || inv.Subtotal != 30000 || inv.CycleDiscount != 1500 || inv.Discount != 0 || inv.Total != 28500 {
		t.Fatalf("the invoice is months %d, subtotal %d, cycle discount %d, discount %d, total %d",
			inv.Months, inv.Subtotal, inv.CycleDiscount, inv.Discount, inv.Total)
	}
	c := w.gateway.Checkouts()[0]
	if c.Months != 3 || c.Subtotal != 30000 || c.CycleDiscount != 1500 || c.Total != 28500 || !strings.Contains(c.Description, "3 meses") {
		t.Fatalf("the gateway was asked to collect %+v", c)
	}

	w.pay(t, inv)
	paidAt := w.clock.Now()
	sub := w.serving(t, acme)
	if !sub.CurrentPeriodEnd.Equal(billing.AddMonths(paidAt, 3)) || sub.CycleMonths != 3 || sub.Status != billing.SubscriptionActive {
		t.Fatalf("after paying: %+v (paid at %s)", sub, paidAt)
	}

	// The cycle is in the log of the invoice issued and of the invoice paid.
	for _, event := range []string{"invoice.created", "invoice.paid"} {
		if n := w.count(t, "SELECT COUNT(*) FROM activity_log WHERE event = ? AND properties LIKE ?", event, `%"cycle":3%`); n != 1 {
			t.Errorf("%d %s entries record the cycle", n, event)
		}
	}
}

func TestThePlansOwnIntervalIsTheDefaultCycle(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.cyclePlan(t)

	for _, months := range []int{0, 1} {
		r := w.subscribeIn(t, owner(globex), plan.ID, months)
		if r.Invoice.Months != 1 || r.Invoice.Total != 10000 || r.Invoice.CycleDiscount != 0 {
			t.Errorf("%d months: the invoice is %+v", months, r.Invoice)
		}
	}
	// Both asked for the same period, so the second answered the first.
	if got := len(w.gateway.Checkouts()); got != 1 {
		t.Errorf("the gateway was asked %d times", got)
	}
	w.pay(t, w.subscribeIn(t, owner(globex), plan.ID, 0).Invoice)
	if sub := w.serving(t, globex); sub.CycleMonths != 0 {
		t.Errorf("a subscription on the plan's own interval stores cycle %d, so it would stop following the plan", sub.CycleMonths)
	}
}

func TestACycleThePlanDoesNotOfferIsRefusedAndIssuesNothing(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plan := w.cyclePlan(t)
	plain := w.plan(t, billing.PlanRequest{Name: "Plain", Price: 5000})

	for _, c := range []struct {
		plan   string
		months int
	}{{plan.ID, 2}, {plan.ID, 6}, {plain.ID, 3}, {plain.ID, 12}, {plan.ID, -1}, {plan.ID, billing.MaxIntervalMonths + 1}} {
		_, err := w.svc.Subscribe(ctx, owner(acme), billing.SubscribeRequest{PlanID: c.plan, CycleMonths: c.months})
		errs, ok := err.(validation.Errors)
		if !ok || len(errs["cycle"]) == 0 {
			t.Errorf("%d months on %s: got %v, want an error on the cycle field", c.months, c.plan, err)
		}
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices"); n != 0 || len(w.gateway.Checkouts()) != 0 {
		t.Errorf("a refused cycle left %d invoices and %d checkouts", n, len(w.gateway.Checkouts()))
	}
}

func TestARenewalKeepsTheCycleAndAChangeAppliesToTheNextInvoice(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plan := w.cyclePlan(t)
	w.pay(t, w.subscribeIn(t, owner(acme), plan.ID, 3).Invoice)
	sub := w.serving(t, acme)
	paidUntil := sub.CurrentPeriodEnd

	w.clock.Advance(30 * 24 * time.Hour)
	renewal, err := w.svc.Renew(ctx, owner(acme), sub.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if inv := renewal.Invoice; inv.Months != 3 || inv.Total != 28500 || inv.Kind != billing.InvoiceRenewal {
		t.Fatalf("the renewal kept no cycle: %+v", inv)
	}
	w.pay(t, renewal.Invoice)
	sub = w.serving(t, acme)
	if !sub.CurrentPeriodEnd.Equal(billing.AddMonths(paidUntil, 3)) || sub.Renewals != 1 || sub.CycleMonths != 3 {
		t.Fatalf("after the renewal: %+v", sub)
	}

	// Choosing another cycle changes the invoice being issued and what follows,
	// and leaves the period already paid for alone.
	paidUntil = sub.CurrentPeriodEnd
	change, err := w.svc.RenewCycle(ctx, owner(acme), sub.ID, "", 12)
	if err != nil {
		t.Fatal(err)
	}
	if inv := change.Invoice; inv.Months != 12 || inv.Subtotal != 120000 || inv.CycleDiscount != 18000 || inv.Total != 102000 {
		t.Fatalf("the change of cycle issued %+v", inv)
	}
	if got := w.serving(t, acme); got.CycleMonths != 3 || !got.CurrentPeriodEnd.Equal(paidUntil) {
		t.Fatalf("issuing the invoice already changed the subscription: %+v", got)
	}
	w.pay(t, change.Invoice)
	sub = w.serving(t, acme)
	if sub.CycleMonths != 12 || !sub.CurrentPeriodEnd.Equal(billing.AddMonths(paidUntil, 12)) {
		t.Fatalf("after the change: %+v", sub)
	}
	next, err := w.svc.Renew(ctx, owner(acme), sub.ID, "")
	if err != nil || next.Invoice.Months != 12 || next.Invoice.Total != 102000 {
		t.Fatalf("the renewal after the change: %+v, %v", next, err)
	}
	if _, err := w.svc.RenewCycle(ctx, owner(acme), sub.ID, "", 5); err == nil {
		t.Error("a renewal in a cycle the plan does not offer was issued")
	}
}

func TestACycleThePlanWithdrewIsRefusedAtRenewalUntilAnotherIsChosen(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plan := w.cyclePlan(t)
	w.pay(t, w.subscribeIn(t, owner(acme), plan.ID, 3).Invoice)
	sub := w.serving(t, acme)

	if _, err := w.svc.UpdatePlan(ctx, admin(), plan.ID, billing.PlanRequest{
		Name: "Zen", Price: 10000, IntervalMonths: 1, Active: true, Cycles: []billing.Cycle{{Months: 12, Discount: 1500}},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := w.svc.Renew(ctx, owner(acme), sub.ID, "")
	if errs, ok := err.(validation.Errors); !ok || len(errs["cycle"]) == 0 {
		t.Fatalf("got %v, want an error on the cycle field", err)
	}
	r, err := w.svc.RenewCycle(ctx, owner(acme), sub.ID, "", 12)
	if err != nil || r.Invoice.Months != 12 {
		t.Fatalf("choosing another cycle: %+v, %v", r, err)
	}
}

func TestACouponTakesItsDiscountOffTheCyclePrice(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.cyclePlan(t)
	w.coupon(t, billing.CouponRequest{Code: "DEZ", Kind: billing.CouponPercent, Value: 1000})

	r, err := w.svc.Subscribe(context.Background(), owner(acme), billing.SubscribeRequest{PlanID: plan.ID, CycleMonths: 3, CouponCode: "dez"})
	if err != nil {
		t.Fatal(err)
	}
	// 300,00 - 15,00 for the cycle = 285,00; 10% of that is 28,50.
	if inv := r.Invoice; inv.Subtotal != 30000 || inv.CycleDiscount != 1500 || inv.Discount != 2850 || inv.Total != 25650 {
		t.Fatalf("the invoice is %+v", inv)
	}
}

func TestThePlanFormWritesAndReadsTheCycles(t *testing.T) {
	t.Parallel()
	w := install(t)
	u := w.module.URLs()
	cookies := w.signIn(t, admin())
	form := url.Values{
		"name": {"Zen"}, "price_brl": {"100,00"}, "interval_months": {"1"}, "active": {"1"},
		"cycles": {"12=17,5\n\n 3 = 5 \n"},
	}
	rec := w.do(t, http.MethodPost, u.AdminPlans(), form.Encode(), true, cookies...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating the plan answered %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"months":3`) || !strings.Contains(body, `"discount":500`) ||
		!strings.Contains(body, `"months":12`) || !strings.Contains(body, `"discount":1750`) {
		t.Errorf("the plan answered %s", body)
	}
	if strings.Index(body, `"months":3`) > strings.Index(body, `"months":12`) {
		t.Errorf("the cycles are not listed shortest first: %s", body)
	}

	for _, bad := range []string{"three=5", "3", "3=abc", "3=100", "3=5\n3=6", "0=5", "121=5"} {
		form.Set("cycles", bad)
		form.Set("name", "Other "+bad)
		if rec := w.do(t, http.MethodPost, u.AdminPlans(), form.Encode(), true, cookies...); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("cycles %q answered %d %s", bad, rec.Code, rec.Body)
		}
	}
}
