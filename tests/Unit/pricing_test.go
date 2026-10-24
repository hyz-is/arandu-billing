package unit_test

import (
	"errors"
	"testing"
	"time"

	billing "github.com/hyz-is/arandu-billing"
)

func at(day string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", day)
	if err != nil {
		panic(err)
	}
	return t
}

func ptr(t time.Time) *time.Time { return &t }

func plan(price int64, months int) billing.Plan {
	return billing.Plan{ID: "p1", Price: price, IntervalMonths: months, Active: true}
}

func TestAPercentCouponIsRoundedDownToTheCentavo(t *testing.T) {
	t.Parallel()
	c := billing.Coupon{Kind: billing.CouponPercent, Value: 1250, Active: true}
	q, err := billing.Price(plan(9990, 1), &c, false, at("2026-10-23 10:00"))
	if err != nil {
		t.Fatal(err)
	}
	// 12,5% of R$ 99,90 is R$ 12,4875: R$ 12,48 off, R$ 87,42 charged.
	if q.Subtotal != 9990 || q.Discount != 1248 || q.Total != 8742 || !q.CouponApplied {
		t.Fatalf("got %+v", q)
	}
}

func TestAFixedCouponNeverTakesMoreThanThePrice(t *testing.T) {
	t.Parallel()
	c := billing.Coupon{Kind: billing.CouponFixed, Value: 50000, Active: true}
	q, err := billing.Price(plan(9900, 1), &c, false, at("2026-10-23 10:00"))
	if err != nil {
		t.Fatal(err)
	}
	if q.Discount != 9900 || q.Total != 0 {
		t.Fatalf("got %+v", q)
	}
	full := billing.Coupon{Kind: billing.CouponPercent, Value: 10000, Active: true}
	if q, _ := billing.Price(plan(9900, 1), &full, false, at("2026-10-23 10:00")); q.Total != 0 {
		t.Fatalf("100%% off charged %d", q.Total)
	}
}

func TestACouponIsRefusedForEveryReasonItCanBe(t *testing.T) {
	t.Parallel()
	now := at("2026-10-23 10:00")
	base := billing.Coupon{Kind: billing.CouponPercent, Value: 1000, Active: true}
	cases := map[error]billing.Coupon{}
	inactive := base
	inactive.Active = false
	cases[billing.ErrCouponInactive] = inactive
	early := base
	early.ValidFrom = ptr(now.Add(time.Hour))
	cases[billing.ErrCouponNotStarted] = early
	late := base
	late.ValidUntil = ptr(now)
	cases[billing.ErrCouponExpired] = late
	used := base
	used.MaxUses, used.Uses = 3, 3
	cases[billing.ErrCouponExhausted] = used
	other := base
	other.PlanID = "p2"
	cases[billing.ErrCouponWrongPlan] = other
	small := base
	small.MinPurchase = 10000
	cases[billing.ErrCouponMinPurchase] = small
	for want, c := range cases {
		c := c
		if _, err := billing.Price(plan(9900, 1), &c, false, now); !errors.Is(err, want) {
			t.Errorf("want %v, got %v", want, err)
		}
	}
	// A renewal keeps a discount already granted, whatever the coupon became.
	if q, err := billing.Price(plan(9900, 1), &inactive, true, now); err != nil || q.Discount != 990 {
		t.Errorf("a granted renewal lost its discount: %+v, %v", q, err)
	}
	unlimited := base
	unlimited.Uses = 1000
	if _, err := billing.Price(plan(9900, 1), &unlimited, false, now); err != nil {
		t.Errorf("a coupon with no maximum was refused after many uses: %v", err)
	}
}

func TestInstallmentsStartAtTheThresholdAndFollowTheMonths(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		from, months, capped, want int
	}{
		{0, 12, 0, 1},  // never in installments
		{6, 3, 0, 1},   // below the threshold
		{6, 6, 0, 6},   // up to the number of months
		{6, 12, 0, 12}, // a year in twelve
		{6, 24, 0, 12}, // never above twelve
		{6, 12, 10, 10},
		{1, 1, 0, 1},
	} {
		p := billing.Plan{IntervalMonths: c.months, InstallmentsFrom: c.from, InstallmentsCap: c.capped}
		if got := p.Installments(); got != c.want {
			t.Errorf("from %d, %d months, cap %d: %d installments, want %d", c.from, c.months, c.capped, got, c.want)
		}
		q, _ := billing.Price(billing.Plan{Price: 1000, IntervalMonths: c.months, InstallmentsFrom: c.from, InstallmentsCap: c.capped}, nil, false, time.Now())
		if q.MaxInstallments != c.want {
			t.Errorf("the quote offers %d installments, want %d", q.MaxInstallments, c.want)
		}
	}
}

func TestMonthsAreAddedHoldingTheDayInsideTheMonth(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ from, want string }{
		{"2026-01-31 09:30", "2026-02-28 09:30"},
		{"2028-01-31 09:30", "2028-02-29 09:30"},
		{"2026-10-23 09:30", "2026-11-23 09:30"},
		{"2026-12-15 00:00", "2027-01-15 00:00"},
	} {
		if got := billing.AddMonths(at(c.from), 1); !got.Equal(at(c.want)) {
			t.Errorf("%s + 1 month = %s, want %s", c.from, got, c.want)
		}
	}
	if got := billing.AddMonths(at("2026-08-31 12:00"), 6); !got.Equal(at("2027-02-28 12:00")) {
		t.Errorf("Aug 31 + 6 months = %s", got)
	}
}

func TestTheNextPeriodNeverLosesADayAlreadyPaid(t *testing.T) {
	t.Parallel()
	paid := at("2026-10-23 10:00")
	if p := billing.NextPeriod(nil, paid, 1); !p.Start.Equal(paid) || !p.End.Equal(at("2026-11-23 10:00")) {
		t.Errorf("a first payment bought %v", p)
	}
	active := &billing.Subscription{Status: billing.SubscriptionActive, CurrentPeriodStart: at("2026-10-01 00:00"), CurrentPeriodEnd: at("2026-11-01 00:00")}
	if p := billing.NextPeriod(active, paid, 3); !p.End.Equal(at("2027-02-01 00:00")) || !p.Start.Equal(at("2026-10-01 00:00")) {
		t.Errorf("an early renewal bought %v", p)
	}
	trial := &billing.Subscription{Status: billing.SubscriptionTrialing, CurrentPeriodStart: at("2026-10-20 00:00"), CurrentPeriodEnd: at("2026-10-27 00:00")}
	if p := billing.NextPeriod(trial, paid, 1); !p.Start.Equal(paid) || !p.End.Equal(at("2026-11-27 00:00")) {
		t.Errorf("a trial paid for bought %v", p)
	}
	late := &billing.Subscription{Status: billing.SubscriptionPastDue, CurrentPeriodEnd: at("2026-10-20 00:00"), GraceDays: 5}
	if p := billing.NextPeriod(late, paid, 1); !p.Start.Equal(at("2026-10-20 00:00")) || !p.End.Equal(at("2026-11-20 00:00")) {
		t.Errorf("a payment in the grace bought %v", p)
	}
	ended := &billing.Subscription{Status: billing.SubscriptionExpired, CurrentPeriodEnd: at("2026-01-20 00:00")}
	if p := billing.NextPeriod(ended, paid, 1); !p.Start.Equal(paid) {
		t.Errorf("a payment after the end bought %v", p)
	}
	stale := &billing.Subscription{Status: billing.SubscriptionActive, CurrentPeriodEnd: at("2026-09-01 00:00")}
	if p := billing.NextPeriod(stale, paid, 1); !p.Start.Equal(paid) {
		t.Errorf("an active subscription the sweep had not reached bought %v", p)
	}
}

func TestTheClockMovesASubscriptionOnlyPastItsEnd(t *testing.T) {
	t.Parallel()
	end := at("2026-11-01 00:00")
	sub := func(status billing.SubscriptionStatus, grace int, cancel bool) billing.Subscription {
		return billing.Subscription{Status: status, CurrentPeriodEnd: end, GraceDays: grace, CancelAtPeriodEnd: billing.Flag(cancel)}
	}
	for _, c := range []struct {
		name string
		s    billing.Subscription
		now  time.Time
		want billing.SubscriptionStatus
		move bool
	}{
		{"active before the end", sub(billing.SubscriptionActive, 0, false), end.Add(-time.Second), billing.SubscriptionActive, false},
		{"active at the end, no grace", sub(billing.SubscriptionActive, 0, false), end, billing.SubscriptionExpired, true},
		{"active at the end, grace", sub(billing.SubscriptionActive, 5, false), end, billing.SubscriptionPastDue, true},
		{"active long past the grace", sub(billing.SubscriptionActive, 5, false), end.AddDate(0, 0, 6), billing.SubscriptionExpired, true},
		{"cancellation asked", sub(billing.SubscriptionActive, 5, true), end, billing.SubscriptionCanceled, true},
		{"past due inside the grace", sub(billing.SubscriptionPastDue, 5, false), end.AddDate(0, 0, 4), billing.SubscriptionPastDue, false},
		{"past due after the grace", sub(billing.SubscriptionPastDue, 5, false), end.AddDate(0, 0, 5), billing.SubscriptionExpired, true},
		{"trial at its end", sub(billing.SubscriptionTrialing, 5, false), end, billing.SubscriptionExpired, true},
		{"trial canceled", sub(billing.SubscriptionTrialing, 0, true), end, billing.SubscriptionCanceled, true},
		{"expired stays", sub(billing.SubscriptionExpired, 0, false), end.AddDate(1, 0, 0), billing.SubscriptionExpired, false},
	} {
		got := billing.Due(c.s, c.now)
		if got.To != c.want || got.Moved != c.move {
			t.Errorf("%s: %+v, want %s moved=%v", c.name, got, c.want, c.move)
		}
	}
}

func TestSlugsAndCodesAreNormalized(t *testing.T) {
	t.Parallel()
	if got := billing.Slugify("", "Câmera Mensal — Plus"); got != "camera-mensal-plus" {
		t.Errorf("slug %q", got)
	}
	if got := billing.Code("  bemVindo10 "); got != "BEMVINDO10" {
		t.Errorf("code %q", got)
	}
}
