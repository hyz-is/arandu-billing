package billing

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/database/model"
	"github.com/arandu-io/hesape/translation"
	activitylog "github.com/hyz-is/arandu-activitylog"
)

// BillingService orchestrates the use cases of this package: it validates the
// request, asks the policy for a Grant, applies the rules the entities and the
// pricing hold, and persists with that Grant.
//
// It receives its collaborators through the constructor and the module's
// configuration. Everything a handler is allowed to do goes through here: the
// service is the only owner of the database handle, so the request layer
// cannot reach a Model before the policy has answered.
type BillingService struct {
	db     *data.DB
	policy BillingPolicy
	// now is the clock, a field so a test can hold it still.
	now func() time.Time
	// providers are the gateways, by name; fallback is the one a checkout
	// uses when none is named.
	providers map[string]Provider
	fallback  string
	// listener is Config.Listener.
	listener Listener
	// customer is Config.Customer.
	customer func(ctx context.Context, who security.Subject) (Customer, error)
	// returnURL builds the address a gateway sends the customer back to.
	returnURL func(invoiceID string) string
	// activity is Config.Activity; describe turns an event into its sentence.
	activity *activitylog.Logger
	describe func(event string) string
	// labels are the sentences of one request.
	labels func(ctx context.Context) Labels
}

// NewBillingService wires the service over the application's database handle,
// with no provider and no listener: New wires the module's from its Config.
func NewBillingService(db *data.DB) *BillingService {
	return &BillingService{
		db:        db,
		now:       func() time.Time { return time.Now().UTC() },
		providers: map[string]Provider{},
	}
}

// useProviders sets the gateways the service checks out with; fallback is the
// one used when none is named.
func (s *BillingService) useProviders(providers []Provider, fallback string) {
	s.providers = map[string]Provider{}
	for _, p := range providers {
		if p != nil {
			s.providers[p.Name()] = p
		}
	}
	s.fallback = fallback
}

// labelsFor are the sentences of one request.
func (s *BillingService) labelsFor(ctx context.Context) Labels {
	if s.labels != nil {
		return s.labels(ctx)
	}
	return Labels{Locale: translation.Locale(ctx)}
}

// notFound turns the model's missing row into ErrNotFound, and leaves every
// other error as it is.
func notFound(err error) error {
	if errors.Is(err, model.ErrModelNotFound) {
		return ErrNotFound
	}
	return err
}

// newID is a fresh identifier.
func newID() (string, error) { return data.NewID() }

// actingIn is the admin as they act inside one account: the same person,
// roles and actions, with the account's tenant, so the Grant the policy issues
// writes that account's rows and the activity log names the admin as causer.
func actingIn(admin security.Subject, tenant string) security.Subject {
	out := admin
	out.Tenant = tenant
	return out
}

// PlanRequest is the input contract for a plan of the catalogue.
//
// The fields are explicit and there is no mass assignment, so a request body
// cannot write a column nobody meant to expose.
type PlanRequest struct {
	// Slug is the stable name; empty is derived from Name.
	Slug string
	// Name is what a customer reads.
	Name string
	// Description is the sentence under the name.
	Description string
	// Features are the lines under the price.
	Features []string
	// Price is what one period costs, in centavos.
	Price int64
	// IntervalMonths is how many months one payment buys.
	IntervalMonths int
	// TrialDays is the trial's length; zero offers none.
	TrialDays int
	// GraceDays is how long a lapsed subscription stays past due.
	GraceDays int
	// InstallmentsFrom is the smallest period paid in installments; zero
	// never.
	InstallmentsFrom int
	// InstallmentsCap is the most installments; zero is MaxInstallments.
	InstallmentsCap int
	// Meta is the consumer's own facts about the plan.
	Meta Meta
	// Active says the plan is offered.
	Active bool
	// Position orders the plans.
	Position int
}

// Validate reports the errors per field.
func (r PlanRequest) Validate() validation.Errors {
	e := validation.Errors{}
	validation.Required(e, "name", r.Name)
	validation.MaxLen(e, "name", r.Name, maxNameLen)
	validation.MaxLen(e, "slug", r.Slug, maxSlugLen)
	if slug := Slugify(r.Slug, r.Name); slug == "" && strings.TrimSpace(r.Name) != "" {
		e.Add("slug", message("slug"))
	}
	validation.MaxLen(e, "description", r.Description, maxTextLen)
	if r.Price < 0 {
		e.Add("price", message("not_negative"))
	}
	if r.IntervalMonths < 1 || r.IntervalMonths > MaxIntervalMonths {
		e.Add("interval_months", message("between", translation.Replace{"min": "1", "max": itoa(MaxIntervalMonths)}))
	}
	if r.TrialDays < 0 || r.TrialDays > 365 {
		e.Add("trial_days", message("between", translation.Replace{"min": "0", "max": "365"}))
	}
	if r.GraceDays < 0 || r.GraceDays > 365 {
		e.Add("grace_days", message("between", translation.Replace{"min": "0", "max": "365"}))
	}
	if r.InstallmentsFrom < 0 || r.InstallmentsFrom > MaxIntervalMonths {
		e.Add("installments_from", message("between", translation.Replace{"min": "0", "max": itoa(MaxIntervalMonths)}))
	}
	if r.InstallmentsCap < 0 || r.InstallmentsCap > MaxInstallments {
		e.Add("installments_cap", message("between", translation.Replace{"min": "0", "max": itoa(MaxInstallments)}))
	}
	features := 0
	for _, f := range r.Features {
		if strings.TrimSpace(f) != "" {
			features++
		}
		if len(f) > maxNameLen {
			e.Add("features", message("feature_length", translation.Replace{"max": itoa(maxNameLen)}))
			break
		}
	}
	if features > 40 {
		e.Add("features", message("features_count", translation.Replace{"max": "40"}))
	}
	if len(r.Meta) > 40 {
		e.Add("meta", message("meta_count", translation.Replace{"max": "40"}))
	}
	return e
}

func (r PlanRequest) apply(p *Plan, now time.Time) {
	p.Slug = Slugify(r.Slug, r.Name)
	p.Name = strings.TrimSpace(r.Name)
	p.Description = strings.TrimSpace(r.Description)
	p.Features = cleanLines(r.Features)
	p.Price = r.Price
	p.IntervalMonths = r.IntervalMonths
	p.TrialDays = r.TrialDays
	p.GraceDays = r.GraceDays
	p.InstallmentsFrom = r.InstallmentsFrom
	p.InstallmentsCap = r.InstallmentsCap
	p.Meta = r.Meta
	if p.Meta == nil {
		p.Meta = Meta{}
	}
	p.Active = Flag(r.Active)
	p.Position = r.Position
	p.UpdatedAt = now
}

// ActivePlans are the plans the catalogue offers, in their order: what an
// account reads before it subscribes.
func (s *BillingService) ActivePlans(ctx context.Context, actor security.Subject) ([]*Plan, error) {
	g, err := security.Authorize(ctx, s.policy, actor, PlansView, Account{})
	if err != nil {
		return nil, err
	}
	return Plans(s.db).Where("active", "=", 1).OrderBy("position").OrderBy("price").OrderBy("id").Limit(MaxPageSize).Get(ctx, g)
}

// Plan is one plan of the catalogue, active or not, for the admin.
func (s *BillingService) Plan(ctx context.Context, actor security.Subject, id string) (*Plan, error) {
	g, err := security.Authorize(ctx, s.policy, actor, PlansManage, Account{})
	if err != nil {
		return nil, err
	}
	p, err := Plans(s.db).WhereKey(id).FirstOrFail(ctx, g)
	return p, notFound(err)
}

// PlanBySlug is one plan by the name the application's code refers to it by,
// active or not, for any signed-in subject: what an application's seeder or
// its own screens look a plan up by.
func (s *BillingService) PlanBySlug(ctx context.Context, actor security.Subject, slug string) (*Plan, error) {
	g, err := security.Authorize(ctx, s.policy, actor, PlansView, Account{})
	if err != nil {
		return nil, err
	}
	p, err := Plans(s.db).Where("slug", "=", strings.TrimSpace(slug)).FirstOrFail(ctx, g)
	return p, notFound(err)
}

// CreatePlan adds a plan to the catalogue.
func (s *BillingService) CreatePlan(ctx context.Context, actor security.Subject, in PlanRequest) (*Plan, error) {
	if errs := in.Validate(); errs.Any() {
		return nil, errs
	}
	g, err := security.Authorize(ctx, s.policy, actor, PlansManage, Account{})
	if err != nil {
		return nil, err
	}
	slug := Slugify(in.Slug, in.Name)
	taken, err := Plans(s.db).Where("slug", "=", slug).Exists(ctx, g)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, validation.Errors{"slug": {message("slug_taken")}}
	}
	p, err := Plans(s.db).New()
	if err != nil {
		return nil, err
	}
	if p.ID, err = newID(); err != nil {
		return nil, err
	}
	now := s.now()
	in.apply(p, now)
	p.CreatedAt = now
	if _, err := p.Save(ctx, g); err != nil {
		return nil, err
	}
	return p, s.record(ctx, g, actor, activityPlanCreated, "billing_plan", p.ID, map[string]any{"slug": p.Slug, "price": p.Price, "months": p.IntervalMonths})
}

// UpdatePlan changes a plan. The subscriptions already sold keep the period
// they were sold; the next invoice charges the new price.
func (s *BillingService) UpdatePlan(ctx context.Context, actor security.Subject, id string, in PlanRequest) (*Plan, error) {
	if errs := in.Validate(); errs.Any() {
		return nil, errs
	}
	g, err := security.Authorize(ctx, s.policy, actor, PlansManage, Account{})
	if err != nil {
		return nil, err
	}
	p, err := Plans(s.db).WhereKey(id).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	slug := Slugify(in.Slug, in.Name)
	taken, err := Plans(s.db).Where("slug", "=", slug).Where("id", "!=", p.ID).Exists(ctx, g)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, validation.Errors{"slug": {message("slug_taken")}}
	}
	before := map[string]any{"price": p.Price, "months": p.IntervalMonths, "active": bool(p.Active)}
	in.apply(p, s.now())
	if _, err := p.Save(ctx, g); err != nil {
		return nil, err
	}
	return p, s.record(ctx, g, actor, activityPlanUpdated, "billing_plan", p.ID, map[string]any{
		"before": before, "after": map[string]any{"price": p.Price, "months": p.IntervalMonths, "active": bool(p.Active)},
	})
}

// CouponRequest is the input contract for a coupon.
type CouponRequest struct {
	// Code is what the customer types; it is stored upper case.
	Code string
	// Description is a note for the admin.
	Description string
	// Kind is CouponPercent or CouponFixed.
	Kind CouponKind
	// Value is basis points for CouponPercent, centavos for CouponFixed.
	Value int64
	// MaxUses is how many first payments may use it; zero is unlimited.
	MaxUses int
	// Renewals is how many renewals keep the discount.
	Renewals int
	// MinPurchase is the smallest price it applies to, in centavos.
	MinPurchase int64
	// PlanID restricts it to one plan; empty is every plan.
	PlanID string
	// ValidFrom and ValidUntil bound when it is accepted; nil is unbounded.
	ValidFrom, ValidUntil *time.Time
	// Active says it is accepted.
	Active bool
}

// Validate reports the errors per field.
func (r CouponRequest) Validate() validation.Errors {
	e := validation.Errors{}
	code := Code(r.Code)
	validation.Required(e, "code", code)
	validation.MaxLen(e, "code", code, maxCodeLen)
	if code != "" && !couponCode(code) {
		e.Add("code", message("coupon_code"))
	}
	validation.MaxLen(e, "description", r.Description, maxTextLen)
	switch r.Kind {
	case CouponPercent:
		if r.Value < 1 || r.Value > 10000 {
			e.Add("value", message("percent"))
		}
	case CouponFixed:
		if r.Value < 1 {
			e.Add("value", message("positive"))
		}
	default:
		e.Add("kind", message("coupon_kind"))
	}
	if r.MaxUses < 0 {
		e.Add("max_uses", message("not_negative"))
	}
	if r.Renewals < 0 || r.Renewals > MaxIntervalMonths {
		e.Add("renewals", message("between", translation.Replace{"min": "0", "max": itoa(MaxIntervalMonths)}))
	}
	if r.MinPurchase < 0 {
		e.Add("min_purchase", message("not_negative"))
	}
	validation.MaxLen(e, "plan_id", r.PlanID, maxIdentifierLen)
	if r.ValidFrom != nil && r.ValidUntil != nil && !r.ValidUntil.After(*r.ValidFrom) {
		e.Add("valid_until", message("after_start"))
	}
	return e
}

// couponCode says the code is letters, digits, - and _.
func couponCode(code string) bool {
	for _, r := range code {
		if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func (r CouponRequest) apply(c *Coupon, now time.Time) {
	c.Code = Code(r.Code)
	c.Description = strings.TrimSpace(r.Description)
	c.Kind = r.Kind
	c.Value = r.Value
	c.MaxUses = r.MaxUses
	c.Renewals = r.Renewals
	c.MinPurchase = r.MinPurchase
	c.PlanID = strings.TrimSpace(r.PlanID)
	c.ValidFrom = utcPtr(r.ValidFrom)
	c.ValidUntil = utcPtr(r.ValidUntil)
	c.Active = Flag(r.Active)
	c.UpdatedAt = now
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	u := t.UTC()
	return &u
}

// CreateCoupon adds a coupon.
func (s *BillingService) CreateCoupon(ctx context.Context, actor security.Subject, in CouponRequest) (*Coupon, error) {
	if errs := in.Validate(); errs.Any() {
		return nil, errs
	}
	g, err := security.Authorize(ctx, s.policy, actor, CouponsManage, Account{})
	if err != nil {
		return nil, err
	}
	taken, err := Coupons(s.db).Where("code", "=", Code(in.Code)).Exists(ctx, g)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, validation.Errors{"code": {message("coupon_taken")}}
	}
	if in.PlanID != "" {
		if exists, err := Plans(s.db).WhereKey(in.PlanID).Exists(ctx, g); err != nil {
			return nil, err
		} else if !exists {
			return nil, validation.Errors{"plan_id": {message("plan_unknown")}}
		}
	}
	c, err := Coupons(s.db).New()
	if err != nil {
		return nil, err
	}
	if c.ID, err = newID(); err != nil {
		return nil, err
	}
	now := s.now()
	in.apply(c, now)
	c.CreatedAt = now
	if _, err := c.Save(ctx, g); err != nil {
		return nil, err
	}
	return c, s.record(ctx, g, actor, activityCouponCreated, "billing_coupon", c.ID, map[string]any{"code": c.Code, "kind": string(c.Kind), "value": c.Value})
}

// UpdateCoupon changes a coupon. The code stays; a coupon is retired by
// deactivating it, never by renaming it.
func (s *BillingService) UpdateCoupon(ctx context.Context, actor security.Subject, id string, in CouponRequest) (*Coupon, error) {
	g, err := security.Authorize(ctx, s.policy, actor, CouponsManage, Account{})
	if err != nil {
		return nil, err
	}
	c, err := Coupons(s.db).WhereKey(id).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	in.Code = c.Code
	if errs := in.Validate(); errs.Any() {
		return nil, errs
	}
	before := map[string]any{"active": bool(c.Active), "value": c.Value, "max_uses": c.MaxUses}
	in.apply(c, s.now())
	if _, err := c.Save(ctx, g); err != nil {
		return nil, err
	}
	return c, s.record(ctx, g, actor, activityCouponUpdated, "billing_coupon", c.ID, map[string]any{
		"before": before, "after": map[string]any{"active": bool(c.Active), "value": c.Value, "max_uses": c.MaxUses},
	})
}

// SetCouponActive switches a coupon on or off, keeping everything else.
func (s *BillingService) SetCouponActive(ctx context.Context, actor security.Subject, id string, active bool) (*Coupon, error) {
	g, err := security.Authorize(ctx, s.policy, actor, CouponsManage, Account{})
	if err != nil {
		return nil, err
	}
	c, err := Coupons(s.db).WhereKey(id).FirstOrFail(ctx, g)
	if err != nil {
		return nil, notFound(err)
	}
	if bool(c.Active) == active {
		return c, nil
	}
	c.Active = Flag(active)
	c.UpdatedAt = s.now()
	if _, err := c.Save(ctx, g); err != nil {
		return nil, err
	}
	return c, s.record(ctx, g, actor, activityCouponUpdated, "billing_coupon", c.ID, map[string]any{"active": active})
}

// Slugify is the slug a plan is stored under: the one given, or one derived
// from the name -- lower case ASCII letters and digits joined by "-".
func Slugify(slug, name string) string {
	source := strings.TrimSpace(slug)
	if source == "" {
		source = name
	}
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(foldAccents(source)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.TrimSuffix(b.String(), "-")
	if len(out) > maxSlugLen {
		out = strings.TrimSuffix(out[:maxSlugLen], "-")
	}
	return out
}

// foldAccents takes the accents off the Latin letters Portuguese writes.
func foldAccents(s string) string {
	return strings.NewReplacer(
		"á", "a", "à", "a", "â", "a", "ã", "a", "ä", "a", "Á", "a", "À", "a", "Â", "a", "Ã", "a",
		"é", "e", "ê", "e", "è", "e", "É", "e", "Ê", "e",
		"í", "i", "ì", "i", "Í", "i",
		"ó", "o", "ô", "o", "õ", "o", "ò", "o", "Ó", "o", "Ô", "o", "Õ", "o",
		"ú", "u", "ü", "u", "ù", "u", "Ú", "u",
		"ç", "c", "Ç", "c",
	).Replace(s)
}

func cleanLines(lines []string) StringList {
	out := StringList{}
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}
