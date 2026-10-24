package billing

import (
	"fmt"
	stdhttp "net/http"
	"strings"
)

// Paths are the segments the routes of this package are built from. Each one
// starts with "/", ends without one and holds no wildcard.
//
//	GET    Prefix + Plans                                the catalogue
//	POST   Prefix + Plans + /{id} + Checkout             pay for a plan
//	POST   Prefix + Plans + /{id} + Trial                start its trial
//	GET    Prefix + Subscriptions                        the account's subscriptions
//	POST   Prefix + Subscriptions + /{id} + Renewal      pay the next period
//	POST   Prefix + Subscriptions + /{id} + Cancellation stop at the period's end
//	DELETE Prefix + Subscriptions + /{id} + Cancellation take that back
//	GET    Prefix + Invoices                             the account's invoices
//	GET    Prefix + Invoices + /{id}                     one invoice
//	GET    Prefix + Invoices + /{id} + Return            back from the gateway
//
// and under AdminPrefix: Plans (with /{id}), Coupons (with /{id}),
// Subscriptions (with Cancellation) and Invoices (with Payment and
// Cancellation).
type Paths struct {
	Plans, Checkout, Trial, Subscriptions, Renewal, Cancellation string
	Invoices, Return, Admin, Coupons, Payment                    string
}

// DefaultPaths are the English segments.
var DefaultPaths = Paths{
	Plans: "/plans", Checkout: "/checkout", Trial: "/trial", Subscriptions: "/subscriptions",
	Renewal: "/renewal", Cancellation: "/cancellation", Invoices: "/invoices", Return: "/return",
	Admin: "/admin", Coupons: "/coupons", Payment: "/payment",
}

// PortuguesePaths are the segments in Brazilian Portuguese, for an
// application whose addresses are written in it: /planos/{id}/assinar,
// /assinaturas/{id}/cancelamento, /faturas/{id}/retorno, and the admin's
// /cupons and /faturas/{id}/pagamento.
var PortuguesePaths = Paths{
	Plans: "/planos", Checkout: "/assinar", Trial: "/teste-gratis", Subscriptions: "/assinaturas",
	Renewal: "/renovacao", Cancellation: "/cancelamento", Invoices: "/faturas", Return: "/retorno",
	Admin: "/admin", Coupons: "/cupons", Payment: "/pagamento",
}

// fields are the segments with their names, for validating and filling them.
func (p *Paths) fields() []struct {
	name  string
	value *string
} {
	return []struct {
		name  string
		value *string
	}{
		{"Plans", &p.Plans}, {"Checkout", &p.Checkout}, {"Trial", &p.Trial},
		{"Subscriptions", &p.Subscriptions}, {"Renewal", &p.Renewal}, {"Cancellation", &p.Cancellation},
		{"Invoices", &p.Invoices}, {"Return", &p.Return}, {"Admin", &p.Admin},
		{"Coupons", &p.Coupons}, {"Payment", &p.Payment},
	}
}

// validate reports a segment that cannot be part of a route.
func (p Paths) validate() error {
	for _, f := range p.fields() {
		v := *f.value
		if v == "" {
			continue
		}
		if v[0] != '/' || v == "/" || strings.HasSuffix(v, "/") || strings.ContainsAny(v, "{} \t?#") {
			return fmt.Errorf("billing: Config.Paths.%s is %q: a segment starts with /, ends without one and holds no wildcard", f.name, v)
		}
	}
	return nil
}

// withDefaults fills every empty segment from DefaultPaths.
func (p Paths) withDefaults() Paths {
	d := DefaultPaths
	defaults := d.fields()
	for i, f := range p.fields() {
		if *f.value == "" {
			*f.value = *defaults[i].value
		}
	}
	return p
}

// URLs are the addresses of this package's routes, built from Config.Prefix,
// Config.AdminPrefix and Config.Paths. The route table is built from the same
// methods with the wildcard in place of the identifier, so an address a screen
// links to and the pattern that answers it are one spelling.
type URLs struct {
	prefix, admin, notifications string
	p                            Paths
}

// URLs answers the addresses the module is mounted at, for an application that
// links to them from its own pages.
func (m *Module) URLs() URLs { return urlsOf(m.cfg) }

// NewURLs builds the addresses for the prefixes and the paths, as New would
// from a Config holding them: empty segments take DefaultPaths, an empty admin
// prefix is the prefix followed by Paths.Admin, and an empty notification
// prefix is DefaultNotificationPrefix.
func NewURLs(prefix, adminPrefix, notificationPrefix string, paths Paths) URLs {
	paths = paths.withDefaults()
	if adminPrefix == "" {
		adminPrefix = prefix + paths.Admin
	}
	if notificationPrefix == "" {
		notificationPrefix = DefaultNotificationPrefix
	}
	return URLs{prefix: prefix, admin: adminPrefix, notifications: notificationPrefix, p: paths}
}

func urlsOf(c Config) URLs {
	return URLs{prefix: c.Prefix, admin: c.AdminPrefix, notifications: c.NotificationPrefix, p: c.Paths}
}

// Prefix is where the account's routes are mounted.
func (u URLs) Prefix() string { return u.prefix }

// AdminPrefix is where the admin's routes are mounted.
func (u URLs) AdminPrefix() string { return u.admin }

// Plans is the catalogue, as an account reads it.
func (u URLs) Plans() string { return u.prefix + u.p.Plans }

// Checkout pays for one plan.
func (u URLs) Checkout(planID string) string { return u.Plans() + "/" + planID + u.p.Checkout }

// Trial starts one plan's trial.
func (u URLs) Trial(planID string) string { return u.Plans() + "/" + planID + u.p.Trial }

// Subscriptions are the account's subscriptions.
func (u URLs) Subscriptions() string { return u.prefix + u.p.Subscriptions }

// Renew pays the next period of one subscription.
func (u URLs) Renew(id string) string { return u.Subscriptions() + "/" + id + u.p.Renewal }

// Cancel asks one subscription to stop at the end of its period (POST), or
// takes that back (DELETE).
func (u URLs) Cancel(id string) string { return u.Subscriptions() + "/" + id + u.p.Cancellation }

// Invoices are the account's invoices.
func (u URLs) Invoices() string { return u.prefix + u.p.Invoices }

// Invoice is one of the account's invoices.
func (u URLs) Invoice(id string) string { return u.Invoices() + "/" + id }

// Return is where the gateway sends the customer back after paying one
// invoice.
func (u URLs) Return(id string) string { return u.Invoice(id) + u.p.Return }

// Notification is where one provider posts.
func (u URLs) Notification(provider string) string { return u.notifications + "/" + provider }

// AdminPlans is the catalogue, for the admin who keeps it.
func (u URLs) AdminPlans() string { return u.admin + u.p.Plans }

// AdminPlan is one plan, for the admin.
func (u URLs) AdminPlan(id string) string { return u.AdminPlans() + "/" + id }

// AdminCoupons are the coupons.
func (u URLs) AdminCoupons() string { return u.admin + u.p.Coupons }

// AdminCoupon is one coupon, for the admin.
func (u URLs) AdminCoupon(id string) string { return u.AdminCoupons() + "/" + id }

// AdminSubscriptions are every account's subscriptions.
func (u URLs) AdminSubscriptions() string { return u.admin + u.p.Subscriptions }

// AdminCancel cancels one subscription at once.
func (u URLs) AdminCancel(id string) string {
	return u.AdminSubscriptions() + "/" + id + u.p.Cancellation
}

// AdminInvoices are every account's invoices.
func (u URLs) AdminInvoices() string { return u.admin + u.p.Invoices }

// AdminConfirm records one invoice paid by hand.
func (u URLs) AdminConfirm(id string) string { return u.AdminInvoices() + "/" + id + u.p.Payment }

// AdminVoid cancels one open invoice.
func (u URLs) AdminVoid(id string) string {
	return u.AdminInvoices() + "/" + id + u.p.Cancellation
}

// route is one entry of the table the module registers and Validate parses.
type route struct {
	method, path, name string
	admin              bool
}

// routeTable is every route behind a session, by the name it is registered
// under. The names never change with the paths.
func routeTable(c Config) []route {
	u := urlsOf(c)
	const id = "{id}"
	get, post, del := stdhttp.MethodGet, stdhttp.MethodPost, stdhttp.MethodDelete
	return []route{
		{get, u.Plans(), "billing.plans.index", false},
		{post, u.Checkout(id), "billing.plans.checkout", false},
		{post, u.Trial(id), "billing.plans.trial", false},
		{get, u.Subscriptions(), "billing.subscriptions.index", false},
		{post, u.Renew(id), "billing.subscriptions.renew", false},
		{post, u.Cancel(id), "billing.subscriptions.cancel", false},
		{del, u.Cancel(id), "billing.subscriptions.resume", false},
		{get, u.Invoices(), "billing.invoices.index", false},
		{get, u.Invoice(id), "billing.invoices.show", false},
		{get, u.Return(id), "billing.invoices.return", false},

		{get, u.AdminPlans(), "billing.admin.plans.index", true},
		{post, u.AdminPlans(), "billing.admin.plans.store", true},
		{get, u.AdminPlan(id), "billing.admin.plans.edit", true},
		{post, u.AdminPlan(id), "billing.admin.plans.update", true},
		{get, u.AdminCoupons(), "billing.admin.coupons.index", true},
		{post, u.AdminCoupons(), "billing.admin.coupons.store", true},
		{post, u.AdminCoupon(id), "billing.admin.coupons.update", true},
		{get, u.AdminSubscriptions(), "billing.admin.subscriptions.index", true},
		{post, u.AdminCancel(id), "billing.admin.subscriptions.cancel", true},
		{get, u.AdminInvoices(), "billing.admin.invoices.index", true},
		{post, u.AdminConfirm(id), "billing.admin.invoices.confirm", true},
		{post, u.AdminVoid(id), "billing.admin.invoices.cancel", true},
	}
}
