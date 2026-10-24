<p align="center">
  <img src=".github/logo.svg" alt="Arandu" width="180">
</p>

# Arandu Billing

Billing management for an Arandu application: a catalogue of plans and
coupons, the subscriptions of each account and the date each one is paid
until, and the invoices a payment gateway collects -- with the admin's screens
and the customer's, in English and Brazilian Portuguese.

It ships **no payment gateway**. The application implements the `Provider`
interface as a service of its own, over its gateway's client and its own
merchant account, and hands it to `New`. The package asks it for a checkout
page, believes a notification only after asking it again, and confirms each
invoice once. Balances are not kept here: a plan that sells credits deposits
them in [arandu-wallet](https://github.com/hyz-is/arandu-wallet) through the
optional `walletcredit` listener.

## Install

```bash
go get github.com/hyz-is/arandu-billing@v0.1.0
```

## Wire it

An Arandu application registers a module explicitly, in `bootstrap/app.go`.
These are the lines, and there are no others.

The imports:

```go
import (
	billing "github.com/hyz-is/arandu-billing"

	"your/module/path/app/Services/payments"
)
```

The construction, in `Build`, after the session store, the activity log and
the gateway service exist and before `k.Register`:

```go
	gateway, err := payments.NewCheckout(cfg.Payments) // the application's own Provider
	if err != nil {
		return App{}, err
	}
	billingModule, err := billing.New(billing.Config{
		Prefix:      "/painel",
		AdminPrefix: "/admin/cobranca",
		Paths:       billing.PortuguesePaths,
		Providers:   []billing.Provider{gateway, billing.ManualProvider{Instructions: cfg.PixInstructions}},
		PublicURL:   cfg.App.URL,
		Locale:      "pt-BR",
		Activity:    activityModule.Logger(),
		Listener:    onBilling, // optional: the application's reaction to each event
		Customer:    customerOf, // optional: who pays, for the gateway's page
		Middleware:  []func(http.Handler) http.Handler{permissionModule.Roles()},
	}, db, sessions)
	if err != nil {
		return App{}, err
	}
```

And the registration, inside the `k.Register(...)` call already there:

```go
		billingModule,
```

Then, once, before the application serves:

```bash
aru migrate
aru vendor:publish --tag=view --apply
aru view:build
```

and import the compiled views beside the other imports:

```go
	_ "your/module/path/storage/framework/views/modules/billing"
	_ "your/module/path/storage/framework/views/modules/billing/admin"
```

Without them the module refuses to boot, and the refusal names the views, the
command and the imports. An application that mounts the account's routes in
its customer panel and the admin's in its operator console, each with its own
guards, calls `billingModule.AccountRoutes(panel)`,
`billingModule.AdminRoutes(console)` and `billingModule.NotificationRoutes(api)`
instead of registering the module's `Routes` whole.

## Implement a Provider

A gateway is four methods, written in the application:

```go
// app/Services/payments/checkout.go
type Checkout struct{ client *gatewayclient.Client }

func (c *Checkout) Name() string { return "gateway" } // the last segment of its notification address

// CreateCheckout asks the gateway for a payment page for one invoice.
func (c *Checkout) CreateCheckout(ctx context.Context, req billing.CheckoutRequest) (billing.Checkout, error) {
	order, err := c.client.CreateOrder(ctx, gatewayclient.Order{
		Number: req.InvoiceID, Description: req.Description, Amount: req.Total,
		MaxInstallments: req.MaxInstallments, ReturnURL: req.ReturnURL,
	})
	if err != nil {
		return billing.Checkout{}, err
	}
	return billing.Checkout{URL: order.PaymentURL, Reference: order.ID}, nil
}

// ParseNotification reads what the gateway posted. Nothing in it is trusted:
// it only names the order.
func (c *Checkout) ParseNotification(r *http.Request) (billing.Notification, error) {
	if err := r.ParseForm(); err != nil || r.PostForm.Get("order_number") == "" {
		return billing.Notification{}, billing.ErrBadNotification
	}
	return billing.Notification{InvoiceID: r.PostForm.Get("order_number"), Reference: r.PostForm.Get("checkout_id")}, nil
}

// QueryStatus asks the gateway itself where the payment stands.
func (c *Checkout) QueryStatus(ctx context.Context, q billing.StatusQuery) (billing.PaymentStatus, error) {
	p, err := c.client.Payment(ctx, q.InvoiceID)
	if err != nil {
		return billing.PaymentStatus{}, err
	}
	state := billing.StatePending
	switch {
	case p.Paid:
		state = billing.StatePaid
	case p.Denied:
		state = billing.StateFailed
	}
	return billing.PaymentStatus{State: state, Amount: p.Amount, Method: p.Method,
		Installments: p.Installments, Reference: p.ID, PaidAt: p.PaidAt, Raw: p.Status}, nil
}
```

What the package does with the answers is the contract:

- `CreateCheckout` is called once per invoice, after the invoice is saved,
  with the amount computed here from the catalogue. The customer is sent to
  `Checkout.URL`; an empty URL shows the invoice page, where a provider that
  implements `PaymentInstructions() string` tells the customer how to pay.
- `ParseNotification` runs on `POST NotificationPrefix + "/" + Name()`, with no
  session and no CSRF token. Its answer only names the invoice (by
  `InvoiceID`, the order number the checkout was given, or by the gateway's
  `Reference`); a request it does not understand wraps `ErrBadNotification` and
  the address answers 400.
- `QueryStatus` is asked before anything is confirmed. Only `StatePaid` with
  `Amount` equal to the invoice's total confirms it; another amount answers 409
  and is kept on the invoice for the admin. `StateFailed` marks the invoice
  failed.
- Confirmation is idempotent: the invoice is claimed by a conditional update,
  and the unique `invoice_id` of the payments holds the same line in the
  database, so one invoice is paid once however many notifications arrive, in
  whatever order, racing or not. A repeated notification answers 200.

The customer's return address (`PublicURL` + `/faturas/{id}/retorno` with the
Portuguese paths) asks the gateway again too, so a payment counts even before
the notification arrives.

`ManualProvider` is the gateway-free one: no page and no notification; the
invoice page shows its `Instructions` and the admin confirms the payment on
the admin's invoices screen. For tests, `billingtest.New("fake")` is an
in-memory gateway: `Pay`, `Fail` and `Refund` say what happened, and
`Notification` builds the post.

## Configuration

| field | required | meaning |
| --- | --- | --- |
| `Providers` | yes | the gateways, the application's own; the first is the default |
| `DefaultProvider` | no | the `Name` of the provider a checkout uses |
| `Prefix` | no | where the account's routes are mounted. Defaults to `/billing` |
| `AdminPrefix` | no | where the admin's routes are mounted. Defaults to `Prefix + /admin` |
| `Paths` | no | the segments of every route; `PortuguesePaths` is the Brazilian set |
| `NotificationPrefix` | no | where the gateways post. Defaults to `/api/billing/notifications` |
| `PageSize` | no | rows per page, 25 by default, refused above 200 |
| `Listener` | no | told about each event, inside the act's transaction |
| `Customer` | no | who pays, for the gateway's page |
| `PublicURL` | no | the application's address, for the return address |
| `Location` | no | the timezone dates are shown in. Defaults to `America/Sao_Paulo` |
| `Activity` | no | arandu-activitylog's logger: every act is recorded under its Grant |
| `Translator`, `Locale`, `TitleSeparator` | no | the sentences and the document title |
| `Middleware`, `AdminMiddleware`, `Chrome` | no | the application's guards and layout |
| `Clock` | no | the time, for a test that holds it still |

There is no tenant to set and there are no prices to set: the tenant of every
statement is the one on the Grant, and every number a business person changes
is a plan's or a coupon's row, written by the application's seeder or on the
admin's screen.

## Routes

With `PortuguesePaths`, the account's side under `Prefix`:

| method | path | name |
| --- | --- | --- |
| `GET` | `/planos` | `billing.plans.index` |
| `POST` | `/planos/{id}/assinar` | `billing.plans.checkout` |
| `POST` | `/planos/{id}/teste-gratis` | `billing.plans.trial` |
| `GET` | `/assinaturas` | `billing.subscriptions.index` |
| `POST` | `/assinaturas/{id}/renovacao` | `billing.subscriptions.renew` |
| `POST` | `/assinaturas/{id}/cancelamento` | `billing.subscriptions.cancel` |
| `DELETE` | `/assinaturas/{id}/cancelamento` | `billing.subscriptions.resume` |
| `GET` | `/faturas` | `billing.invoices.index` |
| `GET` | `/faturas/{id}` | `billing.invoices.show` |
| `GET` | `/faturas/{id}/retorno` | `billing.invoices.return` |

the admin's under `AdminPrefix`: `GET`/`POST /planos`, `GET`/`POST
/planos/{id}`, `GET`/`POST /cupons`, `POST /cupons/{id}`, `GET /assinaturas`,
`POST /assinaturas/{id}/cancelamento`, `GET /faturas`, `POST
/faturas/{id}/pagamento` and `POST /faturas/{id}/cancelamento`; and `POST
NotificationPrefix/{provider}`, `billing.notifications`.

Every route but the notification sits behind `RequireAuth`: a visitor with no
session is sent to `/auth/login` (or wherever the application's guard sends
one), and a handler reads who is asking with `ctx.User()`. A client that asks
for JSON gets JSON from every route.

## Who may do what

| subject | how the application says so | may |
| --- | --- | --- |
| a member of an account | signed in to that account | read the plans, the account's subscriptions and invoices |
| the account's manager | `billing.AccountRole`, or the action `billing.account` through arandu-permission | pay for a plan, start a trial, renew, cancel at the period's end |
| the platform's admin | `billing.AdminRole`, or the action `billing.admin` | keep the plans and coupons, read every account's subscriptions and invoices, confirm a payment by hand, void an invoice, cancel a subscription at once |

A subject inside somebody else's account through arandu-impersonate is never
the platform's admin there: it acts as the account. A gateway's notification
and the clock's sweep run as this package's own system subject, inside the
invoice's or the subscription's own account.

## The rules

- **Price.** A plan's `Price` is in centavos and buys `IntervalMonths`. A
  percent coupon takes `Value` basis points off, rounded down to the centavo; a
  fixed one takes `Value` centavos, never more than the price. A coupon is
  refused when it is inactive, not yet valid, expired, out of uses, for another
  plan or above the price (`MinPurchase`). A price taken to zero is confirmed at
  once, with no gateway.
- **Recurring coupons.** `Renewals` is how many renewals after the first
  payment keep the discount; a renewal keeps it even if the coupon was
  deactivated afterwards. `MaxUses` counts first payments.
- **Installments.** A plan offers installments from `InstallmentsFrom` months
  on, one per month of the period, never above `InstallmentsCap` or 12; every
  checkout tells the gateway its `MaxInstallments`.
- **Periods.** A first payment buys `[paid, paid + months)`; an early renewal is
  added to the day already paid until; a payment during the grace (`GraceDays`)
  extends from the day the period ended; a subscription that ended starts again
  on the day it is paid. One month after January 31 is the last day of
  February.
- **The clock.** `Schedule` runs `SweepDue` every fifteen minutes: a period that
  ended goes past due inside the grace and expires after it; a subscription
  whose cancellation was asked for is canceled at the end of its period; a
  trial that ended without a payment expires.

## Events

`Config.Listener` is told, inside the transaction of the act, about
`subscription.trial_started`, `invoice.paid`, `subscription.activated`,
`subscription.renewed`, `subscription.past_due`, `subscription.expired` and
`subscription.canceled`. Each `Event` carries the subscription, the plan with
its `Meta`, the invoice when there is one, and the Grant scoped to the
account. A listener's error rolls the act back, and a gateway's notification
is then answered with an error and sent again.

A plan that sells credits writes `wallet_slug` and `wallet_amount` in its
`Meta`, and the application wraps its listener:

```go
	Listener: walletcredit.Listener(walletModule.Service(), walletcredit.Options{Next: onBilling}),
```

The deposit runs as the wallet's operator of the invoice's own account, under
the idempotency key `billing:invoice:<id>`.

## From the application's own code

`billingModule.Service()` is the use cases, each asking the policy:
`ServingSubscriptions(ctx, who)` is what a screen asks before opening what a
plan pays for; `SubscriptionsOf(ctx, g)` answers the same for a Grant the
application's own policy issued for an account (a job, a sync);
`Subscribe(ctx, who, billing.SubscribeRequest{PlanID, Reference: "camera:42"})`
starts a checkout for something inside the account, named by the application.

## The data path

Each entity embeds `model.Model`, and its table is declared once in
`model.go`: `planTable`, `couponTable`, `subscriptionTable`,
`invoiceTable`, `paymentTable` and `couponUsageTable`. `aru model:build`
generates `Plans(db)` and its `PlanQuery` into `PlanQuery.go`, and the same for
the others; the files are committed, never edited by hand, and `aru
model:build --check` fails when one is stale. `BillingService` owns the
`*data.DB` and follows `validate -> security.Authorize -> Grant -> Model
terminal`; handlers never hold the database. The lists of every account read
`internal/everytenant`, Global read models no application can import, after
the policy allowed the admin.

## Migrations

`20261023_0001_create_billing_plans`, `20261023_0002_create_billing_coupons`,
`20261023_0003_create_billing_subscriptions`,
`20261023_0004_create_billing_invoices`, `20261023_0005_create_billing_payments`
and `20261023_0006_create_billing_coupon_usages`, applied by `aru migrate`,
never at boot.

## Tests

```bash
GOWORK=off go test -race -p 2 ./...
ARANDU_TEST_POSTGRES_DSN="postgres://localhost:5432/arandu_billing_test?sslmode=disable" GOWORK=off go test -race -p 2 ./tests/Feature/
```

## Licence

MIT. See [LICENSE.md](LICENSE.md). Copyright Paulo R. Lima.
