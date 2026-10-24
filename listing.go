package billing

import (
	"context"
	"strings"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/hyz-is/arandu-billing/internal/everytenant"
)

// The lists the screens page through: searched, filtered, ordered and paged in
// the database, so a list that grows to millions of rows still answers one
// page. Every sort is a column of an allowlist, never a name taken from the
// request.

// PageQuery is what a data table asks for.
type PageQuery struct {
	// Search is the text typed in the search box.
	Search string
	// Status narrows the list to one status; empty is every one.
	Status string
	// Sort is a column key of the list's allowlist; empty is its default.
	Sort string
	// Dir is "asc" or "desc"; empty is the list's default.
	Dir string
	// Page is the page asked for, from 1.
	Page int
	// PerPage is how many rows a page holds; zero or above MaxPageSize is the
	// module's page size.
	PerPage int
}

// Page is one page of a list, with what a data table draws around it.
type Page[T any] struct {
	// Rows are the page's rows.
	Rows []T
	// Total is how many rows the whole list holds.
	Total int
	// Page is the page shown, from 1.
	Page int
	// PerPage is how many rows a page holds.
	PerPage int
	// Pages is how many pages the list has.
	Pages int
}

func (q PageQuery) normalized(fallback int) PageQuery {
	if q.PerPage <= 0 || q.PerPage > MaxPageSize {
		q.PerPage = fallback
	}
	if q.PerPage <= 0 {
		q.PerPage = DefaultPageSize
	}
	if q.Page < 1 {
		q.Page = 1
	}
	if q.Dir != "asc" && q.Dir != "desc" {
		q.Dir = ""
	}
	q.Search = strings.TrimSpace(q.Search)
	if len(q.Search) > maxNameLen {
		q.Search = q.Search[:maxNameLen]
	}
	return q
}

func pageOf[T any](rows []T, total int64, q PageQuery) Page[T] {
	pages := int((total + int64(q.PerPage) - 1) / int64(q.PerPage))
	if pages < 1 {
		pages = 1
	}
	return Page[T]{Rows: rows, Total: int(total), Page: q.Page, PerPage: q.PerPage, Pages: pages}
}

// sortColumn reads the column a list is ordered by, and refuses one that is
// not on the allowlist.
func sortColumn(allowed map[string]string, q PageQuery) (string, error) {
	column, ok := allowed[q.Sort]
	if !ok {
		return "", validation.Errors{"sort": {message("sort")}}
	}
	return column, nil
}

var (
	planSorts         = map[string]string{"": "position", "position": "position", "name": "name", "price": "price", "months": "interval_months", "created_at": "created_at"}
	couponSorts       = map[string]string{"": "created_at", "code": "code", "value": "value", "uses": "uses", "valid_until": "valid_until", "created_at": "created_at"}
	subscriptionSorts = map[string]string{"": "current_period_end", "status": "status", "paid_until": "current_period_end", "created_at": "created_at", "tenant": "tenant_id"}
	invoiceSorts      = map[string]string{"": "created_at", "created_at": "created_at", "total": "total", "status": "status", "paid_at": "paid_at", "tenant": "tenant_id"}
)

// PlansPage is a page of the catalogue, active or not, for the admin.
func (s *BillingService) PlansPage(ctx context.Context, actor security.Subject, q PageQuery, perPage int) (Page[*Plan], error) {
	q = q.normalized(perPage)
	g, err := security.Authorize(ctx, s.policy, actor, PlansManage, Account{})
	if err != nil {
		return Page[*Plan]{}, err
	}
	column, err := sortColumn(planSorts, q)
	if err != nil {
		return Page[*Plan]{}, err
	}
	filtered := func() *PlanQuery {
		b := Plans(s.db)
		switch q.Status {
		case "active":
			b = b.Where("active", "=", 1)
		case "inactive":
			b = b.Where("active", "=", 0)
		}
		if q.Search != "" {
			like := "%" + q.Search + "%"
			b = b.Where(func(w *PlanQuery) { w.Where("name", "like", like).OrWhere("slug", "like", like) })
		}
		return b
	}
	total, err := filtered().Count(ctx, g)
	if err != nil {
		return Page[*Plan]{}, err
	}
	b := filtered()
	if q.Dir == "desc" {
		b = b.OrderByDesc(column)
	} else {
		b = b.OrderBy(column)
	}
	rows, err := b.OrderBy("id").Offset((q.Page-1)*q.PerPage).Limit(q.PerPage).Get(ctx, g)
	if err != nil {
		return Page[*Plan]{}, err
	}
	return pageOf([]*Plan(rows), total, q), nil
}

// CouponsPage is a page of the coupons, for the admin.
func (s *BillingService) CouponsPage(ctx context.Context, actor security.Subject, q PageQuery, perPage int) (Page[*Coupon], error) {
	q = q.normalized(perPage)
	g, err := security.Authorize(ctx, s.policy, actor, CouponsManage, Account{})
	if err != nil {
		return Page[*Coupon]{}, err
	}
	column, err := sortColumn(couponSorts, q)
	if err != nil {
		return Page[*Coupon]{}, err
	}
	filtered := func() *CouponQuery {
		b := Coupons(s.db)
		switch q.Status {
		case "active":
			b = b.Where("active", "=", 1)
		case "inactive":
			b = b.Where("active", "=", 0)
		}
		if q.Search != "" {
			like := "%" + strings.ToUpper(q.Search) + "%"
			b = b.Where(func(w *CouponQuery) { w.Where("code", "like", like).OrWhere("description", "like", "%"+q.Search+"%") })
		}
		return b
	}
	total, err := filtered().Count(ctx, g)
	if err != nil {
		return Page[*Coupon]{}, err
	}
	b := filtered()
	if q.Dir == "asc" {
		b = b.OrderBy(column)
	} else {
		b = b.OrderByDesc(column)
	}
	rows, err := b.OrderByDesc("id").Offset((q.Page-1)*q.PerPage).Limit(q.PerPage).Get(ctx, g)
	if err != nil {
		return Page[*Coupon]{}, err
	}
	return pageOf([]*Coupon(rows), total, q), nil
}

// SubscriptionsPage is a page of the asking account's subscriptions.
func (s *BillingService) SubscriptionsPage(ctx context.Context, actor security.Subject, q PageQuery, perPage int) (Page[*Subscription], error) {
	q = q.normalized(perPage)
	g, err := security.Authorize(ctx, s.policy, actor, SubscriptionsView, Account{TenantID: actor.Tenant})
	if err != nil {
		return Page[*Subscription]{}, err
	}
	column, err := sortColumn(subscriptionSorts, q)
	if err != nil {
		return Page[*Subscription]{}, err
	}
	filtered := func() *SubscriptionQuery {
		b := Subscriptions(s.db)
		if q.Status != "" {
			b = b.Where("status", "=", q.Status)
		}
		if q.Search != "" {
			like := "%" + q.Search + "%"
			b = b.Where(func(w *SubscriptionQuery) { w.Where("reference", "like", like).OrWhere("id", "=", q.Search) })
		}
		return b
	}
	total, err := filtered().Count(ctx, g)
	if err != nil {
		return Page[*Subscription]{}, err
	}
	b := filtered()
	if q.Dir == "asc" {
		b = b.OrderBy(column)
	} else {
		b = b.OrderByDesc(column)
	}
	rows, err := b.OrderByDesc("id").Offset((q.Page-1)*q.PerPage).Limit(q.PerPage).Get(ctx, g)
	if err != nil {
		return Page[*Subscription]{}, err
	}
	return pageOf([]*Subscription(rows), total, q), nil
}

// InvoicesPage is a page of the asking account's invoices.
func (s *BillingService) InvoicesPage(ctx context.Context, actor security.Subject, q PageQuery, perPage int) (Page[*Invoice], error) {
	q = q.normalized(perPage)
	g, err := security.Authorize(ctx, s.policy, actor, InvoicesView, Account{TenantID: actor.Tenant})
	if err != nil {
		return Page[*Invoice]{}, err
	}
	column, err := sortColumn(invoiceSorts, q)
	if err != nil {
		return Page[*Invoice]{}, err
	}
	filtered := func() *InvoiceQuery {
		b := Invoices(s.db)
		if q.Status != "" {
			b = b.Where("status", "=", q.Status)
		}
		if q.Search != "" {
			like := "%" + q.Search + "%"
			b = b.Where(func(w *InvoiceQuery) {
				w.Where("plan_name", "like", like).OrWhere("coupon_code", "like", like).OrWhere("id", "=", q.Search)
			})
		}
		return b
	}
	total, err := filtered().Count(ctx, g)
	if err != nil {
		return Page[*Invoice]{}, err
	}
	b := filtered()
	if q.Dir == "asc" {
		b = b.OrderBy(column)
	} else {
		b = b.OrderByDesc(column)
	}
	rows, err := b.OrderByDesc("id").Offset((q.Page-1)*q.PerPage).Limit(q.PerPage).Get(ctx, g)
	if err != nil {
		return Page[*Invoice]{}, err
	}
	return pageOf([]*Invoice(rows), total, q), nil
}

// AllSubscriptionsPage is a page of every account's subscriptions, for the
// platform's admin. The rows are copies that hold no connection.
func (s *BillingService) AllSubscriptionsPage(ctx context.Context, actor security.Subject, q PageQuery, perPage int) (Page[Subscription], error) {
	q = q.normalized(perPage)
	g, err := security.Authorize(ctx, s.policy, actor, SubscriptionsManage, Account{})
	if err != nil {
		return Page[Subscription]{}, err
	}
	column, err := sortColumn(subscriptionSorts, q)
	if err != nil {
		return Page[Subscription]{}, err
	}
	filtered := func() *everytenant.SubscriptionQuery {
		b := everytenant.Subscriptions(s.db)
		if q.Status != "" {
			b = b.Where("status", "=", q.Status)
		}
		if q.Search != "" {
			like := "%" + q.Search + "%"
			b = b.Where(func(w *everytenant.SubscriptionQuery) {
				w.Where("tenant_id", "like", like).OrWhere("reference", "like", like).OrWhere("id", "=", q.Search)
			})
		}
		return b
	}
	total, err := filtered().Count(ctx, g)
	if err != nil {
		return Page[Subscription]{}, err
	}
	b := filtered()
	if q.Dir == "asc" {
		b = b.OrderBy(column)
	} else {
		b = b.OrderByDesc(column)
	}
	rows, err := b.OrderByDesc("id").Offset((q.Page-1)*q.PerPage).Limit(q.PerPage).Get(ctx, g)
	if err != nil {
		return Page[Subscription]{}, err
	}
	out := make([]Subscription, 0, len(rows))
	for _, r := range rows {
		out = append(out, subscriptionCopy(r))
	}
	return pageOf(out, total, q), nil
}

// AllInvoicesPage is a page of every account's invoices, for the platform's
// admin. The rows are copies that hold no connection.
func (s *BillingService) AllInvoicesPage(ctx context.Context, actor security.Subject, q PageQuery, perPage int) (Page[Invoice], error) {
	q = q.normalized(perPage)
	g, err := security.Authorize(ctx, s.policy, actor, InvoicesManage, Account{})
	if err != nil {
		return Page[Invoice]{}, err
	}
	column, err := sortColumn(invoiceSorts, q)
	if err != nil {
		return Page[Invoice]{}, err
	}
	filtered := func() *everytenant.InvoiceQuery {
		b := everytenant.Invoices(s.db)
		if q.Status != "" {
			b = b.Where("status", "=", q.Status)
		}
		if q.Search != "" {
			like := "%" + q.Search + "%"
			b = b.Where(func(w *everytenant.InvoiceQuery) {
				w.Where("tenant_id", "like", like).OrWhere("plan_name", "like", like).
					OrWhere("coupon_code", "like", like).OrWhere("id", "=", q.Search).OrWhere("provider_ref", "=", q.Search)
			})
		}
		return b
	}
	total, err := filtered().Count(ctx, g)
	if err != nil {
		return Page[Invoice]{}, err
	}
	b := filtered()
	if q.Dir == "asc" {
		b = b.OrderBy(column)
	} else {
		b = b.OrderByDesc(column)
	}
	rows, err := b.OrderByDesc("id").Offset((q.Page-1)*q.PerPage).Limit(q.PerPage).Get(ctx, g)
	if err != nil {
		return Page[Invoice]{}, err
	}
	out := make([]Invoice, 0, len(rows))
	for _, r := range rows {
		out = append(out, invoiceCopy(r))
	}
	return pageOf(out, total, q), nil
}

// PlanNames are the names of the plans, by id, for a screen that lists
// subscriptions.
func (s *BillingService) PlanNames(ctx context.Context, actor security.Subject) (map[string]string, error) {
	g, err := security.Authorize(ctx, s.policy, actor, PlansView, Account{})
	if err != nil {
		return nil, err
	}
	rows, err := Plans(s.db).Select("id", "name").Limit(1000).Get(ctx, g)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, p := range rows {
		out[p.ID] = p.Name
	}
	return out, nil
}

func subscriptionCopy(r *everytenant.Subscription) Subscription {
	return Subscription{
		ID: r.ID, TenantID: r.TenantID, Reference: r.Reference, PlanID: r.PlanID,
		Status: SubscriptionStatus(r.Status), CurrentPeriodStart: r.CurrentPeriodStart,
		CurrentPeriodEnd: r.CurrentPeriodEnd, TrialEndsAt: r.TrialEndsAt,
		CancelAtPeriodEnd: r.CancelAtPeriodEnd != 0, CanceledAt: r.CanceledAt, EndedAt: r.EndedAt,
		GraceDays: r.GraceDays, CouponID: r.CouponID, CouponRenewalsLeft: r.CouponRenewalsLeft,
		Renewals: r.Renewals, CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func invoiceCopy(r *everytenant.Invoice) Invoice {
	return Invoice{
		ID: r.ID, TenantID: r.TenantID, SubscriptionID: r.SubscriptionID, PlanID: r.PlanID,
		PlanName: r.PlanName, Reference: r.Reference, Kind: InvoiceKind(r.Kind), Status: InvoiceStatus(r.Status),
		Months: r.Months, Subtotal: r.Subtotal, Discount: r.Discount, Total: r.Total,
		CouponID: r.CouponID, CouponCode: r.CouponCode, RenewalNumber: r.RenewalNumber,
		MaxInstallments: r.MaxInstallments, Provider: r.Provider, ProviderRef: r.ProviderRef,
		ProviderStatus: r.ProviderStatus, CheckoutURL: r.CheckoutURL, PaidAt: r.PaidAt,
		CreatedBy: r.CreatedBy, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}
