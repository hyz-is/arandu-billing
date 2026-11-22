package billing

import (
	"errors"
	"time"
)

// What a checkout costs, and the rules that decide it. Everything here is
// pure: no database, no clock read inside, no Grant. The service loads the
// plan and the coupon, asks these functions, and saves what they answer.

// Quote is what one period of a plan costs in a billing cycle, with a coupon.
type Quote struct {
	// Subtotal is the plan's price for the cycle's months at its own rate,
	// before any discount, in centavos.
	Subtotal int64
	// CycleDiscount is what the billing cycle takes off Subtotal, in
	// centavos.
	CycleDiscount int64
	// Discount is what the coupon takes off the price the cycle left, in
	// centavos; never more than that price.
	Discount int64
	// Total is what is charged, in centavos: Subtotal minus CycleDiscount
	// minus Discount.
	Total int64
	// MaxInstallments is the most installments the period may be paid in.
	MaxInstallments int
	// CouponApplied says a coupon took part.
	CouponApplied bool
}

// The reasons a coupon is refused. Each one is a sentence of the catalogue
// ("validation.coupon_expired"), so a screen says why in the customer's
// language; the service turns them into validation.Errors on the field
// "coupon".
var (
	ErrCouponInactive    = errors.New("billing: the coupon is not active")
	ErrCouponNotStarted  = errors.New("billing: the coupon is not valid yet")
	ErrCouponExpired     = errors.New("billing: the coupon has expired")
	ErrCouponExhausted   = errors.New("billing: the coupon has no uses left")
	ErrCouponWrongPlan   = errors.New("billing: the coupon does not apply to this plan")
	ErrCouponMinPurchase = errors.New("billing: the price is below the coupon's minimum purchase")
)

// CheckCoupon says whether a coupon may be used now on a plan, for a first
// payment.
func CheckCoupon(c Coupon, p Plan, at time.Time) error {
	switch {
	case !bool(c.Active):
		return ErrCouponInactive
	case c.ValidFrom != nil && at.Before(*c.ValidFrom):
		return ErrCouponNotStarted
	case c.ValidUntil != nil && !at.Before(*c.ValidUntil):
		return ErrCouponExpired
	case c.MaxUses > 0 && c.Uses >= c.MaxUses:
		return ErrCouponExhausted
	case c.PlanID != "" && c.PlanID != p.ID:
		return ErrCouponWrongPlan
	case p.Price < c.MinPurchase:
		return ErrCouponMinPurchase
	}
	return nil
}

// DiscountOf is what a coupon takes off a price, in centavos: a percentage
// rounded down to the centavo, or a fixed amount, never more than the price
// and never less than zero.
func DiscountOf(c Coupon, price int64) int64 {
	if price <= 0 || c.Value <= 0 {
		return 0
	}
	var off int64
	switch c.Kind {
	case CouponPercent:
		value := c.Value
		if value > 10000 {
			value = 10000
		}
		off = price * value / 10000
	case CouponFixed:
		off = c.Value
	}
	if off > price {
		off = price
	}
	if off < 0 {
		off = 0
	}
	return off
}

// Price is what one period of the plan costs, with the coupon when one is
// given: checked with CheckCoupon for a first payment, taken as granted for a
// renewal the subscription still has coupon renewals for. It prices the plan's
// own IntervalMonths; PriceCycle prices another cycle.
func Price(p Plan, c *Coupon, renewal bool, at time.Time) (Quote, error) {
	return PriceCycle(p, p.IntervalMonths, c, renewal, at)
}

// CyclePrice is what months months of the plan cost: the plan's price at its
// own rate, scaled to the months and rounded half up to the centavo, and what
// the cycle's discount takes off it, rounded the same way. The months must be
// one the plan offers (Plan.Cycle), or the answer is ErrCycleUnavailable.
//
//	Price 10000, IntervalMonths 1, months 3, discount 500 -> 30000, 1500
func CyclePrice(p Plan, months int) (list, discount int64, err error) {
	cycle, err := p.Cycle(months)
	if err != nil {
		return 0, 0, err
	}
	interval := int64(p.IntervalMonths)
	if interval < 1 {
		interval = 1
	}
	list = roundDiv(p.Price*int64(cycle.Months), interval)
	discount = list - roundDiv(list*(10000-cycle.Discount), 10000)
	return list, discount, nil
}

// roundDiv is a divided by b, rounded half up; both are positive.
func roundDiv(a, b int64) int64 { return (a + b/2) / b }

// PriceCycle is Price for a payment of months months: the billing cycle's
// discount comes off first and the coupon takes its discount off what is
// left, so the coupon's minimum purchase is read against that price. Zero
// months is the plan's own IntervalMonths.
func PriceCycle(p Plan, months int, c *Coupon, renewal bool, at time.Time) (Quote, error) {
	if months == 0 {
		months = p.IntervalMonths
	}
	list, off, err := CyclePrice(p, months)
	if err != nil {
		return Quote{}, err
	}
	price := list - off
	q := Quote{Subtotal: list, CycleDiscount: off, Total: price, MaxInstallments: p.InstallmentsFor(months)}
	if c == nil {
		return q, nil
	}
	if !renewal {
		priced := p
		priced.Price = price
		if err := CheckCoupon(*c, priced, at); err != nil {
			return Quote{}, err
		}
	}
	q.Discount = DiscountOf(*c, price)
	q.Total = price - q.Discount
	q.CouponApplied = true
	return q, nil
}

// AddMonths moves an instant n months on, keeping the time of day and holding
// the day of the month where the target month has it: January 31 plus one
// month is the last day of February, not March 3.
func AddMonths(t time.Time, n int) time.Time {
	year, month, day := t.Date()
	first := time.Date(year, month+time.Month(n), 1, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
	last := first.AddDate(0, 1, -1).Day()
	if day > last {
		day = last
	}
	return time.Date(first.Year(), first.Month(), day, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), t.Location())
}

// Period is the stretch of time one payment buys.
type Period struct {
	// Start is when the period begins.
	Start time.Time
	// End is the instant the subscription is paid until.
	End time.Time
}

// NextPeriod is what a payment made at paidAt buys for the subscription, for
// months months:
//
//   - a subscription that does not exist yet, or that ended (canceled,
//     expired), starts again on the day of the payment;
//   - a trial paid for keeps its free days and the period follows them;
//   - an active subscription renewed early is extended from the day it is paid
//     until, so no day already paid for is lost;
//   - a subscription past due is extended from the day it was paid until, so
//     the days of grace it was served are the first days of the new period --
//     unless even that end would already be behind the payment, and then the
//     period starts at the payment;
//   - a trial or an active subscription whose end the sweep has not reached
//     yet, though it is behind the payment, starts again at the payment.
func NextPeriod(s *Subscription, paidAt time.Time, months int) Period {
	fresh := Period{Start: paidAt, End: AddMonths(paidAt, months)}
	if s == nil || s.Ended() {
		return fresh
	}
	end := s.CurrentPeriodEnd
	switch s.Status {
	case SubscriptionTrialing:
		if !end.After(paidAt) {
			return fresh
		}
		return Period{Start: paidAt, End: AddMonths(end, months)}
	case SubscriptionActive:
		if !end.After(paidAt) {
			return fresh
		}
		return Period{Start: s.CurrentPeriodStart, End: AddMonths(end, months)}
	case SubscriptionPastDue:
		next := AddMonths(end, months)
		if !next.After(paidAt) {
			return fresh
		}
		return Period{Start: end, End: next}
	}
	return fresh
}

// Transition is what the clock does to a subscription at an instant: the
// status it moves to, and whether it moved.
type Transition struct {
	// To is the status after the instant.
	To SubscriptionStatus
	// Moved says To differs from where the subscription stood.
	Moved bool
}

// Due is what happens to a subscription at now, with nobody paying:
//
//   - a trial or a period that reached its end with a cancellation asked for
//     is canceled;
//   - a trial that reached its end without a payment expires;
//   - a period that reached its end goes past due when the subscription has
//     grace days, and expires when it has none;
//   - a subscription past due for longer than its grace expires.
//
// Anything else stays where it is.
func Due(s Subscription, now time.Time) Transition {
	stay := Transition{To: s.Status}
	reached := !now.Before(s.CurrentPeriodEnd)
	switch s.Status {
	case SubscriptionTrialing:
		if !reached {
			return stay
		}
		if s.CancelAtPeriodEnd {
			return Transition{To: SubscriptionCanceled, Moved: true}
		}
		return Transition{To: SubscriptionExpired, Moved: true}
	case SubscriptionActive:
		if !reached {
			return stay
		}
		if s.CancelAtPeriodEnd {
			return Transition{To: SubscriptionCanceled, Moved: true}
		}
		if s.GraceDays > 0 && now.Before(s.CurrentPeriodEnd.AddDate(0, 0, s.GraceDays)) {
			return Transition{To: SubscriptionPastDue, Moved: true}
		}
		return Transition{To: SubscriptionExpired, Moved: true}
	case SubscriptionPastDue:
		if s.CancelAtPeriodEnd {
			return Transition{To: SubscriptionCanceled, Moved: true}
		}
		if !now.Before(s.CurrentPeriodEnd.AddDate(0, 0, s.GraceDays)) {
			return Transition{To: SubscriptionExpired, Moved: true}
		}
	}
	return stay
}
