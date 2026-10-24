// Package billingtest is an in-memory payment gateway for tests: what an
// application's own suite, and this package's, check its billing against
// without any real gateway, network or merchant account.
//
// It answers a checkout with a page address of its own, reads a notification
// posted as a form ("order" names the invoice), and answers QueryStatus with
// what the test said happened: Pay, Fail, Refund, or nothing (pending). A
// notification is only a hint, exactly as with a real gateway; the status the
// test set is what confirms.
package billingtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	queries   int
	failNext  error
}

var _ billing.Provider = (*Gateway)(nil)

// New is a gateway with the name given: "fake" when it is empty.
func New(name string) *Gateway {
	if name == "" {
		name = "fake"
	}
	return &Gateway{name: name, statuses: map[string]billing.PaymentStatus{}}
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
// optionally, "reference".
func (p *Gateway) ParseNotification(r *http.Request) (billing.Notification, error) {
	if err := r.ParseForm(); err != nil {
		return billing.Notification{}, billing.ErrBadNotification
	}
	order := strings.TrimSpace(r.PostForm.Get("order"))
	reference := strings.TrimSpace(r.PostForm.Get("reference"))
	if order == "" && reference == "" {
		return billing.Notification{}, billing.ErrBadNotification
	}
	return billing.Notification{InvoiceID: order, Reference: reference}, nil
}

// QueryStatus answers what the test set for the invoice, pending otherwise.
func (p *Gateway) QueryStatus(_ context.Context, q billing.StatusQuery) (billing.PaymentStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queries++
	if st, ok := p.statuses[q.InvoiceID]; ok {
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

// Notification is the request the gateway would post about an invoice, to the
// address given.
func (p *Gateway) Notification(address, invoiceID string) *http.Request {
	form := url.Values{"order": {invoiceID}}
	r := httptest.NewRequest(http.MethodPost, address, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return r
}
