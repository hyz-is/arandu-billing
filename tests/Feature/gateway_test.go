package feature_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/migrations"

	billing "github.com/hyz-is/arandu-billing"
)

// post sends a request the gateway made to the notification address.
func (w *world) post(r *http.Request) *httptest.ResponseRecorder { return w.serve(r) }

func (w *world) address() string { return w.module.URLs().Notification(w.gateway.Name()) }

// held subscribes an account to the team plan with three members, pays the first
// invoice with the gateway's identifier of the subscription, and answers the
// subscription as it then stands.
func (w *world) held(t *testing.T, who security.Subject, plan *billing.Plan, ref string) *billing.Subscription {
	t.Helper()
	inv := w.subscribeUnits(t, who, plan.ID, 0, 3).Invoice
	w.gateway.PaySubscription(inv.ID, inv.Total, 1, ref)
	if rec := w.notify(t, inv.ID); rec.Code != http.StatusOK {
		t.Fatalf("paying the first invoice answered %d %s", rec.Code, rec.Body)
	}
	return w.serving(t, who.Tenant)
}

// subscriptionRow reads a subscription by the gateway's identifier of it,
// whatever its status.
func (w *world) subscriptionRow(t *testing.T, ref string) (status string, units int, ended bool) {
	t.Helper()
	query := "SELECT status, units, ended_at IS NOT NULL FROM billing_subscriptions WHERE provider_ref = ?"
	if w.db.Dialect() == data.DialectPostgres {
		query = numbered(query)
	}
	var flag any
	if err := w.sql.QueryRow(query, ref).Scan(&status, &units, &flag); err != nil {
		t.Fatalf("reading the subscription %s: %v", ref, err)
	}
	switch v := flag.(type) {
	case bool:
		ended = v
	case int64:
		ended = v != 0
	}
	return status, units, ended
}

func TestAPaidInvoiceBindsTheSubscriptionToTheGatewaysIdentifier(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)

	sub := w.held(t, owner(acme), plan, "sub_acme")
	if sub.Provider != "fake" || sub.ProviderRef != "sub_acme" || sub.Units != 3 {
		t.Fatalf("the subscription is %+v", sub)
	}

	// The identifier may also come with the notification, when the gateway's
	// answer about the payment does not carry it.
	inv := w.subscribeUnits(t, owner(globex), plan.ID, 0, 1).Invoice
	w.gateway.Pay(inv.ID, inv.Total, 1)
	address := w.address()
	if rec := w.post(w.gateway.SubscriptionNotification(address, inv.ID, "sub_globex")); rec.Code != http.StatusOK {
		t.Fatalf("answered %d %s", rec.Code, rec.Body)
	}
	if got := w.serving(t, globex); got.Provider != "fake" || got.ProviderRef != "sub_globex" {
		t.Fatalf("the subscription is %+v", got)
	}

	// One gateway subscription is held by one subscription: a second account
	// claiming it is refused and its invoice stays open.
	other := w.subscribeUnits(t, owner("initech"), plan.ID, 0, 1).Invoice
	w.gateway.PaySubscription(other.ID, other.Total, 1, "sub_acme")
	if rec := w.notify(t, other.ID); rec.Code == http.StatusOK {
		t.Fatalf("a second subscription took the gateway's identifier of the first")
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices WHERE status = ?", "paid"); n != 2 {
		t.Errorf("%d invoices are paid", n)
	}
}

func TestARenewalTheGatewayChargesIsInvoicedWithoutACheckoutAndConfirmed(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	sub := w.held(t, owner(acme), plan, "sub_acme")
	checkouts := len(w.gateway.Checkouts())

	w.clock.Advance(30 * 24 * time.Hour)
	charge := w.gateway.Renew("sub_acme", 4)
	w.gateway.PayCharge(charge, 11990+3*2000)
	rec := w.post(w.gateway.ChargeNotification(w.address(), charge))
	if rec.Code != http.StatusOK {
		t.Fatalf("the notification answered %d %s", rec.Code, rec.Body)
	}
	if len(w.gateway.Checkouts()) != checkouts {
		t.Fatal("the renewal asked the gateway for a checkout")
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices"); n != 2 {
		t.Fatalf("%d invoices", n)
	}
	var kind, status, provider, ref string
	var units int
	var total, amount int64
	query := "SELECT kind, status, provider, provider_ref, units, units_amount, total FROM billing_invoices WHERE charge_ref = ?"
	if w.db.Dialect() == data.DialectPostgres {
		query = numbered(query)
	}
	if err := w.sql.QueryRow(query, charge).Scan(&kind, &status, &provider, &ref, &units, &amount, &total); err != nil {
		t.Fatal(err)
	}
	if kind != "renewal" || status != "paid" || provider != "fake" || ref != charge || units != 4 || amount != 6000 || total != 17990 {
		t.Fatalf("the invoice is %s %s %s %s units %d (%d) total %d", kind, status, provider, ref, units, amount, total)
	}
	after := w.serving(t, acme)
	if after.ID != sub.ID || after.Units != 4 || after.Renewals != 1 || !after.CurrentPeriodEnd.Equal(billing.AddMonths(sub.CurrentPeriodEnd, 1)) {
		t.Fatalf("after the renewal the subscription is %+v (was %+v)", after, sub)
	}
	if w.events.count(billing.EventRenewed) != 1 {
		t.Errorf("the listener heard %v", w.events.names())
	}
	if n := w.count(t, "SELECT COUNT(*) FROM activity_log WHERE event = ? AND properties LIKE ?", "invoice.created", `%"origin":"gateway"%`); n != 1 {
		t.Errorf("%d entries record the invoice issued for the gateway's charge", n)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM activity_log WHERE event = ?", "subscription.units_changed"); n != 1 {
		t.Errorf("%d entries record the units the gateway reported", n)
	}

	// The same charge again answers 200 and does nothing more.
	for i := 0; i < 3; i++ {
		if rec := w.post(w.gateway.ChargeNotification(w.address(), charge)); rec.Code != http.StatusOK {
			t.Fatalf("the repeated notification answered %d %s", rec.Code, rec.Body)
		}
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices"); n != 2 {
		t.Errorf("a repeated notification issued an invoice: %d", n)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments"); n != 2 {
		t.Errorf("%d payments", n)
	}
	if again := w.serving(t, acme); !again.CurrentPeriodEnd.Equal(after.CurrentPeriodEnd) || again.Renewals != 1 {
		t.Errorf("a repeated notification moved the period: %+v", again)
	}
}

func TestARenewalWithoutUnitsUsesTheSubscriptionsOwn(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	sub := w.held(t, owner(acme), plan, "sub_acme")
	w.clock.Advance(30 * 24 * time.Hour)
	charge := w.gateway.Renew("sub_acme", 0)
	w.gateway.PayCharge(charge, 11990+2*2000)
	if rec := w.post(w.gateway.ChargeNotification(w.address(), charge)); rec.Code != http.StatusOK {
		t.Fatalf("answered %d %s", rec.Code, rec.Body)
	}
	if got := w.serving(t, acme); got.Units != 3 || got.Renewals != 1 || !got.CurrentPeriodEnd.After(sub.CurrentPeriodEnd) {
		t.Fatalf("the subscription is %+v", got)
	}
}

func TestNotificationsOfOneChargeRacingIssueOneInvoice(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	w.held(t, owner(acme), plan, "sub_acme")
	w.clock.Advance(30 * 24 * time.Hour)
	charge := w.gateway.Renew("sub_acme", 3)
	w.gateway.PayCharge(charge, 11990+2*2000)

	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.post(w.gateway.ChargeNotification(w.address(), charge))
		}()
	}
	wg.Wait()
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices"); n != 2 {
		t.Fatalf("%d invoices after six racing notifications of one charge", n)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments"); n != 2 {
		t.Fatalf("%d payments", n)
	}
	if got := w.serving(t, acme); got.Renewals != 1 {
		t.Fatalf("the subscription renewed %d times", got.Renewals)
	}
}

func TestAChargeOfAnotherAmountConfirmsNothingAndLeavesTheInvoiceOpen(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	sub := w.held(t, owner(acme), plan, "sub_acme")
	w.clock.Advance(30 * 24 * time.Hour)
	charge := w.gateway.Renew("sub_acme", 3)
	w.gateway.PayCharge(charge, 11990)

	if rec := w.post(w.gateway.ChargeNotification(w.address(), charge)); rec.Code != http.StatusConflict {
		t.Fatalf("answered %d %s, want 409", rec.Code, rec.Body)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices WHERE status = ?", "open"); n != 1 {
		t.Fatalf("%d open invoices", n)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices WHERE provider_status LIKE ?", "amount_mismatch%"); n != 1 {
		t.Errorf("the invoice was not annotated with the mismatch")
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments"); n != 1 {
		t.Errorf("%d payments", n)
	}
	if got := w.serving(t, acme); !got.CurrentPeriodEnd.Equal(sub.CurrentPeriodEnd) {
		t.Errorf("the period moved: %+v", got)
	}
	// Telling it again does not issue a second invoice for the same charge.
	w.post(w.gateway.ChargeNotification(w.address(), charge))
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices"); n != 2 {
		t.Errorf("%d invoices", n)
	}
}

func TestAChargeTheSubscriptionAlreadyPaysForIsNotInvoicedAgain(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	w.held(t, owner(acme), plan, "sub_acme")
	// The subscription was paid a moment ago for a month: a second charge
	// with another reference finds the period covered.
	charge := w.gateway.Renew("sub_acme", 3)
	w.gateway.PayCharge(charge, 11990+2*2000)
	if rec := w.post(w.gateway.ChargeNotification(w.address(), charge)); rec.Code != http.StatusOK {
		t.Fatalf("answered %d %s", rec.Code, rec.Body)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices"); n != 1 {
		t.Fatalf("%d invoices: a charge for a period already paid was invoiced", n)
	}
}

func TestAChargeOfASubscriptionNoOneHoldsIsNotFound(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	w.held(t, owner(acme), plan, "sub_acme")
	charge := w.gateway.Renew("sub_nobody", 3)
	w.gateway.PayCharge(charge, 11990)
	if rec := w.post(w.gateway.ChargeNotification(w.address(), charge)); rec.Code != http.StatusNotFound {
		t.Fatalf("answered %d %s", rec.Code, rec.Body)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices"); n != 1 {
		t.Errorf("%d invoices", n)
	}
}

func TestAChargeIsInvoicedToTheAccountThatHoldsTheSubscriptionAndToNoOther(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	w.held(t, owner(acme), plan, "sub_acme")
	w.held(t, owner(globex), plan, "sub_globex")
	w.clock.Advance(30 * 24 * time.Hour)
	charge := w.gateway.Renew("sub_globex", 3)
	w.gateway.PayCharge(charge, 11990+2*2000)
	if rec := w.post(w.gateway.ChargeNotification(w.address(), charge)); rec.Code != http.StatusOK {
		t.Fatalf("answered %d %s", rec.Code, rec.Body)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices WHERE tenant_id = ?", globex); n != 2 {
		t.Errorf("globex has %d invoices", n)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_invoices WHERE tenant_id = ?", acme); n != 1 {
		t.Errorf("acme has %d invoices", n)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_payments WHERE tenant_id = ?", acme); n != 1 {
		t.Errorf("acme has %d payments", n)
	}
	list, err := w.svc.InvoicesPage(context.Background(), owner(acme), billing.PageQuery{}, 25)
	if err != nil || list.Total != 1 {
		t.Errorf("acme reads %v, %v", list, err)
	}
}

func TestANoticeMovesTheSubscriptionToTheStateTheGatewaySaysOnce(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	w.held(t, owner(acme), plan, "sub_acme")
	address := w.address()
	state := func(to billing.SubscriptionStatus, units int) int {
		return w.post(w.gateway.StateNotification(address, "sub_acme", to, units)).Code
	}

	if code := state(billing.SubscriptionPastDue, 0); code != http.StatusOK {
		t.Fatalf("past_due answered %d", code)
	}
	if status, _, _ := w.subscriptionRow(t, "sub_acme"); status != "past_due" {
		t.Fatalf("the status is %s", status)
	}
	state(billing.SubscriptionPastDue, 0)
	if w.events.count(billing.EventPastDue) != 1 {
		t.Errorf("a repeated notice was heard again: %v", w.events.names())
	}
	if n := w.count(t, "SELECT COUNT(*) FROM activity_log WHERE event = ?", "subscription.moved"); n != 1 {
		t.Errorf("%d entries record the move", n)
	}

	// The period is still ahead, so the gateway may serve it again.
	state(billing.SubscriptionActive, 0)
	if status, _, ended := w.subscriptionRow(t, "sub_acme"); status != "active" || ended {
		t.Fatalf("the status is %s, ended %v", status, ended)
	}

	state(billing.SubscriptionExpired, 0)
	if status, _, ended := w.subscriptionRow(t, "sub_acme"); status != "expired" || !ended {
		t.Fatalf("the status is %s, ended %v", status, ended)
	}
	if w.events.count(billing.EventExpired) != 1 {
		t.Errorf("the listener heard %v", w.events.names())
	}
	state(billing.SubscriptionExpired, 0)
	state(billing.SubscriptionCanceled, 0) // an ended subscription stays as it ended
	state(billing.SubscriptionPastDue, 0)
	if status, _, _ := w.subscriptionRow(t, "sub_acme"); status != "expired" {
		t.Fatalf("the status is %s", status)
	}
	if w.events.count(billing.EventExpired) != 1 || w.events.count(billing.EventCanceled) != 0 {
		t.Errorf("the listener heard %v", w.events.names())
	}

	// Served again while the period is ahead, and canceled for good.
	state(billing.SubscriptionActive, 0)
	if status, _, ended := w.subscriptionRow(t, "sub_acme"); status != "active" || ended {
		t.Fatalf("the status is %s, ended %v", status, ended)
	}
	state(billing.SubscriptionCanceled, 0)
	if status, _, ended := w.subscriptionRow(t, "sub_acme"); status != "canceled" || !ended {
		t.Fatalf("the status is %s, ended %v", status, ended)
	}
	if w.events.count(billing.EventCanceled) != 1 {
		t.Errorf("the listener heard %v", w.events.names())
	}
}

func TestAnActiveNoticeWithThePeriodBehindChangesNothing(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	w.held(t, owner(acme), plan, "sub_acme")
	address := w.address()

	w.post(w.gateway.StateNotification(address, "sub_acme", billing.SubscriptionPastDue, 0))
	w.clock.Advance(40 * 24 * time.Hour)
	if rec := w.post(w.gateway.StateNotification(address, "sub_acme", billing.SubscriptionActive, 0)); rec.Code != http.StatusOK {
		t.Fatalf("answered %d", rec.Code)
	}
	if status, _, _ := w.subscriptionRow(t, "sub_acme"); status != "past_due" {
		t.Fatalf("a subscription whose period ended was served again: %s", status)
	}
}

func TestANoticeKeepsTheUnitsTheGatewayReports(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	w.held(t, owner(acme), plan, "sub_acme")

	w.post(w.gateway.StateNotification(w.address(), "sub_acme", billing.SubscriptionPastDue, 6))
	if status, units, _ := w.subscriptionRow(t, "sub_acme"); status != "past_due" || units != 6 {
		t.Fatalf("the subscription is %s with %d units", status, units)
	}
	// More than the plan sells is refused.
	if rec := w.post(w.gateway.StateNotification(w.address(), "sub_acme", billing.SubscriptionPastDue, billing.MaxUnits+1)); rec.Code == http.StatusOK {
		t.Error("units above the maximum were accepted")
	}

	sub, err := w.svc.SetProviderUnits(context.Background(), "fake", "sub_acme", 2)
	if err != nil || sub.Units != 2 {
		t.Fatalf("SetProviderUnits: %+v, %v", sub, err)
	}
	if _, err := w.svc.SetProviderUnits(context.Background(), "fake", "sub_nobody", 2); err == nil {
		t.Error("units of a subscription no one holds were set")
	}
}

func TestANoticeOfASubscriptionNoOneHoldsIsNotFoundAndTouchesNothing(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	w.held(t, owner(acme), plan, "sub_acme")
	for _, to := range []billing.SubscriptionStatus{billing.SubscriptionPastDue, billing.SubscriptionExpired, billing.SubscriptionCanceled, billing.SubscriptionActive} {
		if rec := w.post(w.gateway.StateNotification(w.address(), "sub_nobody", to, 0)); rec.Code != http.StatusNotFound {
			t.Errorf("%s answered %d", to, rec.Code)
		}
	}
	if status, _, _ := w.subscriptionRow(t, "sub_acme"); status != "active" {
		t.Errorf("the status is %s", status)
	}
	// A state the package does not take from a gateway is a bad notification.
	if rec := w.post(w.gateway.StateNotification(w.address(), "sub_acme", billing.SubscriptionTrialing, 0)); rec.Code != http.StatusBadRequest {
		t.Errorf("trialing answered %d", rec.Code)
	}
}

func TestANoticeMovesOnlyTheAccountThatHoldsTheSubscription(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.teamPlan(t)
	w.held(t, owner(acme), plan, "sub_acme")
	w.held(t, owner(globex), plan, "sub_globex")

	w.post(w.gateway.StateNotification(w.address(), "sub_globex", billing.SubscriptionCanceled, 0))
	if status, _, _ := w.subscriptionRow(t, "sub_globex"); status != "canceled" {
		t.Errorf("globex is %s", status)
	}
	if status, _, _ := w.subscriptionRow(t, "sub_acme"); status != "active" {
		t.Errorf("acme is %s", status)
	}
	subs, err := w.svc.ServingSubscriptions(context.Background(), member(acme))
	if err != nil || len(subs) != 1 {
		t.Errorf("acme serves %v, %v", subs, err)
	}
}

func TestTheUnitAndGatewayMigrationsGoDownAndUpAgain(t *testing.T) {
	t.Parallel()
	w := install(t)
	ctx := context.Background()
	all := w.module.Migrations()
	units, gateway := all[len(all)-2], all[len(all)-1]
	if units.GetName() != "20261124_0008_add_billing_units" || gateway.GetName() != "20261124_0009_add_gateway_subscriptions" {
		t.Fatalf("the last migrations are %s and %s", units.GetName(), gateway.GetName())
	}
	reversible := func(m interface{ GetName() string }) migrations.ReversibleMigration {
		r, ok := m.(migrations.ReversibleMigration)
		if !ok {
			t.Fatalf("%s is not reversible", m.GetName())
		}
		return r
	}
	columns := []string{"SELECT charge_ref FROM billing_invoices", "SELECT provider_ref FROM billing_subscriptions",
		"SELECT units_amount FROM billing_invoices", "SELECT unit_price FROM billing_plans"}

	if err := reversible(gateway).Down(ctx, w.conn); err != nil {
		t.Fatalf("going down: %v", err)
	}
	if err := reversible(units).Down(ctx, w.conn); err != nil {
		t.Fatalf("going down: %v", err)
	}
	for _, query := range columns {
		if _, err := w.sql.Exec(query); err == nil {
			t.Errorf("%s still works after the migrations went down", query)
		}
	}
	for _, m := range []foundation.Migration{units, gateway} {
		if err := m.Up(ctx, w.conn); err != nil {
			t.Fatalf("going up again: %v", err)
		}
	}
	for _, query := range columns {
		if _, err := w.sql.Exec(query); err != nil {
			t.Errorf("%s: %v", query, err)
		}
	}
}

func TestTheGatewayIdentifiersAreUniqueOnlyWhereTheyAreNotEmpty(t *testing.T) {
	t.Parallel()
	w := install(t)
	plan := w.plan(t, billing.PlanRequest{Name: "Plain", Price: 5000})
	// Subscriptions that never met a gateway all hold the empty identifier.
	for _, tenant := range []string{acme, globex, "initech"} {
		w.pay(t, w.subscribe(t, owner(tenant), plan.ID, "").Invoice)
	}
	if n := w.count(t, "SELECT COUNT(*) FROM billing_subscriptions WHERE provider_ref = ''"); n != 3 {
		t.Fatalf("%d subscriptions hold no gateway identifier", n)
	}
}
