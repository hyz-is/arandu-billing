// Package billingtest is an in-memory payment gateway for tests: what an
// application's own suite, and this package's, check its billing against
// without any real gateway, network or merchant account.
//
// It answers a checkout with a page address of its own, reads a notification
// posted as a form ("order" names the invoice), and answers QueryStatus with
// what the test said happened: Pay, Fail, Refund, or nothing (pending). A
// notification is only a hint, exactly as with a real gateway; the status the
// test set is what confirms.
//
// It also plays a gateway that holds subscriptions of its own: Renew makes it
// charge the renewal of one, PayCharge pays that charge, and the requests
// ChargeNotification and StateNotification are what it would post about a
// charge and about a subscription that changed state.
package billingtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"

	billing "github.com/hyz-is/arandu-billing"
)

// Gateway is the in-memory gateway. The zero value is not usable; build one
// with New.
type Gateway struct {
	name string

	mu        sync.Mutex
	checkouts []billing.CheckoutRequest
	statuses  map[string]billing.PaymentStatus
	charges   map[string]charge
	queries   int
	failNext  error
}

// charge is a renewal the gateway made by itself.
type charge struct {
	subscription string
	units        int
}

var _ billing.Provider = (*Gateway)(nil)

// New is a gateway with the name given: "fake" when it is empty.
func New(name string) *Gateway {
	if name == "" {
		name = "fake"
	}
	return &Gateway{name: name, statuses: map[string]billing.PaymentStatus{}, charges: map[string]charge{}}
}

// Name is the gateway's name.
func (p *Gateway) Name() string { return p.name }

// CreateCheckout records the request and answers a page of its own.
func (p *Gateway) CreateCheckout(_ context.Context, req billing.CheckoutRequest) (billing.Checkout, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.failNext; err != nil {
		p.failNext = nil
		return billing.Checkout{}, err
	}
	p.checkouts = append(p.checkouts, req)
	return billing.Checkout{URL: "https://gateway.example/pay/" + req.InvoiceID, Reference: "ref-" + req.InvoiceID}, nil
}

// ParseNotification reads a form posted with "order" (the invoice) and,
// optionally, "reference" (the gateway's identifier of the charge),
// "subscription" (the gateway's identifier of the subscription), "units" and
// "state" (the state the subscription moved to). A form with "state" and a
// "subscription" is about the subscription alone.
func (p *Gateway) ParseNotification(r *http.Request) (billing.Notification, error) {
	if err := r.ParseForm(); err != nil {
		return billing.Notification{}, billing.ErrBadNotification
	}
	order := strings.TrimSpace(r.PostForm.Get("order"))
	reference := strings.TrimSpace(r.PostForm.Get("reference"))
	subscription := strings.TrimSpace(r.PostForm.Get("subscription"))
	state := billing.SubscriptionStatus(strings.TrimSpace(r.PostForm.Get("state")))
	if order == "" && reference == "" && (state == "" || subscription == "") {
		return billing.Notification{}, billing.ErrBadNotification
	}
	units, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("units")))
	return billing.Notification{
		InvoiceID: order, Reference: reference, SubscriptionRef: subscription, Units: units, SubscriptionState: state,
	}, nil
}

// QueryStatus answers what the test set for the invoice, pending otherwise.
func (p *Gateway) QueryStatus(_ context.Context, q billing.StatusQuery) (billing.PaymentStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queries++
	if st, ok := p.statuses[q.InvoiceID]; ok {
		return st, nil
	}
	if st, ok := p.statuses[q.Reference]; ok {
		return st, nil
	}
	return billing.PaymentStatus{State: billing.StatePending, Raw: "pending"}, nil
}

// Pay says the invoice was paid with the amount, in centavos, and the
// installments given.
func (p *Gateway) Pay(invoiceID string, amount int64, installments int) {
	p.set(invoiceID, billing.PaymentStatus{State: billing.StatePaid, Amount: amount, Method: "credit_card",
		Installments: installments, Reference: "pay-" + invoiceID, Raw: "paid"})
}

// PaySubscription is Pay for an invoice that belongs to a subscription the
// gateway holds: the payment answers the subscription's identifier.
func (p *Gateway) PaySubscription(invoiceID string, amount int64, installments int, subscriptionRef string) {
	p.set(invoiceID, billing.PaymentStatus{State: billing.StatePaid, Amount: amount, Method: "credit_card",
		Installments: installments, Reference: "pay-" + invoiceID, SubscriptionRef: subscriptionRef, Raw: "paid"})
}

// Renew makes the gateway charge the renewal of the subscription it holds under
// subscriptionRef, for units units (zero says it does not tell), and answers
// the charge's reference. The charge is pending until PayCharge says it was
// paid.
func (p *Gateway) Renew(subscriptionRef string, units int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	reference := "charge-" + strconv.Itoa(len(p.charges)+1) + "-" + subscriptionRef
	p.charges[reference] = charge{subscription: subscriptionRef, units: units}
	return reference
}

// PayCharge says the charge was paid with the amount, in centavos. The payment
// answers the identifier of the subscription the charge was made for.
func (p *Gateway) PayCharge(reference string, amount int64) {
	p.mu.Lock()
	c := p.charges[reference]
	p.mu.Unlock()
	p.set(reference, billing.PaymentStatus{State: billing.StatePaid, Amount: amount, Method: "credit_card",
		Installments: 1, Reference: reference, SubscriptionRef: c.subscription, Raw: "paid"})
}

// Fail says the invoice's payment was refused.
func (p *Gateway) Fail(invoiceID string) {
	p.set(invoiceID, billing.PaymentStatus{State: billing.StateFailed, Raw: "denied"})
}

// Refund says the invoice's payment was given back.
func (p *Gateway) Refund(invoiceID string) {
	p.set(invoiceID, billing.PaymentStatus{State: billing.StateRefunded, Raw: "refunded"})
}

// FailNextCheckout makes the next CreateCheckout answer err.
func (p *Gateway) FailNextCheckout(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failNext = err
}

func (p *Gateway) set(invoiceID string, st billing.PaymentStatus) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.statuses[invoiceID] = st
}

// Checkouts are the requests CreateCheckout was given, in order.
func (p *Gateway) Checkouts() []billing.CheckoutRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]billing.CheckoutRequest(nil), p.checkouts...)
}

// Queries is how many times QueryStatus was asked.
func (p *Gateway) Queries() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.queries
}

// ChargeNotification is the request the gateway would post about a charge made
// by Renew, to the address given: it names the charge, the subscription and the
// units, and no invoice.
func (p *Gateway) ChargeNotification(address, reference string) *http.Request {
	p.mu.Lock()
	c := p.charges[reference]
	p.mu.Unlock()
	form := url.Values{"reference": {reference}, "subscription": {c.subscription}}
	if c.units > 0 {
		form.Set("units", strconv.Itoa(c.units))
	}
	return post(address, form)
}

// StateNotification is the request the gateway would post when the
// subscription it holds under subscriptionRef moved to state, charging units
// units (zero says it does not tell), to the address given.
func (p *Gateway) StateNotification(address, subscriptionRef string, state billing.SubscriptionStatus, units int) *http.Request {
	form := url.Values{"subscription": {subscriptionRef}, "state": {string(state)}}
	if units > 0 {
		form.Set("units", strconv.Itoa(units))
	}
	return post(address, form)
}

// SubscriptionNotification is Notification for an invoice that belongs to a
// subscription the gateway holds: it names the subscription too.
func (p *Gateway) SubscriptionNotification(address, invoiceID, subscriptionRef string) *http.Request {
	return post(address, url.Values{"order": {invoiceID}, "subscription": {subscriptionRef}})
}

func post(address string, form url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, address, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}

// Notification is the request the gateway would post about an invoice, to the
// address given.
func (p *Gateway) Notification(address, invoiceID string) *http.Request {
	return post(address, url.Values{"order": {invoiceID}})
}
