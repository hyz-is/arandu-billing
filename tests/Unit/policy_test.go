package unit_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"

	billing "github.com/hyz-is/arandu-billing"
)

// The properties this package exists to keep are checked here against the
// code: the policy denies what no rule names, the tenant of an account is the
// subject's own, and the service refuses before it reaches the Model. The
// service is built over a handle that wraps a nil *sql.DB, so any statement
// issued after a refusal would panic -- which makes "the refusal happened
// before the Model" a fact the suite proves.

// everyAction is the whole set the policy answers about.
var everyAction = []security.Action{
	billing.PlansView, billing.PlansManage, billing.CouponsManage, billing.Subscribe,
	billing.SubscriptionsView, billing.SubscriptionsCancel, billing.SubscriptionsManage,
	billing.InvoicesView, billing.InvoicesManage, billing.PaymentsConfirm, billing.SubscriptionsSweep,
}

// adminActions are the ones only the platform's admin takes.
var adminActions = []security.Action{billing.PlansManage, billing.CouponsManage, billing.SubscriptionsManage, billing.InvoicesManage}

func member() security.Subject {
	return security.Subject{ID: "user-1", Tenant: "acme", Roles: []string{"admin"}, Verified: true}
}

func manager() security.Subject {
	return security.Subject{ID: "user-2", Tenant: "acme", Roles: []string{billing.AccountRole}, Verified: true}
}

func platformAdmin() security.Subject {
	return security.Subject{ID: "user-9", Tenant: "platform", Roles: []string{billing.AdminRole}, Verified: true}
}

func can(s security.Subject, a security.Action, tenant string) error {
	return billing.BillingPolicy{}.Can(context.Background(), s, a, billing.Account{TenantID: tenant})
}

func TestAGuestReachesNothing(t *testing.T) {
	t.Parallel()
	for _, a := range everyAction {
		if err := can(security.Guest("acme"), a, ""); err == nil {
			t.Errorf("a guest was allowed %s", a)
		}
	}
}

func TestAMemberReadsTheirOwnAccountAndNothingElse(t *testing.T) {
	t.Parallel()
	for _, a := range []security.Action{billing.PlansView, billing.SubscriptionsView, billing.InvoicesView} {
		if err := can(member(), a, "acme"); err != nil {
			t.Errorf("a member was refused %s on their own account: %v", a, err)
		}
		if err := can(member(), a, "other"); err == nil {
			t.Errorf("a member was allowed %s on another account", a)
		}
	}
	for _, a := range []security.Action{billing.Subscribe, billing.SubscriptionsCancel} {
		if err := can(member(), a, "acme"); err == nil {
			t.Errorf("a member who does not manage the account was allowed %s", a)
		}
	}
	for _, a := range append(adminActions, billing.PaymentsConfirm, billing.SubscriptionsSweep) {
		if err := can(member(), a, "acme"); err == nil {
			t.Errorf("a member, even one holding a role named admin, was allowed %s", a)
		}
	}
}

func TestTheAccountManagerPaysAndCancelsTheirOwnAccountOnly(t *testing.T) {
	t.Parallel()
	for _, a := range []security.Action{billing.Subscribe, billing.SubscriptionsCancel} {
		if err := can(manager(), a, "acme"); err != nil {
			t.Errorf("the manager was refused %s: %v", a, err)
		}
		if err := can(manager(), a, "other"); err == nil {
			t.Errorf("the manager was allowed %s on another account", a)
		}
	}
	action := security.Subject{ID: "user-3", Tenant: "acme", Actions: []security.Action{billing.AccountAction}}
	if err := can(action, billing.Subscribe, "acme"); err != nil {
		t.Errorf("the manager held as an arandu-permission action was refused: %v", err)
	}
}

func TestThePlatformAdminActsOnEveryAccountForTheAdminActionsAlone(t *testing.T) {
	t.Parallel()
	for _, a := range adminActions {
		for _, tenant := range []string{"", "acme", "other"} {
			if err := can(platformAdmin(), a, tenant); err != nil {
				t.Errorf("the admin was refused %s on %q: %v", a, tenant, err)
			}
		}
	}
	for _, a := range []security.Action{billing.PaymentsConfirm, billing.SubscriptionsSweep, billing.Subscribe} {
		if err := can(platformAdmin(), a, "acme"); err == nil {
			t.Errorf("the admin was allowed %s inside a customer's account", a)
		}
	}
	impersonating := platformAdmin()
	impersonating.Impersonator = &security.Subject{ID: "user-9"}
	if billing.IsAdmin(impersonating) {
		t.Error("a subject inside somebody else's account is the platform's admin")
	}
}

func TestEveryActionButTheReadsWrites(t *testing.T) {
	t.Parallel()
	reads := map[security.Action]bool{billing.PlansView: true, billing.SubscriptionsView: true, billing.InvoicesView: true}
	for _, a := range everyAction {
		if got := (billing.BillingPolicy{}).Writes(a); got == reads[a] {
			t.Errorf("Writes(%s) = %v", a, got)
		}
	}
}

func nilHandle() *data.DB { return data.Wrap(nil, data.DialectSQLite) }

// TestTheServiceRefusesBeforeReachingTheModel calls every use case as somebody
// it must refuse, over a handle that panics on the first statement.
func TestTheServiceRefusesBeforeReachingTheModel(t *testing.T) {
	t.Parallel()
	svc := billing.NewBillingService(nilHandle())
	ctx := context.Background()
	guest := security.Guest("acme")
	calls := map[string]func() error{
		"ActivePlans":  func() error { _, err := svc.ActivePlans(ctx, guest); return err },
		"Plan":         func() error { _, err := svc.Plan(ctx, member(), "p1"); return err },
		"CreatePlan":   func() error { _, err := svc.CreatePlan(ctx, member(), validPlan()); return err },
		"UpdatePlan":   func() error { _, err := svc.UpdatePlan(ctx, member(), "p1", validPlan()); return err },
		"CreateCoupon": func() error { _, err := svc.CreateCoupon(ctx, member(), validCoupon()); return err },
		"Subscribe": func() error {
			_, err := svc.Subscribe(ctx, member(), billing.SubscribeRequest{PlanID: "p1"})
			return err
		},
		"Renew":           func() error { _, err := svc.Renew(ctx, member(), "s1", ""); return err },
		"StartTrial":      func() error { _, err := svc.StartTrial(ctx, member(), "p1", ""); return err },
		"Cancel":          func() error { _, err := svc.CancelAtPeriodEnd(ctx, member(), "s1"); return err },
		"Resume":          func() error { _, err := svc.ResumeSubscription(ctx, member(), "s1"); return err },
		"CancelNow":       func() error { _, err := svc.CancelNow(ctx, manager(), "s1"); return err },
		"ConfirmManual":   func() error { _, err := svc.ConfirmManually(ctx, manager(), "i1", billing.ManualPayment{}); return err },
		"VoidInvoice":     func() error { _, err := svc.VoidInvoice(ctx, manager(), "i1"); return err },
		"Invoice":         func() error { _, err := svc.Invoice(ctx, guest, "i1"); return err },
		"Refresh":         func() error { _, err := svc.RefreshInvoice(ctx, guest, "i1"); return err },
		"InvoicesPage":    func() error { _, err := svc.InvoicesPage(ctx, guest, billing.PageQuery{}, 25); return err },
		"AllInvoices":     func() error { _, err := svc.AllInvoicesPage(ctx, manager(), billing.PageQuery{}, 25); return err },
		"AllSubs":         func() error { _, err := svc.AllSubscriptionsPage(ctx, manager(), billing.PageQuery{}, 25); return err },
		"PlansPage":       func() error { _, err := svc.PlansPage(ctx, manager(), billing.PageQuery{}, 25); return err },
		"CouponsPage":     func() error { _, err := svc.CouponsPage(ctx, manager(), billing.PageQuery{}, 25); return err },
		"SubsPage":        func() error { _, err := svc.SubscriptionsPage(ctx, guest, billing.PageQuery{}, 25); return err },
		"SubscriptionsOf": func() error { _, err := svc.SubscriptionsOf(ctx, security.Grant{}); return err },
	}
	for name, call := range calls {
		err := call()
		if err == nil {
			t.Errorf("%s was not refused", name)
			continue
		}
		if !errors.Is(err, security.ErrForbidden) {
			t.Errorf("%s answered %v, want a refusal", name, err)
		}
	}
}

func TestASystemGrantWithoutATenantReachesNothing(t *testing.T) {
	t.Parallel()
	if err := security.SystemGrant(billing.PaymentsConfirm, "").Check(billing.PaymentsConfirm); err == nil {
		t.Fatal("a system Grant with no tenant passed its check")
	}
}

func validPlan() billing.PlanRequest {
	return billing.PlanRequest{Name: "Câmera mensal", Price: 9900, IntervalMonths: 1, Active: true}
}

func validCoupon() billing.CouponRequest {
	return billing.CouponRequest{Code: "bemvindo", Kind: billing.CouponPercent, Value: 1000, Active: true}
}

func TestTheRequestsValidateTheirInput(t *testing.T) {
	t.Parallel()
	if errs := validPlan().Validate(); errs.Any() {
		t.Fatalf("a valid plan was refused: %v", errs)
	}
	bad := validPlan()
	bad.Name, bad.Price, bad.IntervalMonths, bad.InstallmentsCap = "", -1, 0, 13
	errs := bad.Validate()
	for _, field := range []string{"name", "price", "interval_months", "installments_cap"} {
		if len(errs.Get(field)) == 0 {
			t.Errorf("a plan with a bad %s was accepted", field)
		}
	}
	if errs := validCoupon().Validate(); errs.Any() {
		t.Fatalf("a valid coupon was refused: %v", errs)
	}
	coupon := validCoupon()
	coupon.Code, coupon.Value = "não vale", 10001
	errs = coupon.Validate()
	for _, field := range []string{"code", "value"} {
		if len(errs.Get(field)) == 0 {
			t.Errorf("a coupon with a bad %s was accepted", field)
		}
	}
	if errs := (billing.SubscribeRequest{}).Validate(); len(errs.Get("plan_id")) == 0 {
		t.Error("a checkout with no plan was accepted")
	}
}

func TestTheConfigurationRefusesWhatCannotWork(t *testing.T) {
	t.Parallel()
	manual := []billing.Provider{billing.ManualProvider{}}
	for name, cfg := range map[string]billing.Config{
		"no provider":        {},
		"two of one name":    {Providers: []billing.Provider{billing.ManualProvider{}, billing.ManualProvider{}}},
		"unknown default":    {Providers: manual, DefaultProvider: "gateway"},
		"relative prefix":    {Providers: manual, Prefix: "billing"},
		"page too large":     {Providers: manual, PageSize: billing.MaxPageSize + 1},
		"segment wildcard":   {Providers: manual, Paths: billing.Paths{Plans: "/{x}"}},
		"relative public":    {Providers: manual, PublicURL: "alpr.sh"},
		"colliding segments": {Providers: manual, Paths: billing.Paths{Plans: "/x", Subscriptions: "/x", Checkout: "/y", Renewal: "/y"}},
	} {
		if err := cfg.Validate(); err == nil {
			t.Errorf("%s: the configuration was accepted", name)
		}
	}
	if err := (billing.Config{Providers: manual, Paths: billing.PortuguesePaths}).Validate(); err != nil {
		t.Fatalf("the Portuguese paths were refused: %v", err)
	}
	if !strings.HasPrefix(billing.DefaultNotificationPrefix, "/api/") {
		t.Error("the notification address left the API side")
	}
}
