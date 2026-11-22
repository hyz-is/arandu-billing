package unit_test

import (
	"errors"
	"testing"

	billing "github.com/hyz-is/arandu-billing"
)

func monthly(price int64, cycles ...billing.Cycle) billing.Plan {
	p := plan(price, 1)
	p.Cycles = cycles
	return p
}

func TestACycleChargesTheMonthlyPriceForItsMonthsLessItsDiscount(t *testing.T) {
	t.Parallel()
	p := monthly(9990, billing.Cycle{Months: 3, Discount: 500}, billing.Cycle{Months: 12, Discount: 1750})
	cases := []struct {
		months                  int
		subtotal, off, expected int64
	}{
		{0, 9990, 0, 9990},      // the plan's own interval
		{1, 9990, 0, 9990},      // the same, named
		{3, 29970, 1498, 28472}, // 95% of 299,70 is 284,715: the price rounds half up to 284,72
		{12, 119880, 20979, 98901},
	}
	for _, c := range cases {
		q, err := billing.PriceCycle(p, c.months, nil, false, at("2026-11-22 10:00"))
		if err != nil {
			t.Fatalf("%d months: %v", c.months, err)
		}
		if q.Subtotal != c.subtotal || q.CycleDiscount != c.off || q.Total != c.expected || q.Discount != 0 {
			t.Errorf("%d months: got %+v, want subtotal %d, off %d, total %d", c.months, q, c.subtotal, c.off, c.expected)
		}
	}
}

func TestACycleThePlanDoesNotOfferIsRefused(t *testing.T) {
	t.Parallel()
	p := monthly(10000, billing.Cycle{Months: 3, Discount: 500})
	for _, months := range []int{2, 6, 12, -1} {
		if _, err := billing.PriceCycle(p, months, nil, false, at("2026-11-22 10:00")); !errors.Is(err, billing.ErrCycleUnavailable) {
			t.Errorf("%d months: got %v, want ErrCycleUnavailable", months, err)
		}
	}
	if _, err := billing.Price(plan(10000, 1), nil, false, at("2026-11-22 10:00")); err != nil {
		t.Errorf("a plan with no cycles refuses its own interval: %v", err)
	}
}

func TestAPlanWithoutCyclesPricesAsItAlwaysDid(t *testing.T) {
	t.Parallel()
	c := billing.Coupon{Kind: billing.CouponPercent, Value: 1000, Active: true}
	q, err := billing.Price(plan(60000, 6), &c, false, at("2026-11-22 10:00"))
	if err != nil {
		t.Fatal(err)
	}
	if q.Subtotal != 60000 || q.CycleDiscount != 0 || q.Discount != 6000 || q.Total != 54000 {
		t.Fatalf("got %+v", q)
	}
}

func TestACouponTakesItsDiscountOffThePriceTheCycleLeft(t *testing.T) {
	t.Parallel()
	p := monthly(10000, billing.Cycle{Months: 3, Discount: 1000})
	c := billing.Coupon{Kind: billing.CouponPercent, Value: 1000, Active: true}
	q, err := billing.PriceCycle(p, 3, &c, false, at("2026-11-22 10:00"))
	if err != nil {
		t.Fatal(err)
	}
	// 3 x 100,00 = 300,00; the cycle takes 30,00 off; the coupon 10% of 270,00.
	if q.Subtotal != 30000 || q.CycleDiscount != 3000 || q.Discount != 2700 || q.Total != 24300 {
		t.Fatalf("got %+v", q)
	}
	if q.Subtotal-q.CycleDiscount-q.Discount != q.Total {
		t.Errorf("the quote does not add up: %+v", q)
	}
	// The minimum purchase is read against the price the cycle left.
	min := billing.Coupon{Kind: billing.CouponPercent, Value: 1000, Active: true, MinPurchase: 28000}
	if _, err := billing.PriceCycle(p, 3, &min, false, at("2026-11-22 10:00")); !errors.Is(err, billing.ErrCouponMinPurchase) {
		t.Errorf("got %v, want ErrCouponMinPurchase", err)
	}
}

func TestInstallmentsFollowTheCycleChosen(t *testing.T) {
	t.Parallel()
	p := monthly(10000, billing.Cycle{Months: 6, Discount: 800}, billing.Cycle{Months: 12, Discount: 1500})
	p.InstallmentsFrom = 6
	for months, want := range map[int]int{1: 1, 6: 6, 12: 12} {
		if got := p.InstallmentsFor(months); got != want {
			t.Errorf("%d months: %d installments, want %d", months, got, want)
		}
	}
	if q, _ := billing.PriceCycle(p, 6, nil, false, at("2026-11-22 10:00")); q.MaxInstallments != 6 {
		t.Errorf("the quote offers %d installments", q.MaxInstallments)
	}
}

func TestACycleAtThePlansOwnIntervalMayBeDiscounted(t *testing.T) {
	t.Parallel()
	p := monthly(10000, billing.Cycle{Months: 1, Discount: 1000})
	q, err := billing.Price(p, nil, false, at("2026-11-22 10:00"))
	if err != nil || q.Total != 9000 {
		t.Fatalf("got %+v, %v", q, err)
	}
	if got := p.OfferedCycles(); len(got) != 1 || got[0].Discount != 1000 {
		t.Errorf("offered %+v", got)
	}
}

func TestTheOfferedCyclesAreTheOwnIntervalAndTheDeclaredOnesShortestFirst(t *testing.T) {
	t.Parallel()
	p := monthly(10000, billing.Cycle{Months: 12, Discount: 1500}, billing.Cycle{Months: 3, Discount: 500}, billing.Cycle{Months: 2, Discount: 200})
	var got []int
	for _, c := range p.OfferedCycles() {
		got = append(got, c.Months)
	}
	if len(got) != 4 || got[0] != 1 || got[1] != 2 || got[2] != 3 || got[3] != 12 {
		t.Errorf("offered %v", got)
	}
}
