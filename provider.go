package billing

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Provider is a payment gateway, as this package uses one.
//
// The package ships no gateway. Each application writes its own as a service of
// its own -- an app/Services type over the gateway's client -- and hands it to
// New in Config.Providers. That keeps the gateway's credentials, its client and
// its contract in the application that holds the merchant account, and keeps
// every application that installs this package free of gateways it does not
// use: a gateway built in here could never be removed without breaking them.
//
// The package's side of the contract is what it does with the answers:
//
//   - CreateCheckout is called once per invoice, after the invoice is saved,
//     with the amount this package computed from the catalogue. The customer is
//     sent to Checkout.URL; an empty URL shows the invoice page instead.
//   - ParseNotification reads what the gateway posted. Nothing in it is
//     trusted: it only names the invoice (by its ID, the order number the
//     checkout was given) or the gateway's own reference.
//   - QueryStatus is asked before anything is confirmed, with the invoice as
//     this package has it, so a forged notification confirms nothing. Only
//     StatePaid with the invoice's Total confirms.
//   - Confirmation is idempotent: an invoice is paid once, however many
//     notifications arrive and in whatever order, and a repeated one answers
//     success without doing anything again.
type Provider interface {
	// Name is the provider's stable name: what Invoice.Provider records, and
	// the last segment of its notification address. Lowercase letters,
	// digits, - and _.
	Name() string

	// CreateCheckout asks the gateway for a payment page for one invoice.
	CreateCheckout(ctx context.Context, req CheckoutRequest) (Checkout, error)

	// ParseNotification reads the request the gateway posted to the
	// notification address. A request it does not recognise returns an error
	// that wraps ErrBadNotification, and the address answers 400.
	ParseNotification(r *http.Request) (Notification, error)

	// QueryStatus asks the gateway where the payment of one invoice stands.
	QueryStatus(ctx context.Context, q StatusQuery) (PaymentStatus, error)
}

// CheckoutRequest is what a Provider is asked to collect.
type CheckoutRequest struct {
	// InvoiceID is the order number: what the notification will name.
	InvoiceID string
	// Tenant is the account that pays.
	Tenant string
	// Description is the line the customer reads on the gateway's page: the
	// plan's name and its months.
	Description string
	// PlanID and PlanSlug are the plan charged for.
	PlanID, PlanSlug string
	// Months is how many months the payment buys.
	Months int
	// Subtotal, Discount and Total are in centavos of BRL; Total is what is
	// charged.
	Subtotal, Discount, Total int64
	// CouponCode is the coupon applied, empty for none.
	CouponCode string
	// MaxInstallments is the most installments the page may offer.
	MaxInstallments int
	// Customer is who pays, as the application filled it in.
	Customer Customer
	// ReturnURL is where the gateway sends the customer back, when the
	// application configured one.
	ReturnURL string
	// Meta is what the consumer attached to the checkout and the plan.
	Meta Meta
}

// Customer is who pays, filled in by the application through
// Config.Customer; every field may be empty.
type Customer struct {
	// Name is the person's or the company's name.
	Name string
	// Email is where the gateway writes to.
	Email string
	// Document is the CPF or the CNPJ, digits only.
	Document string
	// Phone is a telephone with area code, digits only.
	Phone string
}

// Checkout is what a Provider answered for one invoice.
type Checkout struct {
	// URL is the gateway's payment page. Empty sends the customer to the
	// invoice page of this package, where the application's instructions are.
	URL string
	// Reference is the gateway's identifier of the order, empty when it gives
	// none until it is paid.
	Reference string
}

// Notification is what a gateway posted, as its Provider read it.
type Notification struct {
	// InvoiceID is the order number the checkout was given. Either it or
	// Reference is required.
	InvoiceID string
	// Reference is the gateway's own identifier of the order or payment.
	Reference string
	// Fields are whatever else the Provider wants QueryStatus to see.
	Fields map[string]string
}

// StatusQuery is what QueryStatus is asked about.
type StatusQuery struct {
	// InvoiceID is the order number.
	InvoiceID string
	// Reference is the gateway's identifier, the one Checkout or a
	// notification gave, empty when there is none yet.
	Reference string
	// Total is the amount the invoice charges, in centavos.
	Total int64
	// Notification is what was posted, when the query follows one.
	Notification *Notification
}

// PaymentState is where a gateway says a payment stands.
type PaymentState string

const (
	// StatePending is not paid yet.
	StatePending PaymentState = "pending"
	// StatePaid is paid; it confirms the invoice when Amount is its Total.
	StatePaid PaymentState = "paid"
	// StateFailed was refused or abandoned; the invoice is marked failed.
	StateFailed PaymentState = "failed"
	// StateRefunded was paid and given back. An invoice already paid is left
	// as it is and the state is recorded; refunds are the admin's to handle.
	StateRefunded PaymentState = "refunded"
)

// PaymentStatus is a gateway's answer about one payment.
type PaymentStatus struct {
	// State is where the payment stands.
	State PaymentState
	// Amount is what was paid, in centavos.
	Amount int64
	// Method is how: "credit_card", "debit_card", "pix", "boleto".
	Method string
	// Installments is how many installments the customer chose.
	Installments int
	// Reference is the gateway's identifier of the payment.
	Reference string
	// PaidAt is when it was paid; zero is now.
	PaidAt time.Time
	// Raw is the gateway's own word for the state, recorded on the invoice.
	Raw string
}

// ErrBadNotification is what a Provider wraps when a posted request is not a
// notification it understands.
var ErrBadNotification = errors.New("billing: the request is not a notification this provider understands")

// ErrAmountMismatch is the refusal of a payment whose amount is not the
// invoice's Total: nothing is confirmed, and the gateway's answer is kept on
// the invoice for the admin.
var ErrAmountMismatch = errors.New("billing: the amount paid is not the invoice's total")

// ManualProviderName is the name of ManualProvider, and of the payments the
// admin confirms by hand.
const ManualProviderName = "manual"

// ManualProvider is the gateway-free provider: no page, no notification. The
// checkout sends the customer to the invoice page, where the application's
// Instructions say how to pay -- a Pix key, a bank transfer -- and the admin
// confirms the payment by hand on the admin's invoices screen.
type ManualProvider struct {
	// Instructions is what the invoice page tells the customer to do, in the
	// application's words. Empty draws the catalogue's general sentence.
	Instructions string
}

// Name is ManualProviderName.
func (ManualProvider) Name() string { return ManualProviderName }

// CreateCheckout answers no page: the customer is shown the invoice.
func (ManualProvider) CreateCheckout(context.Context, CheckoutRequest) (Checkout, error) {
	return Checkout{}, nil
}

// ParseNotification refuses every request: a manual payment is confirmed by
// the admin, never posted.
func (ManualProvider) ParseNotification(*http.Request) (Notification, error) {
	return Notification{}, ErrBadNotification
}

// QueryStatus answers pending: only the admin knows a manual payment arrived.
func (ManualProvider) QueryStatus(context.Context, StatusQuery) (PaymentStatus, error) {
	return PaymentStatus{State: StatePending}, nil
}

// instructor is a provider with something to tell the customer on the invoice
// page. A gateway of the application's may implement it too.
type instructor interface{ PaymentInstructions() string }

// PaymentInstructions is the text the invoice page shows.
func (p ManualProvider) PaymentInstructions() string { return p.Instructions }

var _ Provider = ManualProvider{}
