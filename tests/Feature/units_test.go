package feature_test

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"

	billing "github.com/hyz-is/arandu-billing"
)

// teamPlan is the plan of R$ 119,90 a month with one member included, R$ 20,00
// for each member beyond it, and the same plan sold by the year.
func (w *world) teamPlan(t *testing.T) *billing.Plan {
	t.Helper()
	return w.plan(t, billing.PlanRequest{
		Name: "Empresas", Price: 11990, IncludedUnits: 1, UnitPrice: 2000,
		Cycles: []billing.Cycle{{Months: 12}},
	})
}

func (w *world) subscribeUnits(t *testing.T, who security.Subject, planID string, months, units int) *billing.CheckoutResult {
	t.Helper()
	r, err := w.svc.Subscribe(context.Background(), who, billing.SubscribeRequest{PlanID: planID, CycleMonths: months, Units: units})
	if err != nil {
		t.Fatalf("subscribing with %d units: %v", units, err)
	}
	return r
}

func TestAnInvoiceChargesTheUnitsAboveTheIncludedOnes(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)

	r := w.subscribeUnits(t, owner(acme), plan.ID, 0, 3)
	inv := r.Invoice
	if inv.Units != 3 || inv.UnitsAmount != 4000 || inv.Subtotal != 15990 || inv.Total != 15990 {
		t.Fatalf("monthly invoice: %+v", inv)
	}
	c := w.gateway.Checkouts()[0]
	if c.Units != 3 || c.UnitsAmount != 4000 || c.Subtotal != 15990 || c.Total != 15990 {
		t.Fatalf("the gateway was asked to collect %+v", c)
	}

	yearly := w.subscribeUnits(t, owner(globex), plan.ID, 12, 3).Invoice
	if yearly.UnitsAmount != 48000 || yearly.Subtotal != 11990*12+48000 || yearly.Units != 3 {
		t.Fatalf("yearly invoice: %+v", yearly)
	}

	w.pay(t, inv)
	sub := w.serving(t, acme)
	if sub.Units != 3 {
		t.Fatalf("the subscription is charged for %d units", sub.Units)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM activity_log WHERE event = ? AND properties LIKE ?", "invoice.created", `%"units":3%`); n != 2 {
		t.Errorf("%d invoice entries record the units", n)
	}
}

func TestUnitsAPlanDoesNotSellAreRefusedAndIssueNothing(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plain := w.plan(t, billing.PlanRequest{Name: "Plain", Price: 5000})
	team := w.teamPlan(t)

	for _, c := range []struct {
		plan  string
		units int
	}{{plain.ID, 2}, {plain.ID, 50}, {team.ID, billing.MaxUnits + 1}, {team.ID, -1}} {
		_, err := w.svc.Subscribe(ctx, owner(acme), billing.SubscribeRequest{PlanID: c.plan, Units: c.units})
		if errs, ok := err.(validation.Errors); !ok || len(errs["units"]) == 0 {
			t.Errorf("%d units on %s: got %v, want an error on the units field", c.units, c.plan, err)
		}
	}
	// One unit is what every plan includes.
	if _, err := w.svc.Subscribe(ctx, owner(acme), billing.SubscribeRequest{PlanID: plain.ID, Units: 1}); err != nil {
		t.Errorf("one unit on a plan with no unit price: %v", err)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices"); n != 1 {
		t.Errorf("refused units left %d invoices", n)
	}
}

func TestAPlanWithoutUnitsBehavesAsItAlwaysDid(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.plan(t, billing.PlanRequest{Name: "Plain", Price: 5000})
	r := w.subscribe(t, owner(acme), plan.ID, "")
	if inv := r.Invoice; inv.Units != 0 || inv.UnitsAmount != 0 || inv.Subtotal != 5000 || inv.Total != 5000 {
		t.Fatalf("the invoice is %+v", inv)
	}
	w.pay(t, r.Invoice)
	if sub := w.serving(t, acme); sub.Units != 0 {
		t.Fatalf("the subscription is charged for %d units", sub.Units)
	}
}

func TestRenewalsAreInvoicedForTheUnitsTheSubscriptionHas(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plan := w.teamPlan(t)
	w.pay(t, w.subscribeUnits(t, owner(acme), plan.ID, 0, 2).Invoice)
	sub := w.serving(t, acme)

	if _, err := w.svc.SetUnits(ctx, owner(acme), sub.ID, 5); err != nil {
		t.Fatal(err)
	}
	w.clock.Advance(30 * 24 * time.Hour)
	r, err := w.svc.Renew(ctx, owner(acme), sub.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if inv := r.Invoice; inv.Units != 5 || inv.UnitsAmount != 8000 || inv.Total != 19990 || inv.Kind != billing.InvoiceRenewal {
		t.Fatalf("the renewal is %+v", inv)
	}
	c, err := w.svc.RenewCycle(ctx, owner(acme), sub.ID, "", 12)
	if err != nil {
		t.Fatal(err)
	}
	if inv := c.Invoice; inv.Units != 5 || inv.UnitsAmount != 96000 || inv.Total != 11990*12+96000 {
		t.Fatalf("the renewal for a year is %+v", inv)
	}
	// A serving subscription changes its units with SetUnits, not by asking
	// for a different number at checkout.
	_, err = w.svc.Subscribe(ctx, owner(acme), billing.SubscribeRequest{PlanID: plan.ID, Units: 9})
	if errs, ok := err.(validation.Errors); !ok || len(errs["units"]) == 0 {
		t.Errorf("a different number of units at checkout: %v", err)
	}
}

func TestSetUnitsChangesTheNextInvoicesAndChargesNothing(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plan := w.teamPlan(t)
	w.pay(t, w.subscribeUnits(t, owner(acme), plan.ID, 0, 2).Invoice)
	sub := w.serving(t, acme)
	invoices := w.count(t, "SELECT COUNT(*) FROM billing_invoices")
	checkouts := len(w.gateway.Checkouts())

	got, err := w.svc.SetUnits(ctx, owner(acme), sub.ID, 4)
	if err != nil || got.Units != 4 {
		t.Fatalf("SetUnits: %+v, %v", got, err)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices"); n != invoices || len(w.gateway.Checkouts()) != checkouts {
		t.Errorf("changing the units issued an invoice or asked the gateway")
	}
	if now := w.serving(t, acme); now.Units != 4 || !now.CurrentPeriodEnd.Equal(sub.CurrentPeriodEnd) {
		t.Errorf("the subscription is %+v", now)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM activity_log WHERE event = ? AND properties LIKE ? AND properties LIKE ?",
		"subscription.units_changed", `%"before":2%`, `%"after":4%`); n != 1 {
		t.Errorf("%d activity entries record the change from 2 to 4", n)
	}
	// The same number again changes and records nothing.
	if _, err := w.svc.SetUnits(ctx, owner(acme), sub.ID, 4); err != nil {
		t.Fatal(err)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM activity_log WHERE event = ?", "subscription.units_changed"); n != 1 {
		t.Errorf("a repeated change was recorded %d times", n)
	}

	for _, units := range []int{0, -3, billing.MaxUnits + 1} {
		_, err := w.svc.SetUnits(ctx, owner(acme), sub.ID, units)
		if errs, ok := err.(validation.Errors); !ok || len(errs["units"]) == 0 {
			t.Errorf("%d units: got %v", units, err)
		}
	}
}

func TestSetUnitsRefusesUnitsThePlanDoesNotSell(t *testing.T) {
	t.Parallel()
	w := install(t)
	plain := w.plan(t, billing.PlanRequest{Name: "Plain", Price: 5000})
	w.pay(t, w.subscribe(t, owner(acme), plain.ID, "").Invoice)
	sub := w.serving(t, acme)
	_, err := w.svc.SetUnits(context.Background(), owner(acme), sub.ID, 2)
	if errs, ok := err.(validation.Errors); !ok || len(errs["units"]) == 0 {
		t.Fatalf("got %v", err)
	}
}

func TestSetUnitsIsTheAccountManagersAndReachesNoOtherAccount(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	plan := w.teamPlan(t)
	w.pay(t, w.subscribeUnits(t, owner(acme), plan.ID, 0, 2).Invoice)
	sub := w.serving(t, acme)

	if _, err := w.svc.SetUnits(ctx, member(acme), sub.ID, 3); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("a member changed the units: %v", err)
	}
	if _, err := w.svc.SetUnits(ctx, security.Subject{}, sub.ID, 3); err == nil {
		t.Error("a guest changed the units")
	}
	if _, err := w.svc.SetUnits(ctx, owner(globex), sub.ID, 3); !errors.Is(err, billing.ErrNotFound) {
		t.Errorf("another account changed the units: %v", err)
	}
	if got := w.serving(t, acme); got.Units != 2 {
		t.Errorf("a refused change moved the units to %d", got.Units)
	}
}

func TestThePlanFormWritesAndReadsTheUnits(t *testing.T) {
	t.Parallel()
	w := install(t)
	u := w.module.URLs()
	cookies := w.signIn(t, admin())
	form := url.Values{
		"name": {"Empresas"}, "price_brl": {"119,90"}, "interval_months": {"1"}, "active": {"1"},
		"included_units": {"1"}, "unit_price_brl": {"20,00"},
	}
	rec := w.do(t, http.MethodPost, u.AdminPlans(), form.Encode(), true, cookies...)
	if rec.Code != http.StatusCreated {
		t.Fatalf("creating the plan answered %d %s", rec.Code, rec.Body)
	}
	if body := rec.Body.String(); !strings.Contains(body, `"included_units":1`) || !strings.Contains(body, `"unit_price":2000`) {
		t.Errorf("the plan answered %s", body)
	}

	for _, bad := range [][2]string{{"included_units", "-1"}, {"included_units", "1001"}, {"unit_price_brl", "-5,00"}} {
		form.Set("name", "Other "+bad[0]+bad[1])
		form.Set(bad[0], bad[1])
		rec := w.do(t, http.MethodPost, u.AdminPlans(), form.Encode(), true, cookies...)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s=%s answered %d %s", bad[0], bad[1], rec.Code, rec.Body)
		}
		form.Set("included_units", "1")
		form.Set("unit_price_brl", "20,00")
	}
}
