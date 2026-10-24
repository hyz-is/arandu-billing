---
name: billing-package
description: Install, wire and use Arandu Billing (plans, coupons, subscriptions, invoices) in an Arandu application. Use when the request is to "install Arandu Billing", "add plans and subscriptions to the app", "go get github.com/hyz-is/arandu-billing", "wire it into bootstrap/app.go", "plug our payment gateway in", "implement the Provider", "the gateway's notification answers 400/404/409", "everything under /billing returns 403", "it redirects to /auth/login", "the table does not exist", "the views are not registered", "deposit credits when a plan is paid", or when a project's go.mod already requires github.com/hyz-is/arandu-billing. Covers the wiring in bootstrap/app.go, the Provider the application implements, the roles, the routes, the Listener and the wallet adapter, and the migration and view steps that are not optional.
license: MIT
metadata:
  audience: app
---

# Using Arandu Billing

The module keeps the catalogue (plans, coupons), each account's subscriptions
and the date each one is paid until, and the invoices a gateway collects. It
ships no gateway: the application implements `billing.Provider` as a service of
its own and hands it to `New`.

## Install

```bash
go get github.com/hyz-is/arandu-billing@v0.1.0
```

## Wire it in bootstrap/app.go

```go
	gateway := payments.NewCheckout(cfg.Payments) // app/Services: the application's Provider
	billingModule, err := billing.New(billing.Config{
		Prefix: "/painel", AdminPrefix: "/admin/cobranca", Paths: billing.PortuguesePaths,
		Providers: []billing.Provider{gateway, billing.ManualProvider{Instructions: cfg.PixInstructions}},
		PublicURL: cfg.App.URL, Locale: "pt-BR", Activity: activityModule.Logger(),
		Middleware: []func(http.Handler) http.Handler{permissionModule.Roles()},
	}, db, sessions)
	if err != nil {
		return App{}, err
	}
```

and `billingModule,` inside the `k.Register(...)` call already there. `New`
refuses a configuration with no provider, two providers of one name, a nil
database or a nil session store, so a wiring mistake costs one restart.

Then, once:

```bash
aru migrate
aru vendor:publish --tag=view --apply
aru view:build
```

and import `storage/framework/views/modules/billing` and
`.../modules/billing/admin`. Without them `Boot` refuses and names them.

## The Provider is the application's

Four methods: `Name`, `CreateCheckout`, `ParseNotification`, `QueryStatus` (see
the README). Put it in `app/Services`, with the gateway's client and the
merchant credentials from the environment. Never add a gateway to the module
itself. The module believes a notification only after `QueryStatus` answers
`StatePaid` with the invoice's total, and confirms an invoice once.

The notification address is `POST NotificationPrefix + "/" + Name()`
(`/api/billing/notifications/<name>` by default), exempt from CSRF, with no
session: 200 handled, 400 not a notification, 404 no such invoice or provider,
409 an amount that is not the total, 500 sent again later.

## Who may do what

- A member of an account reads its plans, subscriptions and invoices.
- The account's manager -- `billing.AccountRole`, or the action
  `billing.account` in an arandu-permission group -- pays, starts a trial,
  renews and cancels at the period's end. A member without it gets 403 on
  those routes.
- The platform's admin -- `billing.AdminRole`, or the action `billing.admin` --
  keeps the catalogue and acts on every account.

Every route but the notification is behind `RequireAuth`: no session is a
redirect to `/auth/login` (or the application's own sign-in).

## Business values live in rows

Prices, months, trial and grace days, installments and coupon terms are plan
and coupon rows: seed them with the application's seeder (`aru db:seed`)
through `Service().CreatePlan` / `UpdatePlan`, or edit them on the admin's
screen. Never as constants in code.

## Reacting to billing

`Config.Listener` is told about `invoice.paid`, `subscription.activated`,
`renewed`, `past_due`, `expired`, `canceled` and `trial_started`, inside the
act's transaction; an error rolls the act back. For plans that sell credits,
`walletcredit.Listener(walletModule.Service(), walletcredit.Options{Next: ...})`
deposits the plan's `wallet_amount` into its `wallet_slug` once per invoice.

`Service().ServingSubscriptions(ctx, who)` is what a screen asks before opening
what a plan pays for.
