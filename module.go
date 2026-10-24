// Package billing is an Arandu module for billing management: a catalogue of
// plans and coupons, the subscriptions of each account and the date each one
// is paid until, and the invoices a payment gateway collects.
//
// It ships no gateway. An application implements Provider as a service of its
// own -- over its gateway's client and its own merchant account -- and hands
// it to New; the package asks it for a checkout page, believes a notification
// only after asking it again, and confirms each invoice once. Balances are not
// kept here: an application that turns a payment into credits deposits them
// in arandu-wallet from its Listener.
//
// The files are laid out by role rather than by layer:
//
//	module.go         -> registration, routes, handlers and migrations
//	notifications.go  -> the gateways' notification address
//	config.go         -> what the application passes in
//	paths.go          -> the addresses, and the route table
//	model.go          -> the entities, and their own rules
//	pricing.go        -> what a period costs, and what the clock does to it
//	provider.go       -> the gateway contract, and the gateway-free provider
//	policy.go         -> who may do what
//	*_service.go      -> the use cases, and Model access after authorization
//	listing.go        -> the server-side pages the data tables read
//	views.go          -> the files the application takes ownership of
//
// An application registers it explicitly. There is no service provider, no
// container and no discovery.
package billing

import (
	"context"
	"errors"
	"fmt"
	stdhttp "net/http"
	"strconv"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/http/middleware"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/database/schema"
	hview "github.com/arandu-io/hesape/view"
)

// Module is what the application registers.
type Module struct {
	cfg      Config
	svc      *BillingService
	sessions *security.SessionStore
}

// Compile-time proof that the module honors the contracts it claims.
var (
	_ foundation.Module      = (*Module)(nil)
	_ foundation.Migratable  = (*Module)(nil)
	_ foundation.Bootable    = (*Module)(nil)
	_ foundation.Publishable = (*Module)(nil)
)

// New returns the module, or the reason it cannot be built.
func New(cfg Config, db *data.DB, sessions *security.SessionStore) (*Module, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, errors.New("billing: New needs a database handle: this package owns its tables, and there is no in-memory mode that would let it start without one")
	}
	if sessions == nil {
		return nil, errors.New("billing: New needs a session store: every route of an account is mounted behind RequireAuth, which reads it")
	}
	cfg = cfg.withDefaults()
	svc := NewBillingService(db)
	svc.useProviders(cfg.Providers, cfg.DefaultProvider)
	svc.listener = cfg.Listener
	svc.customer = cfg.Customer
	svc.activity = cfg.Activity
	if cfg.Clock != nil {
		clock := cfg.Clock
		svc.now = func() time.Time { return clock().UTC() }
	}
	m := &Module{cfg: cfg, svc: svc, sessions: sessions}
	if cfg.PublicURL != "" {
		u := m.URLs()
		svc.returnURL = func(id string) string { return cfg.PublicURL + u.Return(id) }
	}
	labels := m.Labels(cfg.Locale)
	svc.describe = func(event string) string { return labels.T("activity." + strings.ReplaceAll(event, ".", "_")) }
	svc.labels = func(ctx context.Context) Labels { return m.Labels(m.locale(ctx)) }
	return m, nil
}

// Service is the use cases, for an application that calls them from its own
// code: its own pages, a job, the engine's sync. Every method asks the
// policy, so handing it out opens nothing.
func (m *Module) Service() *BillingService { return m.svc }

// Name is the module identifier.
func (m *Module) Name() string { return "billing" }

// Routes registers every route of the module: the account's, the admin's and
// the gateways' notification address.
func (m *Module) Routes(r *fhttp.Router) {
	m.AccountRoutes(r)
	m.AdminRoutes(r)
	m.NotificationRoutes(r)
}

// AccountRoutes registers the account's routes alone, under Prefix, for an
// application that mounts them inside its own customer panel with that side's
// guards on r.
func (m *Module) AccountRoutes(r *fhttp.Router) { m.register(r, false) }

// AdminRoutes registers the admin's routes alone, under AdminPrefix, for an
// application that mounts them inside its operator console.
func (m *Module) AdminRoutes(r *fhttp.Router) { m.register(r, true) }

func (m *Module) register(r *fhttp.Router, admin bool) {
	guards := []fhttp.Middleware{middleware.RequireAuth(m.sessions)}
	for _, mw := range m.cfg.Middleware {
		guards = append(guards, mw)
	}
	if admin {
		for _, mw := range m.cfg.AdminMiddleware {
			guards = append(guards, mw)
		}
	}
	r = r.Group("", guards...)
	handlers := m.handlers()
	for _, rt := range routeTable(m.cfg) {
		if rt.admin != admin {
			continue
		}
		r.Action(rt.method, rt.path, m.localized(handlers[rt.name])).Name(rt.name)
	}
}

// handlers are the handlers of the route table, by route name.
func (m *Module) handlers() map[string]func(*fhttp.Context) error {
	return map[string]func(*fhttp.Context) error{
		"billing.plans.index":                m.plans,
		"billing.plans.checkout":             m.checkout,
		"billing.plans.trial":                m.trial,
		"billing.subscriptions.index":        m.subscriptions,
		"billing.subscriptions.renew":        m.renew,
		"billing.subscriptions.cancel":       m.cancel,
		"billing.subscriptions.resume":       m.resume,
		"billing.invoices.index":             m.invoices,
		"billing.invoices.show":              m.invoice,
		"billing.invoices.return":            m.returned,
		"billing.admin.plans.index":          m.adminPlans,
		"billing.admin.plans.store":          m.storePlan,
		"billing.admin.plans.edit":           m.editPlan,
		"billing.admin.plans.update":         m.updatePlan,
		"billing.admin.coupons.index":        m.adminCoupons,
		"billing.admin.coupons.store":        m.storeCoupon,
		"billing.admin.coupons.update":       m.updateCoupon,
		"billing.admin.subscriptions.index":  m.adminSubscriptions,
		"billing.admin.subscriptions.cancel": m.adminCancel,
		"billing.admin.invoices.index":       m.adminInvoices,
		"billing.admin.invoices.confirm":     m.adminConfirm,
		"billing.admin.invoices.cancel":      m.adminVoid,
	}
}

// screen is what every page of this package is drawn with.
func (m *Module) screen(ctx *fhttp.Context, who security.Subject, titleKey string, admin bool) (Screen, error) {
	labels := m.Labels(m.locale(ctx.Ctx()))
	title := labels.T(titleKey)
	page := hview.New(ctx, title)
	page.Title = m.documentTitle(title, page.AppName)
	page.Authenticated = who.ID != ""
	sc := Screen{Page: page, Labels: labels, URLs: m.URLs(), loc: m.cfg.Location}
	if m.cfg.Chrome != nil {
		chrome, err := m.cfg.Chrome(ctx, ChromeRequest{Title: title, Admin: admin, Subject: who, Page: page})
		if err != nil {
			return Screen{}, err
		}
		sc.chrome = chrome
	}
	return sc, nil
}

// documentTitle is what the browser tab shows for a screen.
func (m *Module) documentTitle(title, appName string) string {
	if m.cfg.TitleSeparator == "" || strings.TrimSpace(appName) == "" {
		return title
	}
	return title + m.cfg.TitleSeparator + appName
}

// localized answers a rejected input in the request's language.
func (m *Module) localized(handler func(*fhttp.Context) error) func(*fhttp.Context) error {
	return func(ctx *fhttp.Context) error {
		err := handler(ctx)
		var errs validation.Errors
		if err == nil || !errors.As(err, &errs) {
			return err
		}
		return m.Labels(m.locale(ctx.Ctx())).Errors(errs)
	}
}

// PublishCommand is what an application runs to take ownership of the views
// this package offers.
const PublishCommand = "aru vendor:publish --apply"

// Boot refuses to serve when a view this package renders is not in the binary,
// or when the archive would publish a file outside the module's directory.
func (m *Module) Boot(context.Context) error {
	prefix := viewRoot + "/" + moduleDir + "/" + m.Name() + "/"
	var stray []string
	for _, path := range PublishedPaths() {
		if !strings.HasPrefix(path, prefix) {
			stray = append(stray, path)
		}
	}
	if len(stray) > 0 {
		return fmt.Errorf("billing: %s would be published outside %s, where it lands on a file the application wrote",
			strings.Join(stray, ", "), prefix)
	}
	registered := make(map[string]bool)
	for _, name := range hview.Registered() {
		registered[name] = true
	}
	var missingViews []string
	for _, name := range ViewNames() {
		if !registered[name] {
			missingViews = append(missingViews, name)
		}
	}
	if len(missingViews) > 0 {
		imports := make([]string, 0, len(ViewPackages()))
		for _, pkg := range ViewPackages() {
			imports = append(imports, "<module path>/"+pkg)
		}
		return fmt.Errorf("billing: no view is registered as %s. Run `%s`, then `aru view:build`, then import %s in bootstrap/app.go",
			strings.Join(missingViews, ", "), PublishCommand, strings.Join(imports, ", "))
	}
	return nil
}

// Schedule declares the sweep that moves the subscriptions whose period
// ended: every fifteen minutes, on one replica.
func (m *Module) Schedule() []foundation.Task {
	return []foundation.Task{{
		ID:        "billing.sweep",
		Spec:      "*/15 * * * *",
		Scope:     foundation.Global,
		Timeout:   10 * time.Minute,
		Singleton: true,
		Action:    SubscriptionsSweep,
		Run: func(ctx context.Context, _ security.Grant) error {
			_, err := m.svc.SweepDue(ctx, time.Now())
			return err
		},
	}}
}

// Handlers are thin on purpose: read the input, ask the service, answer. Who
// is asking comes from ctx.User, which reads the subject RequireAuth put on
// the request. An error from the service is returned as it is, and the router
// answers it.

// pageQuery reads a data table's query string.
func (m *Module) pageQuery(ctx *fhttp.Context) PageQuery {
	page, _ := strconv.Atoi(ctx.Query("page"))
	perPage, _ := strconv.Atoi(ctx.Query("per_page"))
	return PageQuery{
		Search: ctx.Query("q"), Status: ctx.Query("status"), Sort: ctx.Query("sort"), Dir: ctx.Query("dir"),
		Page: page, PerPage: perPage,
	}
}

// plans is the catalogue, as an account reads it.
func (m *Module) plans(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	rows, err := m.svc.ActivePlans(ctx.Ctx(), who)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		items := make([]map[string]any, 0, len(rows))
		for _, p := range rows {
			items = append(items, NewPlanResource(*p).ToArray())
		}
		return ctx.JSON(stdhttp.StatusOK, list{items: items, total: len(items)})
	}
	sc, err := m.screen(ctx, who, "screen.plans_title", false)
	if err != nil {
		return err
	}
	return ctx.View(ViewPlans, PlansPageData{Screen: sc, Plans: planCards(sc, rows), CanSubscribe: IsAccountManager(who)})
}

// checkout pays for one plan, with the coupon typed.
func (m *Module) checkout(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	result, err := m.svc.Subscribe(ctx.Ctx(), who, SubscribeRequest{PlanID: ctx.Param("id"), CouponCode: ctx.Input("coupon")})
	if err != nil {
		return err
	}
	return m.sendToPayment(ctx, result)
}

// sendToPayment answers an issued invoice: the gateway's page, or the
// invoice's own.
func (m *Module) sendToPayment(ctx *fhttp.Context, result *CheckoutResult) error {
	if ctx.WantsJSON() {
		fields := NewInvoiceResource(*result.Invoice).ToArray()
		fields["url"] = result.URL
		return ctx.JSON(stdhttp.StatusCreated, fieldsResource(fields))
	}
	if result.URL != "" {
		return ctx.Redirect(result.URL)
	}
	return ctx.Redirect(m.URLs().Invoice(result.Invoice.ID))
}

// trial starts one plan's trial.
func (m *Module) trial(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	sub, err := m.svc.StartTrial(ctx.Ctx(), who, ctx.Param("id"), "")
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusCreated, NewSubscriptionResource(*sub))
	}
	return ctx.Redirect(m.URLs().Subscriptions())
}

// subscriptions are the account's subscriptions, a page at a time.
func (m *Module) subscriptions(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	q := m.pageQuery(ctx)
	page, err := m.svc.SubscriptionsPage(ctx.Ctx(), who, q, m.cfg.PageSize)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, subscriptionsList(page))
	}
	names, err := m.svc.PlanNames(ctx.Ctx(), who)
	if err != nil {
		return err
	}
	sc, err := m.screen(ctx, who, "screen.subscriptions_title", false)
	if err != nil {
		return err
	}
	return ctx.View(ViewSubscriptions, SubscriptionsPageData{
		Screen: sc, Rows: subscriptionRows(sc, page.Rows, names), Table: tableState(q, page.Page, page.Pages, page.Total, m.URLs().Subscriptions()),
		CanManage: IsAccountManager(who),
	})
}

// renew pays the next period of one subscription.
func (m *Module) renew(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	result, err := m.svc.Renew(ctx.Ctx(), who, ctx.Param("id"), "")
	if err != nil {
		return err
	}
	return m.sendToPayment(ctx, result)
}

// cancel asks one subscription to stop at the end of its period.
func (m *Module) cancel(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	sub, err := m.svc.CancelAtPeriodEnd(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewSubscriptionResource(*sub))
	}
	return ctx.Redirect(m.URLs().Subscriptions())
}

// resume takes a cancellation back.
func (m *Module) resume(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	sub, err := m.svc.ResumeSubscription(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewSubscriptionResource(*sub))
	}
	return ctx.Redirect(m.URLs().Subscriptions())
}

// invoices are the account's invoices, a page at a time.
func (m *Module) invoices(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	q := m.pageQuery(ctx)
	page, err := m.svc.InvoicesPage(ctx.Ctx(), who, q, m.cfg.PageSize)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, invoicesList(page.Rows, page.Total))
	}
	sc, err := m.screen(ctx, who, "screen.invoices_title", false)
	if err != nil {
		return err
	}
	return ctx.View(ViewInvoices, InvoicesPageData{
		Screen: sc, Rows: invoiceRows(sc, page.Rows, false), Table: tableState(q, page.Page, page.Pages, page.Total, m.URLs().Invoices()),
	})
}

// invoice is one of the account's invoices, with how to pay it.
func (m *Module) invoice(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	inv, err := m.svc.Invoice(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewInvoiceResource(*inv))
	}
	sc, err := m.screen(ctx, who, "screen.invoice_title", false)
	if err != nil {
		return err
	}
	rows := invoiceRows(sc, []*Invoice{inv}, false)
	return ctx.View(ViewInvoice, InvoicePageData{Screen: sc, Invoice: rows[0], Instructions: m.svc.instructions(inv)})
}

// returned is where the gateway sends the customer back: the gateway is asked
// again, and the invoice is shown as it stands.
func (m *Module) returned(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	inv, err := m.svc.RefreshInvoice(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewInvoiceResource(*inv))
	}
	return ctx.Redirect(m.URLs().Invoice(inv.ID))
}

// adminPlans is the catalogue, for the admin, with the form that adds a plan.
func (m *Module) adminPlans(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	q := m.pageQuery(ctx)
	page, err := m.svc.PlansPage(ctx.Ctx(), who, q, m.cfg.PageSize)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		items := make([]map[string]any, 0, len(page.Rows))
		for _, p := range page.Rows {
			items = append(items, NewPlanResource(*p).ToArray())
		}
		return ctx.JSON(stdhttp.StatusOK, list{items: items, total: page.Total})
	}
	sc, err := m.screen(ctx, who, "screen.admin_plans_title", true)
	if err != nil {
		return err
	}
	return ctx.View(ViewAdminPlans, AdminPlansPageData{
		Screen: sc, Rows: planRows(sc, page.Rows), Table: tableState(q, page.Page, page.Pages, page.Total, m.URLs().AdminPlans()),
		StoreURL: m.URLs().AdminPlans(), NewPlan: PlanForm{Active: true, IntervalMonths: "1", InstallmentsCap: strconv.Itoa(MaxInstallments)},
	})
}

// planRequest reads a plan's form.
func planRequest(ctx *fhttp.Context) (PlanRequest, error) {
	price, ok := parseReais(ctx.Input("price_brl"))
	if !ok && ctx.Input("price") != "" {
		price, _ = strconv.ParseInt(strings.TrimSpace(ctx.Input("price")), 10, 64)
		ok = true
	}
	if !ok {
		return PlanRequest{}, validation.Errors{"price_brl": {message("amount")}}
	}
	meta, err := parseMeta(ctx.Input("meta"))
	if err != nil {
		return PlanRequest{}, validation.Errors{"meta": {message("meta_lines")}}
	}
	return PlanRequest{
		Slug: ctx.Input("slug"), Name: ctx.Input("name"), Description: ctx.Input("description"),
		Features: strings.Split(ctx.Input("features"), "\n"), Price: price,
		IntervalMonths: atoi(ctx.Input("interval_months")), TrialDays: atoi(ctx.Input("trial_days")),
		GraceDays: atoi(ctx.Input("grace_days")), InstallmentsFrom: atoi(ctx.Input("installments_from")),
		InstallmentsCap: atoi(ctx.Input("installments_cap")), Meta: meta,
		Active: checked(ctx.Input("active")), Position: atoi(ctx.Input("position")),
	}, nil
}

// storePlan adds a plan.
func (m *Module) storePlan(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	in, err := planRequest(ctx)
	if err != nil {
		return err
	}
	p, err := m.svc.CreatePlan(ctx.Ctx(), who, in)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusCreated, NewPlanResource(*p))
	}
	return ctx.Redirect(m.URLs().AdminPlans())
}

// editPlan is one plan, with the form that changes it.
func (m *Module) editPlan(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	p, err := m.svc.Plan(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewPlanResource(*p))
	}
	sc, err := m.screen(ctx, who, "screen.admin_plan_title", true)
	if err != nil {
		return err
	}
	return ctx.View(ViewAdminPlan, AdminPlanPageData{Screen: sc, Plan: planForm(*p), UpdateURL: m.URLs().AdminPlan(p.ID), BackURL: m.URLs().AdminPlans()})
}

// updatePlan changes a plan.
func (m *Module) updatePlan(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	in, err := planRequest(ctx)
	if err != nil {
		return err
	}
	p, err := m.svc.UpdatePlan(ctx.Ctx(), who, ctx.Param("id"), in)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewPlanResource(*p))
	}
	return ctx.Redirect(m.URLs().AdminPlans())
}

// adminCoupons are the coupons, with the form that adds one.
func (m *Module) adminCoupons(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	q := m.pageQuery(ctx)
	page, err := m.svc.CouponsPage(ctx.Ctx(), who, q, m.cfg.PageSize)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		items := make([]map[string]any, 0, len(page.Rows))
		for _, c := range page.Rows {
			items = append(items, NewCouponResource(*c).ToArray())
		}
		return ctx.JSON(stdhttp.StatusOK, list{items: items, total: page.Total})
	}
	names, err := m.svc.PlanNames(ctx.Ctx(), who)
	if err != nil {
		return err
	}
	sc, err := m.screen(ctx, who, "screen.admin_coupons_title", true)
	if err != nil {
		return err
	}
	return ctx.View(ViewAdminCoupons, AdminCouponsPageData{
		Screen: sc, Rows: couponRows(sc, page.Rows, names), Table: tableState(q, page.Page, page.Pages, page.Total, m.URLs().AdminCoupons()),
		StoreURL: m.URLs().AdminCoupons(), PlanOptions: planOptions(sc, names),
	})
}

// storeCoupon adds a coupon.
func (m *Module) storeCoupon(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	in, err := m.couponRequest(ctx)
	if err != nil {
		return err
	}
	c, err := m.svc.CreateCoupon(ctx.Ctx(), who, in)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusCreated, NewCouponResource(*c))
	}
	return ctx.Redirect(m.URLs().AdminCoupons())
}

// updateCoupon switches a coupon on or off.
func (m *Module) updateCoupon(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	c, err := m.svc.SetCouponActive(ctx.Ctx(), who, ctx.Param("id"), checked(ctx.Input("active")))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewCouponResource(*c))
	}
	return ctx.Redirect(m.URLs().AdminCoupons())
}

// couponRequest reads a coupon's form. The value is typed as a percentage
// ("10" or "12,5") for a percent coupon and as reais for a fixed one: two
// decimals either way, which is basis points or centavos.
func (m *Module) couponRequest(ctx *fhttp.Context) (CouponRequest, error) {
	value, ok := parseReais(ctx.Input("value"))
	if !ok {
		return CouponRequest{}, validation.Errors{"value": {message("amount")}}
	}
	minimum := int64(0)
	if raw := ctx.Input("min_purchase_brl"); strings.TrimSpace(raw) != "" {
		if minimum, ok = parseReais(raw); !ok {
			return CouponRequest{}, validation.Errors{"min_purchase_brl": {message("amount")}}
		}
	}
	from, err := m.dayStart(ctx.Input("valid_from"), 0)
	if err != nil {
		return CouponRequest{}, validation.Errors{"valid_from": {message("date")}}
	}
	until, err := m.dayStart(ctx.Input("valid_until"), 1)
	if err != nil {
		return CouponRequest{}, validation.Errors{"valid_until": {message("date")}}
	}
	return CouponRequest{
		Code: ctx.Input("code"), Description: ctx.Input("description"), Kind: CouponKind(ctx.Input("kind")),
		Value: value, MaxUses: atoi(ctx.Input("max_uses")), Renewals: atoi(ctx.Input("renewals")),
		MinPurchase: minimum, PlanID: ctx.Input("plan_id"), ValidFrom: from, ValidUntil: until,
		Active: checked(ctx.Input("active")),
	}, nil
}

// dayStart is the first instant of a day typed as YYYY-MM-DD, in the
// application's timezone, moved by days days; nil for an empty field.
func (m *Module) dayStart(raw string, days int) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	day, err := time.ParseInLocation("2006-01-02", raw, m.cfg.Location)
	if err != nil {
		return nil, err
	}
	t := day.AddDate(0, 0, days).UTC()
	return &t, nil
}

// adminSubscriptions are every account's subscriptions.
func (m *Module) adminSubscriptions(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	q := m.pageQuery(ctx)
	page, err := m.svc.AllSubscriptionsPage(ctx.Ctx(), who, q, m.cfg.PageSize)
	if err != nil {
		return err
	}
	rows := make([]*Subscription, 0, len(page.Rows))
	for i := range page.Rows {
		rows = append(rows, &page.Rows[i])
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, subscriptionsList(Page[*Subscription]{Rows: rows, Total: page.Total}))
	}
	names, err := m.svc.PlanNames(ctx.Ctx(), who)
	if err != nil {
		return err
	}
	sc, err := m.screen(ctx, who, "screen.admin_subscriptions_title", true)
	if err != nil {
		return err
	}
	return ctx.View(ViewAdminSubscriptions, AdminSubscriptionsPageData{
		Screen: sc, Rows: subscriptionRows(sc, rows, names), Table: tableState(q, page.Page, page.Pages, page.Total, m.URLs().AdminSubscriptions()),
	})
}

// adminCancel cancels one account's subscription at once.
func (m *Module) adminCancel(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	sub, err := m.svc.CancelNow(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewSubscriptionResource(*sub))
	}
	return ctx.Redirect(m.URLs().AdminSubscriptions())
}

// adminInvoices are every account's invoices.
func (m *Module) adminInvoices(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	q := m.pageQuery(ctx)
	page, err := m.svc.AllInvoicesPage(ctx.Ctx(), who, q, m.cfg.PageSize)
	if err != nil {
		return err
	}
	rows := make([]*Invoice, 0, len(page.Rows))
	for i := range page.Rows {
		rows = append(rows, &page.Rows[i])
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, invoicesList(rows, page.Total))
	}
	sc, err := m.screen(ctx, who, "screen.admin_invoices_title", true)
	if err != nil {
		return err
	}
	return ctx.View(ViewAdminInvoices, AdminInvoicesPageData{
		Screen: sc, Rows: invoiceRows(sc, rows, true), Table: tableState(q, page.Page, page.Pages, page.Total, m.URLs().AdminInvoices()),
	})
}

// adminConfirm records one invoice paid by hand.
func (m *Module) adminConfirm(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	paidAt, err := m.dayStart(ctx.Input("paid_on"), 0)
	if err != nil {
		return validation.Errors{"paid_on": {message("date")}}
	}
	in := ManualPayment{Method: ctx.Input("method"), Reference: ctx.Input("reference")}
	if paidAt != nil {
		in.PaidAt = paidAt.Add(12 * time.Hour)
	}
	inv, err := m.svc.ConfirmManually(ctx.Ctx(), who, ctx.Param("id"), in)
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewInvoiceResource(*inv))
	}
	return ctx.Redirect(m.URLs().AdminInvoices())
}

// adminVoid cancels one open invoice.
func (m *Module) adminVoid(ctx *fhttp.Context) error {
	who, _ := ctx.User()
	inv, err := m.svc.VoidInvoice(ctx.Ctx(), who, ctx.Param("id"))
	if err != nil {
		return err
	}
	if ctx.WantsJSON() {
		return ctx.JSON(stdhttp.StatusOK, NewInvoiceResource(*inv))
	}
	return ctx.Redirect(m.URLs().AdminInvoices())
}

// fieldsResource is a set of fields as a JSON answer.
type fieldsResource map[string]any

// ToArray is the fields.
func (f fieldsResource) ToArray() map[string]any { return f }

// With is nothing beside them.
func (fieldsResource) With() map[string]any { return nil }

func subscriptionsList(page Page[*Subscription]) list {
	items := make([]map[string]any, 0, len(page.Rows))
	for _, s := range page.Rows {
		items = append(items, NewSubscriptionResource(*s).ToArray())
	}
	return list{items: items, total: page.Total}
}

func invoicesList(rows []*Invoice, total int) list {
	items := make([]map[string]any, 0, len(rows))
	for _, i := range rows {
		items = append(items, NewInvoiceResource(*i).ToArray())
	}
	return list{items: items, total: total}
}

func atoi(raw string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(raw))
	return n
}

func checked(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "on", "yes":
		return true
	}
	return false
}

// parseMeta reads "name=value" lines.
func parseMeta(raw string) (Meta, error) {
	out := Meta{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" || len(name) > maxShortLen || len(value) > maxNameLen {
			return nil, fmt.Errorf("billing: %q is not a name=value line", line)
		}
		out[name] = strings.TrimSpace(value)
	}
	return out, nil
}

// Migrations declares the schema this module owns, in the order the names
// sort in, which is the order they apply in.
func (m *Module) Migrations() []foundation.Migration {
	return []foundation.Migration{
		createPlans{}, createCoupons{}, createSubscriptions{},
		createInvoices{}, createPayments{}, createCouponUsages{},
	}
}

var (
	_ migrations.ReversibleMigration = createPlans{}
	_ migrations.ReversibleMigration = createCoupons{}
	_ migrations.ReversibleMigration = createSubscriptions{}
	_ migrations.ReversibleMigration = createInvoices{}
	_ migrations.ReversibleMigration = createPayments{}
	_ migrations.ReversibleMigration = createCouponUsages{}
)

// createPlans is the catalogue's table.
type createPlans struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order.
func (createPlans) GetName() string { return "20261023_0001_create_billing_plans" }

// Up creates the table: one plan per slug.
func (createPlans) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, plansTable, func(table *schema.Blueprint) {
		table.String("id", maxIdentifierLen).Primary()
		table.String("slug", maxSlugLen)
		table.String("name", maxNameLen)
		table.Text("description")
		table.Text("features")
		table.BigInteger("price")
		table.UnsignedSmallInteger("interval_months")
		table.UnsignedSmallInteger("trial_days").Default(0)
		table.UnsignedSmallInteger("grace_days").Default(0)
		table.UnsignedSmallInteger("installments_from").Default(0)
		table.UnsignedSmallInteger("installments_cap").Default(0)
		table.Text("meta")
		table.UnsignedSmallInteger("active").Default(1)
		table.Integer("position").Default(0)
		table.Timestamp("created_at", 6)
		table.Timestamp("updated_at", 6)
		table.Unique([]string{"slug"}, "billing_plans_slug_uq")
		table.Index([]string{"active", "position", "id"}, "billing_plans_active_position_idx")
	})
}

// Down drops the table.
func (createPlans) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().DropIfExists(ctx, plansTable)
}

// createCoupons is the coupons' table.
type createCoupons struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order.
func (createCoupons) GetName() string { return "20261023_0002_create_billing_coupons" }

// Up creates the table: one coupon per code.
func (createCoupons) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, couponsTable, func(table *schema.Blueprint) {
		table.String("id", maxIdentifierLen).Primary()
		table.String("code", maxCodeLen)
		table.Text("description")
		table.String("kind", maxShortLen)
		table.BigInteger("value")
		table.Integer("max_uses").Default(0)
		table.Integer("uses").Default(0)
		table.Timestamp("valid_from", 6).Nullable()
		table.Timestamp("valid_until", 6).Nullable()
		table.UnsignedSmallInteger("renewals").Default(0)
		table.BigInteger("min_purchase").Default(0)
		table.String("plan_id", maxIdentifierLen).Default("")
		table.UnsignedSmallInteger("active").Default(1)
		table.Timestamp("created_at", 6)
		table.Timestamp("updated_at", 6)
		table.Unique([]string{"code"}, "billing_coupons_code_uq")
		table.Index([]string{"created_at", "id"}, "billing_coupons_created_idx")
	})
}

// Down drops the table.
func (createCoupons) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().DropIfExists(ctx, couponsTable)
}

// createSubscriptions is the accounts' subscriptions.
type createSubscriptions struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order.
func (createSubscriptions) GetName() string { return "20261023_0003_create_billing_subscriptions" }

// Up creates the table and the indexes the account's list, the checkout's
// lookup and the clock's sweep read by.
func (createSubscriptions) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, subscriptionsTable, func(table *schema.Blueprint) {
		table.String("id", maxIdentifierLen).Primary()
		table.String("tenant_id", maxIdentifierLen)
		table.String("reference", maxIdentifierLen).Default("")
		table.String("plan_id", maxIdentifierLen)
		table.String("status", maxShortLen)
		table.Timestamp("current_period_start", 6)
		table.Timestamp("current_period_end", 6)
		table.Timestamp("trial_ends_at", 6).Nullable()
		table.UnsignedSmallInteger("cancel_at_period_end").Default(0)
		table.Timestamp("canceled_at", 6).Nullable()
		table.Timestamp("ended_at", 6).Nullable()
		table.UnsignedSmallInteger("grace_days").Default(0)
		table.String("coupon_id", maxIdentifierLen).Default("")
		table.UnsignedSmallInteger("coupon_renewals_left").Default(0)
		table.Integer("renewals").Default(0)
		table.Text("meta")
		table.String("created_by", maxIdentifierLen).Default("")
		table.Timestamp("created_at", 6)
		table.Timestamp("updated_at", 6)
		table.Index([]string{"tenant_id", "plan_id", "reference", "status"}, "billing_subscriptions_lookup_idx")
		table.Index([]string{"tenant_id", "current_period_end", "id"}, "billing_subscriptions_tenant_end_idx")
		table.Index([]string{"status", "current_period_end", "id"}, "billing_subscriptions_sweep_idx")
		table.Index([]string{"tenant_id", "created_at", "id"}, "billing_subscriptions_tenant_created_idx")
	})
}

// Down drops the table.
func (createSubscriptions) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().DropIfExists(ctx, subscriptionsTable)
}

// createInvoices is the accounts' invoices.
type createInvoices struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order.
func (createInvoices) GetName() string { return "20261023_0004_create_billing_invoices" }

// Up creates the table and the indexes the lists and the notification's
// lookup read by.
func (createInvoices) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, invoicesTable, func(table *schema.Blueprint) {
		table.String("id", maxIdentifierLen).Primary()
		table.String("tenant_id", maxIdentifierLen)
		table.String("subscription_id", maxIdentifierLen).Default("")
		table.String("plan_id", maxIdentifierLen)
		table.String("plan_name", maxNameLen)
		table.String("reference", maxIdentifierLen).Default("")
		table.String("kind", maxShortLen)
		table.String("status", maxShortLen)
		table.UnsignedSmallInteger("months")
		table.BigInteger("subtotal")
		table.BigInteger("discount").Default(0)
		table.BigInteger("total")
		table.String("coupon_id", maxIdentifierLen).Default("")
		table.String("coupon_code", maxCodeLen).Default("")
		table.Integer("renewal_number").Default(1)
		table.UnsignedSmallInteger("max_installments").Default(1)
		table.String("provider", maxShortLen)
		table.String("provider_ref", maxIdentifierLen).Default("")
		table.String("provider_status", maxShortLen).Default("")
		table.String("checkout_url", maxURLLen).Default("")
		table.Text("meta")
		table.Timestamp("paid_at", 6).Nullable()
		table.String("created_by", maxIdentifierLen).Default("")
		table.Timestamp("created_at", 6)
		table.Timestamp("updated_at", 6)
		table.Index([]string{"tenant_id", "created_at", "id"}, "billing_invoices_tenant_created_idx")
		table.Index([]string{"tenant_id", "status", "plan_id"}, "billing_invoices_tenant_status_idx")
		table.Index([]string{"provider", "provider_ref"}, "billing_invoices_provider_ref_idx")
		table.Index([]string{"status", "created_at", "id"}, "billing_invoices_status_created_idx")
	})
}

// Down drops the table.
func (createInvoices) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().DropIfExists(ctx, invoicesTable)
}

// createPayments is the confirmations, one per invoice.
type createPayments struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order.
func (createPayments) GetName() string { return "20261023_0005_create_billing_payments" }

// Up creates the table. The unique invoice_id is the database's half of
// confirming an invoice once.
func (createPayments) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, paymentsTable, func(table *schema.Blueprint) {
		table.String("id", maxIdentifierLen).Primary()
		table.String("tenant_id", maxIdentifierLen)
		table.String("invoice_id", maxIdentifierLen)
		table.String("provider", maxShortLen)
		table.String("provider_ref", maxIdentifierLen).Default("")
		table.String("method", maxShortLen).Default("")
		table.UnsignedSmallInteger("installments").Default(1)
		table.BigInteger("amount")
		table.Timestamp("paid_at", 6)
		table.String("confirmed_by", maxIdentifierLen).Default("")
		table.Timestamp("created_at", 6)
		table.Timestamp("updated_at", 6)
		table.Unique([]string{"invoice_id"}, "billing_payments_invoice_uq")
		table.Index([]string{"tenant_id", "paid_at", "id"}, "billing_payments_tenant_paid_idx")
	})
}

// Down drops the table.
func (createPayments) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().DropIfExists(ctx, paymentsTable)
}

// createCouponUsages is the payments that used a coupon.
type createCouponUsages struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order.
func (createCouponUsages) GetName() string { return "20261023_0006_create_billing_coupon_usages" }

// Up creates the table: one use per coupon and invoice.
func (createCouponUsages) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, couponUsagesTable, func(table *schema.Blueprint) {
		table.String("id", maxIdentifierLen).Primary()
		table.String("tenant_id", maxIdentifierLen)
		table.String("coupon_id", maxIdentifierLen)
		table.String("invoice_id", maxIdentifierLen)
		table.String("payment_id", maxIdentifierLen)
		table.BigInteger("discount")
		table.Integer("renewal_number").Default(1)
		table.Timestamp("used_at", 6)
		table.Timestamp("created_at", 6)
		table.Timestamp("updated_at", 6)
		table.Unique([]string{"coupon_id", "invoice_id"}, "billing_coupon_usages_uq")
		table.Index([]string{"tenant_id", "used_at", "id"}, "billing_coupon_usages_tenant_idx")
	})
}

// Down drops the table.
func (createCouponUsages) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().DropIfExists(ctx, couponUsagesTable)
}
