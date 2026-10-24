package feature_test

import (
	"net/http"
	"strings"
	"testing"

	billing "github.com/hyz-is/arandu-billing"
)

// TestEveryRouteSendsAVisitorWithNoSessionToSignIn walks the route table the
// module registered, so a route added later is held without anybody listing
// it here: every route but the gateways' notification sends a request with no
// session to the sign-in screen, before any handler, policy or statement.
func TestEveryRouteSendsAVisitorWithNoSessionToSignIn(t *testing.T) {
	t.Parallel()
	w := install(t)
	seen := 0
	for _, route := range w.router.Routes() {
		if route.Module != w.module.Name() || route.RouteName() == "billing.notifications" {
			continue
		}
		seen++
		target := strings.ReplaceAll(route.Pattern, "{id}", "x")
		rec := w.do(t, route.Method, target, "", false)
		if rec.Code != http.StatusFound && rec.Code != http.StatusSeeOther {
			t.Errorf("%s %s answered a visitor with no session %d", route.Method, target, rec.Code)
		}
	}
	if seen != 22 {
		t.Errorf("walked %d routes behind a session, want 22", seen)
	}
}

// TestEveryAdminRouteRefusesAnAccountsManager holds the other side: a signed-in
// subject who manages an account, and nothing more, is refused every route of
// the admin's.
func TestEveryAdminRouteRefusesAnAccountsManager(t *testing.T) {
	t.Parallel()
	w := install(t)
	cookies := w.signIn(t, owner(acme))
	admin := w.module.URLs().AdminPrefix()
	seen := 0
	for _, route := range w.router.Routes() {
		if route.Module != w.module.Name() || !strings.HasPrefix(route.Pattern, admin+"/") {
			continue
		}
		seen++
		target := strings.ReplaceAll(route.Pattern, "{id}", "x")
		if rec := w.do(t, route.Method, target, "active=1&name=x&price_brl=1&interval_months=1&code=X&kind=fixed&value=1", true, cookies...); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s answered the account's manager %d", route.Method, target, rec.Code)
		}
	}
	if seen != 12 {
		t.Errorf("walked %d admin routes, want 12", seen)
	}
}

// TestTheRoutesFollowTheConfiguredPrefixes holds that no address is a literal:
// the application decides where the package is mounted.
func TestTheRoutesFollowTheConfiguredPrefixes(t *testing.T) {
	t.Parallel()
	w := install(t)
	u := w.module.URLs()
	for _, route := range w.router.Routes() {
		if route.Module != w.module.Name() {
			continue
		}
		if !strings.HasPrefix(route.Pattern, u.Prefix()+"/") && !strings.HasPrefix(route.Pattern, u.AdminPrefix()+"/") &&
			!strings.HasPrefix(route.Pattern, billing.DefaultNotificationPrefix+"/") {
			t.Errorf("%s landed outside the configured prefixes", route.Pattern)
		}
	}
	if u.Checkout("p1") != "/painel/planos/p1/assinar" || u.AdminConfirm("i1") != "/admin/cobranca/faturas/i1/pagamento" {
		t.Errorf("the Portuguese addresses are %s and %s", u.Checkout("p1"), u.AdminConfirm("i1"))
	}
}
