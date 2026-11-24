package unit_test

import (
	"errors"
	"testing"

	billing "github.com/hyz-is/arandu-billing"
)

// teamPlan is a plan of R$ 119,90 a month with one member included and
// R$ 20,00 for each member beyond it.
func teamPlan(price, unit int64, interval int) billing.Plan {
	p := plan(price, interval)
	p.IncludedUnits = 1
	p.UnitPrice = unit
	return p
}

func TestUnitsAboveTheIncludedOnesAreAddedToTheSubtotal(t *testing.T) {
	t.Parallel()
	now := at("2026-11-24 10:00")

	monthly := teamPlan(11990, 2000, 1)
	q, err := billing.PriceUnits(monthly, 1, 3, nil, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if q.UnitsAmount != 4000 || q.Subtotal != 15990 || q.Total != 15990 {
		t.Fatalf("monthly, 3 members: %+v", q)
	}

	// A plan sold by the year, and the same plan sold by the month for twelve.
	yearly := teamPlan(119880, 24000, 12)
	q, err = billing.PriceUnits(yearly, 0, 3, nil, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if q.UnitsAmount != 48000 || q.Subtotal != 167880 || q.Total != 167880 {
		t.Fatalf("yearly, 3 members: %+v", q)
	}
	scaled := teamPlan(11990, 2000, 1)
	scaled.Cycles = billing.CycleList{{Months: 12}}
	q, err = billing.PriceUnits(scaled, 12, 3, nil, false, now)
	if err != nil {
		t.Fatal(err)
	}
	if q.UnitsAmount != 48000 || q.Subtotal != 11990*12+48000 {
		t.Fatalf("monthly plan sold for twelve months, 3 members: %+v", q)
	}
}

func TestTheCycleDiscountAndTheCouponTakeTheUnitsOffToo(t *testing.T) {
	t.Parallel()
	p := teamPlan(10000, 2000, 1)
	p.Cycles = billing.CycleList{{Months: 12, Discount: 1000}}
	c := billing.Coupon{Kind: billing.CouponPercent, Value: 1000, Active: true}
	q, err := billing.PriceUnits(p, 12, 3, &c, false, at("2026-11-24 10:00"))
	if err != nil {
		t.Fatal(err)
	}
	// 120000 + 48000 = 168000; 10% off the cycle is 16800; 10% off 151200 is 15120.
	if q.Subtotal != 168000 || q.UnitsAmount != 48000 || q.CycleDiscount != 16800 || q.Discount != 15120 || q.Total != 136080 {
		t.Fatalf("got %+v", q)
	}
}

func TestUnitsRoundHalfUpToTheCentavo(t *testing.T) {
	t.Parallel()
	p := teamPlan(1000, 1001, 2) // 10,01 per two months, a month of it is 5,005
	got, err := billing.UnitsPrice(p, 1, 2)
	if err != nil || got != 501 {
		t.Fatalf("got %d, %v; want 501", got, err)
	}
}

func TestTheIncludedUnitsCostNothingAndZeroIsOne(t *testing.T) {
	t.Parallel()
	p := plan(5000, 1)
	p.UnitPrice = 1500
	if p.Included() != 1 {
		t.Fatalf("a plan with IncludedUnits zero includes %d", p.Included())
	}
	for _, units := range []int{0, 1} {
		if got, err := billing.UnitsPrice(p, 1, units); err != nil || got != 0 {
			t.Errorf("%d units: got %d, %v", units, got, err)
		}
	}
	p.IncludedUnits = 5
	if got, err := billing.UnitsPrice(p, 1, 5); err != nil || got != 0 {
		t.Errorf("5 units on a plan that includes 5: got %d, %v", got, err)
	}
	if got, err := billing.UnitsPrice(p, 1, 7); err != nil || got != 3000 {
		t.Errorf("7 units on a plan that includes 5: got %d, %v", got, err)
	}
}

func TestAPlanWithoutAUnitPriceRefusesUnitsBeyondTheIncludedOnes(t *testing.T) {
	t.Parallel()
	p := plan(5000, 1)
	for _, units := range []int{2, 10, billing.MaxUnits + 1, -1} {
		if _, err := billing.PriceUnits(p, 0, units, nil, false, at("2026-11-24 10:00")); !errors.Is(err, billing.ErrUnitsUnavailable) {
			t.Errorf("%d units: got %v, want ErrUnitsUnavailable", units, err)
		}
	}
	if _, err := billing.PriceUnits(teamPlan(5000, 100, 1), 0, billing.MaxUnits+1, nil, false, at("2026-11-24 10:00")); !errors.Is(err, billing.ErrUnitsUnavailable) {
		t.Errorf("more than MaxUnits: got %v", err)
	}
}

func TestPriceCycleIsPriceUnitsWithNoUnits(t *testing.T) {
	t.Parallel()
	p := teamPlan(11990, 2000, 1)
	p.Cycles = billing.CycleList{{Months: 3, Discount: 500}}
	for _, months := range []int{0, 1, 3} {
		a, errA := billing.PriceCycle(p, months, nil, false, at("2026-11-24 10:00"))
		b, errB := billing.PriceUnits(p, months, 0, nil, false, at("2026-11-24 10:00"))
		if errA != nil || errB != nil || a != b || a.UnitsAmount != 0 {
			t.Errorf("%d months: %+v (%v) against %+v (%v)", months, a, errA, b, errB)
		}
	}
}
