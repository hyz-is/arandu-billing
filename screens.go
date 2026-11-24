package billing

import (
	"html/template"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	fview "github.com/arandu-io/framework/view"
	"github.com/arandu-io/hesape/pagination"
	hview "github.com/arandu-io/hesape/view"
	"github.com/arandu-io/kyse/components"
	br "github.com/hyz-is/arandu-br"
)

// What the screens show, built from what the services answer. Nothing here
// reads the database or asks the policy: the handler already did both, and
// these only turn centavos, instants and statuses into the text a page draws.

// FormState is what a form field reads from the page it is drawn on.
type FormState struct{ hview.Page }

// FieldError is the first message for one field, empty when it was accepted.
func (f FormState) FieldError(name string) string { return f.Page.FieldError(name) }

// HasErrors says whether the attempt that landed here was rejected.
func (f FormState) HasErrors() bool { return f.Page.Any() }

// ErrorSummary is one line per rejected field.
func (f FormState) ErrorSummary() []string { return f.Page.All() }

// FieldErrors is every message for one field.
func (f FormState) FieldErrors(name string) []string { return f.Page.Get(name) }

// MarshalJSON writes the form state with the CSRF token redacted.
func (f FormState) MarshalJSON() ([]byte, error) { return framePage(f.Page).MarshalJSON() }

// LogValue is the form state as a log line carries it, the token redacted.
func (f FormState) LogValue() slog.Value { return framePage(f.Page).LogValue() }

// Screen is what every page of this package carries: the page hesape's
// view.New builds, the application's chrome, the sentences, and where the
// package is mounted. It embeds hesape's view.Page, so it is the
// hesape/view.Layout an application's layout asserts.
type Screen struct {
	hview.Page
	// Labels are the sentences, in the request's language.
	Labels Labels
	// URLs are the addresses of the package's routes.
	URLs URLs

	loc    *time.Location
	chrome any
}

// Compile-time proof that a page of this package fits both layout contracts.
var (
	_ hview.Layout   = Screen{}
	_ fview.Layout   = Screen{}
	_ slog.LogValuer = Screen{}
)

// Chrome is what Config.Chrome answered for this screen; nil without one.
func (s Screen) Chrome() any { return s.chrome }

// HasErrors says whether the attempt that landed here was rejected.
func (s Screen) HasErrors() bool { return s.Page.Any() }

// All is one line per rejected message, the field named by its label.
func (s Screen) All() []string { return s.Labels.Summary(s.Page.Errors) }

// ErrorSummary is All, under framework/view.Layout's name.
func (s Screen) ErrorSummary() []string { return s.All() }

// FieldErrors is every message for one field.
func (s Screen) FieldErrors(name string) []string { return s.Page.Get(name) }

// MarshalJSON writes the page with the CSRF token redacted.
func (s Screen) MarshalJSON() ([]byte, error) { return framePage(s.Page).MarshalJSON() }

// LogValue is the page as a log line carries it, the token redacted.
func (s Screen) LogValue() slog.Value { return framePage(s.Page).LogValue() }

// Form is the form state of the page.
func (s Screen) Form() FormState { return FormState{Page: s.Page} }

// framePage copies a hesape page into framework/view.Page, for the two methods
// that redact the token there.
func framePage(p hview.Page) fview.Page {
	return fview.Page{
		Title: p.Title, Description: p.Description, Canonical: p.Canonical, AppName: p.AppName, Token: p.Token,
		Authenticated: p.Authenticated, UserName: p.UserName,
		HomeURL: p.HomeURL, LoginURL: p.LoginURL, LogoutURL: p.LogoutURL, RegisterURL: p.RegisterURL,
		PanelURL: p.PanelURL, AdminURL: p.AdminURL,
		Impersonating: p.Impersonating, ImpersonationActs: p.ImpersonationActs, Impersonator: p.Impersonator,
		LeaveImpersonationURL: p.LeaveImpersonationURL,
		Path:                  p.Path, Errors: p.Errors, Old: p.Old, Messages: p.Messages,
	}
}

// TableState is what a data table on these screens was asked and answers: the
// search, the status chosen, the order, the page with the size and the total of
// the list, and the columns the reader switched off.
type TableState struct {
	Query, Status, Sort, Dir string
	Page, Pages, Total       int
	// Rows is how many rows the page holds and PerPage how many it may hold.
	Rows, PerPage int
	// Hidden are the keys of the columns the reader switched off.
	Hidden []string
	URL    string
}

func tableState[T any](q PageQuery, page Page[T], hidden []string, base string) TableState {
	return TableState{
		Query: q.Search, Status: q.Status, Sort: q.Sort, Dir: q.Dir,
		Page: page.Page, Pages: page.Pages, Total: page.Total, Rows: len(page.Rows), PerPage: page.PerPage,
		Hidden: hidden, URL: base,
	}
}

// State is the table's state as the data table draws its addresses from: every
// control builds its address from it, so a step keeps the search, the status,
// the order and the columns switched off.
func (t TableState) State() components.DataTableState {
	facets := map[string][]string{}
	if t.Status != "" {
		facets["status"] = []string{t.Status}
	}
	return components.DataTableValues{
		Q: t.Query, Sort: t.Sort, Dir: t.Dir, Facets: facets,
		CurrentPage: t.Page, Size: t.PerPage, DefaultSize: t.PerPage, Hide: t.Hidden,
	}
}

// Pager is the page of the list the data table reads its position and its total
// from.
func (t TableState) Pager() components.Paginator {
	return pagination.NewLengthAwarePage(t.Rows, t.Total, max(t.PerPage, 1), max(t.Page, 1), pagination.Options{Path: t.URL})
}

// maxHiddenColumns bounds how many columns a request may switch off, so the
// address a table writes back stays short.
const maxHiddenColumns = 16

// hiddenColumns are the keys of the columns a query switched off. The table
// only hides a column that declared itself hideable, so a key it does not know
// changes nothing; this keeps the list short and each key a short word.
func hiddenColumns(query url.Values) []string {
	var keys []string
	for _, key := range query["hide"] {
		key = strings.TrimSpace(key)
		if key == "" || len(key) > 32 || len(keys) == maxHiddenColumns {
			continue
		}
		keys = append(keys, key)
	}
	return keys
}

// TableLines are the sentences a data table on these screens says.
func (s Screen) TableLines() components.DataTableLines {
	l := s.Labels
	return components.DataTableLines{
		Filter: l.T("table.filter"), NoResult: l.T("table.no_result"),
		Rows: l.T("table.rows"), RowsOne: l.T("table.rows_one"), RowsMany: l.T("table.rows_many"),
		SelectedCount: l.T("table.selected_count"), RowsPerPage: l.T("table.rows_per_page"), PageOf: l.T("table.page_of"),
		First: l.T("table.first"), Previous: l.T("table.previous"), Next: l.T("table.next"), Last: l.T("table.last"),
		Back: l.T("table.back"), Forward: l.T("table.forward"),
		SelectAll: l.T("table.select_all"), SelectRow: l.T("table.select_row"), Bulk: l.T("table.bulk"), Apply: l.T("table.apply"),
		Ascending: l.T("table.ascending"), Descending: l.T("table.descending"), Unsorted: l.T("table.unsorted"),
		Reset: l.T("table.reset"), ClearFilters: l.T("table.clear_filters"),
		NoFacetResults: l.T("table.no_facet_results"), FacetSelected: l.T("table.facet_selected"),
		OpenMenu: l.T("table.open_menu"), Actions: l.T("table.actions"), ToggleColumns: l.T("table.toggle_columns"),
		Asc: l.T("table.asc"), Desc: l.T("table.desc"), Hide: l.T("table.hide"),
	}
}

// SearchLabel is the name a screen reader gives a table's search box.
func (s Screen) SearchLabel(table string) string {
	return s.Labels.line("control.search_label", map[string]any{"label": table})
}

// StatusFilter is the status filter of a data table. The option chosen is the
// table state's, not the filter's.
func (s Screen) StatusFilter(options []components.SelectOption) []components.DataTableFilter {
	return []components.DataTableFilter{{Key: "status", Label: s.Labels.T("field.status"), Options: options}}
}

// SubscriptionStatusOptions are where a subscription can stand.
func (s Screen) SubscriptionStatusOptions() []components.SelectOption {
	out := make([]components.SelectOption, 0, len(SubscriptionStatuses))
	for _, st := range SubscriptionStatuses {
		out = append(out, components.SelectOption{Value: string(st), Label: s.Labels.SubscriptionStatus(st)})
	}
	return out
}

// InvoiceStatusOptions are where an invoice can stand.
func (s Screen) InvoiceStatusOptions() []components.SelectOption {
	out := make([]components.SelectOption, 0, len(InvoiceStatuses))
	for _, st := range InvoiceStatuses {
		out = append(out, components.SelectOption{Value: string(st), Label: s.Labels.InvoiceStatus(st)})
	}
	return out
}

// ActiveOptions are the two states of a plan or a coupon.
func (s Screen) ActiveOptions() []components.SelectOption {
	return []components.SelectOption{
		{Value: "active", Label: s.Labels.T("screen.active")},
		{Value: "inactive", Label: s.Labels.T("screen.inactive")},
	}
}

// CouponKindOptions are the two kinds of coupon.
func (s Screen) CouponKindOptions() []components.SelectOption {
	return []components.SelectOption{
		{Value: string(CouponPercent), Label: s.Labels.T("coupon_kind.percent")},
		{Value: string(CouponFixed), Label: s.Labels.T("coupon_kind.fixed")},
	}
}

// AdminNav is the bar on top of every admin screen.
func (s Screen) AdminNav(current string) template.HTML {
	return s.nav(current, s.Labels.T("screen.admin_nav"), []struct{ key, path string }{
		{"plans", s.URLs.AdminPlans()}, {"coupons", s.URLs.AdminCoupons()},
		{"subscriptions", s.URLs.AdminSubscriptions()}, {"invoices", s.URLs.AdminInvoices()},
	})
}

// AccountNav is the bar on top of every screen of an account.
func (s Screen) AccountNav(current string) template.HTML {
	return s.nav(current, s.Labels.T("screen.account_nav"), []struct{ key, path string }{
		{"plans", s.URLs.Plans()}, {"subscriptions", s.URLs.Subscriptions()}, {"invoices", s.URLs.Invoices()},
	})
}

func (s Screen) nav(current, label string, items []struct{ key, path string }) template.HTML {
	var b strings.Builder
	b.WriteString(`<nav class="flex flex-wrap items-center gap-1 border-b pb-3" aria-label="` + template.HTMLEscapeString(label) + `">`)
	for _, item := range items {
		b.WriteString(string(components.Link(components.LinkProps{
			Label: s.Labels.T("noun." + item.key), URL: item.path,
			Variant: "hover", Current: item.key == current,
			ComponentProps: components.ComponentProps{Class: "rounded-md px-3 py-1.5 text-sm"},
		})))
	}
	b.WriteString(`</nav>`)
	return template.HTML(b.String())
}

// CellInput is one field of a form drawn inside a table cell.
type CellInput struct {
	Name, Type, Placeholder, Value, Label string
	Required                              bool
}

// CellForm is a small form posted from a table cell: the CSRF token of the
// page, the fields given, and one button. Every value is escaped. A method
// other than POST is sent as the _method field the router reads.
func (s Screen) CellForm(action, method, label, variant string, inputs ...CellInput) template.HTML {
	esc := template.HTMLEscapeString
	var b strings.Builder
	b.WriteString(`<form class="flex flex-wrap items-center gap-1" method="post" action="` + esc(action) + `">`)
	b.WriteString(`<input type="hidden" name="_token" value="` + esc(s.CSRFToken()) + `">`)
	if method != "" && !strings.EqualFold(method, "post") {
		b.WriteString(`<input type="hidden" name="_method" value="` + esc(strings.ToUpper(method)) + `">`)
	}
	for _, in := range inputs {
		kind := in.Type
		if kind == "" {
			kind = "text"
		}
		if kind == "hidden" {
			b.WriteString(`<input type="hidden" name="` + esc(in.Name) + `" value="` + esc(in.Value) + `">`)
			continue
		}
		b.WriteString(`<input class="input h-8 w-36 text-xs" type="` + esc(kind) + `" name="` + esc(in.Name) + `" value="` + esc(in.Value) + `"`)
		if in.Placeholder != "" {
			b.WriteString(` placeholder="` + esc(in.Placeholder) + `"`)
		}
		if in.Label != "" {
			b.WriteString(` aria-label="` + esc(in.Label) + `"`)
		}
		if in.Required {
			b.WriteString(` required`)
		}
		b.WriteString(`>`)
	}
	if variant == "" {
		variant = "outline"
	}
	b.WriteString(`<button class="btn" data-variant="` + esc(variant) + `" data-size="sm" type="submit">` + esc(label) + `</button></form>`)
	return template.HTML(b.String())
}

// brl is centavos as reais: 123456 is "R$ 1.234,56".
func brl(c int64) string { return br.BRL(c) }

// day is an instant as the day Brazilians write, in the screen's timezone.
func (s Screen) day(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	if s.loc != nil {
		t = t.In(s.loc)
	}
	return br.Date(t)
}

func (s Screen) dayPtr(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return s.day(*t)
}

// percentText is basis points as a percentage: 1050 is "10,5%".
func percentText(bps int64) string {
	text := strconv.FormatFloat(float64(bps)/100, 'f', 2, 64)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	return strings.Replace(text, ".", ",", 1) + "%"
}

// reaisText is centavos as a form types them back: 19990 is "199,90".
func reaisText(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return sign + strconv.FormatInt(c/100, 10) + "," + leftPad(strconv.FormatInt(c%100, 10))
}

func leftPad(s string) string {
	if len(s) < 2 {
		return "0" + s
	}
	return s
}

// parseReais reads an amount typed as reais -- "1.234,56", "1234,56",
// "1234.56", "R$ 10" -- as centavos. The same reading turns a percentage
// typed as "12,5" into basis points.
func parseReais(s string) (int64, bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "R$"))
	if s == "" {
		return 0, false
	}
	negative := strings.HasPrefix(s, "-")
	s = strings.TrimSpace(strings.TrimPrefix(s, "-"))
	if strings.Contains(s, ",") {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.Replace(s, ",", ".", 1)
	}
	whole, frac, _ := strings.Cut(s, ".")
	if len(frac) > 2 || whole == "" {
		return 0, false
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w > 1<<40 {
		return 0, false
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, false
	}
	v := w*100 + f
	if negative {
		v = -v
	}
	return v, true
}

// PlanCard is one plan as the catalogue shows it.
type PlanCard struct {
	ID, Name, Description, Price, Period, Installments string
	Features                                           []string
	TrialDays                                          int
	CheckoutURL, TrialURL                              string
	// CycleOptions are the billing cycles the account may choose, each with
	// its price: empty when the plan sells only its own interval.
	CycleOptions []components.SelectOption
	// DefaultCycle is the option the plan starts on: its own interval.
	DefaultCycle string
}

// PlansPageData is the catalogue, as an account reads it.
type PlansPageData struct {
	Screen
	Plans []PlanCard
	// CanSubscribe says the subject may pay for the account; a member who
	// may not reads the plans without the buttons.
	CanSubscribe bool
}

func planCards(sc Screen, plans []*Plan) []PlanCard {
	out := make([]PlanCard, 0, len(plans))
	for _, p := range plans {
		card := PlanCard{
			ID: p.ID, Name: p.Name, Description: p.Description, Price: brl(p.Price),
			Period: sc.Labels.Months(p.IntervalMonths), Features: append([]string(nil), p.Features...),
			TrialDays: p.TrialDays, CheckoutURL: sc.URLs.Checkout(p.ID), TrialURL: sc.URLs.Trial(p.ID),
		}
		if n := p.Installments(); n > 1 {
			card.Installments = sc.Labels.line("screen.installments", map[string]any{"count": strconv.Itoa(n), "amount": brl(p.Price / int64(n))})
		}
		if cycles := p.OfferedCycles(); len(cycles) > 1 {
			card.DefaultCycle = strconv.Itoa(p.IntervalMonths)
			for _, c := range cycles {
				list, off, err := CyclePrice(*p, c.Months)
				if err != nil {
					continue
				}
				key := "screen.cycle_option"
				replace := map[string]any{"period": sc.Labels.Months(c.Months), "price": brl(list - off)}
				if c.Discount > 0 {
					key = "screen.cycle_option_discount"
					replace["percent"] = percentText(c.Discount)
				}
				card.CycleOptions = append(card.CycleOptions, components.SelectOption{Value: strconv.Itoa(c.Months), Label: sc.Labels.line(key, replace)})
			}
		}
		out = append(out, card)
	}
	return out
}

// SubscriptionRow is one subscription as a list shows it.
type SubscriptionRow struct {
	ID, Tenant, Plan, Reference, Status, StatusKey, PaidUntil, Started string
	Serving, Cancelling, Ended                                         bool
	RenewURL, CancelURL, AdminCancelURL                                string
}

// SubscriptionsPageData is a page of subscriptions: the account's, or every
// account's for the admin.
type SubscriptionsPageData struct {
	Screen
	Rows  []SubscriptionRow
	Table TableState
	// CanManage says the subject may renew and cancel.
	CanManage bool
}

// AdminSubscriptionsPageData is every account's subscriptions.
type AdminSubscriptionsPageData struct {
	Screen
	Rows  []SubscriptionRow
	Table TableState
}

func subscriptionRows(sc Screen, rows []*Subscription, plans map[string]string) []SubscriptionRow {
	out := make([]SubscriptionRow, 0, len(rows))
	for _, r := range rows {
		name := plans[r.PlanID]
		if name == "" {
			name = r.PlanID
		}
		out = append(out, SubscriptionRow{
			ID: r.ID, Tenant: r.TenantID, Plan: name, Reference: r.Reference,
			Status: sc.Labels.SubscriptionStatus(r.Status), StatusKey: string(r.Status),
			PaidUntil: sc.day(r.CurrentPeriodEnd), Started: sc.day(r.CreatedAt),
			Serving: r.Serving(), Cancelling: bool(r.CancelAtPeriodEnd), Ended: r.Ended(),
			RenewURL: sc.URLs.Renew(r.ID), CancelURL: sc.URLs.Cancel(r.ID), AdminCancelURL: sc.URLs.AdminCancel(r.ID),
		})
	}
	return out
}

// InvoiceRow is one invoice as a list shows it.
type InvoiceRow struct {
	ID, Tenant, Plan, Period, Subtotal, Discount, Total, Coupon string
	Status, StatusKey, Created, Paid, Provider, ProviderStatus  string
	Installments                                                string
	Open                                                        bool
	URL, PayURL, ConfirmURL, VoidURL                            string
}

// InvoicesPageData is a page of the account's invoices.
type InvoicesPageData struct {
	Screen
	Rows  []InvoiceRow
	Table TableState
}

// AdminInvoicesPageData is every account's invoices.
type AdminInvoicesPageData struct {
	Screen
	Rows  []InvoiceRow
	Table TableState
}

// InvoicePageData is one invoice, with how to pay it.
type InvoicePageData struct {
	Screen
	Invoice InvoiceRow
	// Instructions are the provider's words on how to pay, empty for a
	// provider with a page of its own.
	Instructions string
}

func invoiceRows(sc Screen, rows []*Invoice, admin bool) []InvoiceRow {
	out := make([]InvoiceRow, 0, len(rows))
	for _, r := range rows {
		row := InvoiceRow{
			ID: r.ID, Tenant: r.TenantID, Plan: r.PlanName, Period: sc.Labels.Months(r.Months),
			Subtotal: brl(r.Subtotal), Discount: brl(r.Discount), Total: brl(r.Total), Coupon: r.CouponCode,
			Status: sc.Labels.InvoiceStatus(r.Status), StatusKey: string(r.Status),
			Created: sc.day(r.CreatedAt), Paid: sc.dayPtr(r.PaidAt), Provider: r.Provider, ProviderStatus: r.ProviderStatus,
			Open: r.Status == InvoiceOpen || r.Status == InvoiceFailed,
			URL:  sc.URLs.Invoice(r.ID), ConfirmURL: sc.URLs.AdminConfirm(r.ID), VoidURL: sc.URLs.AdminVoid(r.ID),
		}
		if r.MaxInstallments > 1 {
			row.Installments = sc.Labels.line("screen.up_to_installments", map[string]any{"count": strconv.Itoa(r.MaxInstallments)})
		}
		if r.Status == InvoiceOpen && r.CheckoutURL != "" && !admin {
			row.PayURL = r.CheckoutURL
		}
		out = append(out, row)
	}
	return out
}

// PlanRow is one plan in the admin's catalogue.
type PlanRow struct {
	ID, Name, Slug, Price, Period, Installments, Trial, Status string
	Active                                                     bool
	URL                                                        string
}

// AdminPlansPageData is the catalogue, for the admin.
type AdminPlansPageData struct {
	Screen
	Rows     []PlanRow
	Table    TableState
	StoreURL string
	// NewPlan is what the new plan's form starts with.
	NewPlan PlanForm
}

func planRows(sc Screen, rows []*Plan) []PlanRow {
	out := make([]PlanRow, 0, len(rows))
	for _, p := range rows {
		status := sc.Labels.T("screen.inactive")
		if p.Active {
			status = sc.Labels.T("screen.active")
		}
		trial := "—"
		if p.TrialDays > 0 {
			trial = sc.Labels.line("screen.days", map[string]any{"count": strconv.Itoa(p.TrialDays)})
		}
		installments := "1x"
		if n := p.Installments(); n > 1 {
			installments = strconv.Itoa(n) + "x"
		}
		out = append(out, PlanRow{
			ID: p.ID, Name: p.Name, Slug: p.Slug, Price: brl(p.Price), Period: sc.Labels.Months(p.IntervalMonths),
			Installments: installments, Trial: trial, Status: status, Active: bool(p.Active), URL: sc.URLs.AdminPlan(p.ID),
		})
	}
	return out
}

// PlanForm is a plan's form, as text the fields draw.
type PlanForm struct {
	Name, Slug, Description, Features, Price, IntervalMonths, TrialDays string
	GraceDays, InstallmentsFrom, InstallmentsCap, Position, Meta        string
	// Cycles are the plan's billing cycles as lines of months=percent.
	Cycles string
	// IncludedUnits and UnitPrice are the units the price covers and what each
	// extra one costs, as the form types them.
	IncludedUnits, UnitPrice string
	Active                   bool
}

func planForm(p Plan) PlanForm {
	names := make([]string, 0, len(p.Meta))
	for k := range p.Meta {
		names = append(names, k)
	}
	sort.Strings(names)
	meta := make([]string, 0, len(names))
	for _, k := range names {
		meta = append(meta, k+"="+p.Meta[k])
	}
	return PlanForm{
		Name: p.Name, Slug: p.Slug, Description: p.Description, Features: strings.Join(p.Features, "\n"),
		Price: reaisText(p.Price), IntervalMonths: strconv.Itoa(p.IntervalMonths), TrialDays: strconv.Itoa(p.TrialDays),
		GraceDays: strconv.Itoa(p.GraceDays), InstallmentsFrom: strconv.Itoa(p.InstallmentsFrom),
		InstallmentsCap: strconv.Itoa(p.InstallmentsCap), Position: strconv.Itoa(p.Position),
		Meta: strings.Join(meta, "\n"), Cycles: cycleLines(p.Cycles), Active: bool(p.Active),
		IncludedUnits: strconv.Itoa(p.IncludedUnits), UnitPrice: reaisText(p.UnitPrice),
	}
}

// cycleLines is a plan's cycles as the form types them: "3=5", "12=17,5".
func cycleLines(cycles []Cycle) string {
	lines := make([]string, 0, len(cycles))
	for _, c := range cycles {
		lines = append(lines, strconv.Itoa(c.Months)+"="+strings.TrimSuffix(percentText(c.Discount), "%"))
	}
	return strings.Join(lines, "\n")
}

// AdminPlanPageData is one plan, with the form that changes it.
type AdminPlanPageData struct {
	Screen
	Plan               PlanForm
	UpdateURL, BackURL string
}

// CouponRow is one coupon in the admin's list.
type CouponRow struct {
	ID, Code, Description, Discount, Uses, Validity, Renewals, Plan, Minimum, Status string
	Active                                                                           bool
	ToggleURL                                                                        string
}

// AdminCouponsPageData is the coupons, for the admin.
type AdminCouponsPageData struct {
	Screen
	Rows        []CouponRow
	Table       TableState
	StoreURL    string
	PlanOptions []components.SelectOption
}

func couponRows(sc Screen, rows []*Coupon, plans map[string]string) []CouponRow {
	out := make([]CouponRow, 0, len(rows))
	for _, c := range rows {
		discount := brl(c.Value)
		if c.Kind == CouponPercent {
			discount = percentText(c.Value)
		}
		uses := strconv.Itoa(c.Uses)
		if c.MaxUses > 0 {
			uses += " / " + strconv.Itoa(c.MaxUses)
		}
		plan := sc.Labels.T("screen.every_plan")
		if c.PlanID != "" {
			plan = plans[c.PlanID]
			if plan == "" {
				plan = c.PlanID
			}
		}
		status := sc.Labels.T("screen.inactive")
		if c.Active {
			status = sc.Labels.T("screen.active")
		}
		minimum := "—"
		if c.MinPurchase > 0 {
			minimum = brl(c.MinPurchase)
		}
		validity := sc.dayPtr(c.ValidFrom) + " — " + sc.dayPtr(c.ValidUntil)
		out = append(out, CouponRow{
			ID: c.ID, Code: c.Code, Description: c.Description, Discount: discount, Uses: uses,
			Validity: validity, Renewals: strconv.Itoa(c.Renewals), Plan: plan, Minimum: minimum,
			Status: status, Active: bool(c.Active), ToggleURL: sc.URLs.AdminCoupon(c.ID),
		})
	}
	return out
}

func planOptions(sc Screen, plans map[string]string) []components.SelectOption {
	ids := make([]string, 0, len(plans))
	for id := range plans {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return plans[ids[i]] < plans[ids[j]] })
	out := []components.SelectOption{{Value: "", Label: sc.Labels.T("screen.every_plan")}}
	for _, id := range ids {
		out = append(out, components.SelectOption{Value: id, Label: plans[id]})
	}
	return out
}
