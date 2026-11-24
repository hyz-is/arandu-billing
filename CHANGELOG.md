# Changelog

Everything worth knowing about a release of Arandu Billing is recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A published module version is immutable: Go serves it from the proxy forever, so
a release is corrected by another release and never by moving a tag.

## [Unreleased]

## [0.4.0] - 2026-11-24

### Added

- Units (seats, members). A plan may include a number of units in its price and
  sell more at a price each: `Plan.IncludedUnits` (zero is one) and
  `Plan.UnitPrice` (centavos per unit above the included ones, for one
  `IntervalMonths`; zero sells none, and units above the included ones are
  refused). `PlanRequest` carries both, validated, and the admin's plan form has
  the `included_units` and `unit_price_brl` fields, in en and pt-BR.
- The pure rules `PriceUnits`, `UnitsPrice`, `Plan.Included`, `MaxUnits` (1000)
  and `ErrUnitsUnavailable`. The units cost
  `max(units - included, 0) * UnitPrice * months / IntervalMonths`, rounded half
  up to the centavo, and join the price for the months in `Quote.Subtotal`; the
  cycle's discount and the coupon take their discounts off that subtotal.
  `Quote.UnitsAmount` is the part the units cost. `Price` and `PriceCycle` keep
  their signatures and price no units.
- `Subscription.Units`, `Invoice.Units`, `Invoice.UnitsAmount`,
  `SubscribeRequest.Units`, `CheckoutRequest.Units` and
  `CheckoutRequest.UnitsAmount`; `Renew` and `RenewCycle` invoice the units the
  subscription has. The resources and their JSON carry the new fields.
- `BillingService.SetUnits` changes the units of one of the account's
  subscriptions from its next invoice on, between 1 and `MaxUnits`. It issues no
  invoice and collects nothing, and records `subscription.units_changed` with
  the units before and after.
- The migration `20261124_0008_add_billing_units`: `included_units` and
  `unit_price` on the plans, `units` on the subscriptions, `units` and
  `units_amount` on the invoices, every existing row keeping its price. Run
  `aru migrate`.

- Subscriptions a gateway holds and renews by itself. `Subscription.Provider`
  and `Subscription.ProviderRef` (unique together when not empty) are filled
  when an invoice is confirmed with `PaymentStatus.SubscriptionRef` or
  `Notification.SubscriptionRef`. `Notification` gains `SubscriptionRef`,
  `Units` and `SubscriptionState`; `PaymentStatus` gains `SubscriptionRef`.
- `HandleNotification` also handles, in this order, a notification about a
  subscription's state (`SubscriptionState` set and no invoice named: past_due,
  expired, canceled, or active while the period is ahead, once, answering a nil
  invoice), the invoice a notification names, and the renewal a gateway charged
  by itself (no invoice has the `Reference`, but a subscription holds the
  `SubscriptionRef`): the invoice is issued without a checkout, for the
  subscription's cycle and units, and confirmed through `QueryStatus` against
  its total. A charge repeated or racing is invoiced once, recorded under
  `Invoice.ChargeRef`, and a subscription already paid for more than half a
  cycle ahead is not invoiced again. The notification address answers 200 with
  `{"status":"ok"}` when there is no invoice to answer.
- `BillingService.SetProviderUnits`, `SetUnits` for the system when the gateway
  reports the units.
- The action `RenewalsRecord`, answered for this package's system subject alone.
- `billingtest.Gateway` plays a gateway with subscriptions: `Renew`,
  `PayCharge`, `PaySubscription`, `ChargeNotification`, `StateNotification` and
  `SubscriptionNotification`.
- The migration `20261124_0009_add_gateway_subscriptions`: `provider` and
  `provider_ref` on the subscriptions and `charge_ref` on the invoices, with a
  partial unique index on `(provider, provider_ref)` and on `(provider,
  charge_ref)` where the identifier is not empty. Run `aru migrate`.

### Changed

- A subscription moved by a gateway's notice back to active has its end,
  cancellation and cancel-at-period-end cleared and raises
  `subscription.activated`.
- The generated queries are regenerated with the current model generator; they
  gain `ApplyListing` and `Listing` and lose nothing.

## [0.3.0] - 2026-11-22

### Added

- Billing cycles. A plan may declare the cycles a subscriber can choose besides
  its own `IntervalMonths`, each with a discount in basis points
  (`Plan.Cycles`, a `CycleList` of `Cycle{Months, Discount}`; at most
  `MaxCycles`, a discount of at most `MaxCycleDiscount`). The invoice charges
  the plan's price for the cycle's months, `price x months / IntervalMonths`,
  less the cycle's discount, each rounded half up to the centavo, and the
  subscription is paid for that many months. A plan with no cycles behaves as it
  always did.
- `SubscribeRequest.CycleMonths` chooses the cycle at checkout; zero is the
  plan's own `IntervalMonths`. A cycle the plan does not offer is refused with a
  validation error on `cycle` (`ErrCycleUnavailable` from the pure rules) and
  nothing is issued.
- `BillingService.RenewCycle` renews in a cycle chosen now. A change of cycle
  applies to the invoice being issued and, once it is paid, to every renewal
  after it; the period already paid for is never changed. `Renew` keeps the
  subscription's own cycle, and a cycle the plan stopped offering is refused
  until another is chosen.
- `Subscription.CycleMonths` (the cycle renewals are invoiced in; zero follows
  the plan), `Invoice.CycleDiscount`, `Quote.CycleDiscount` and
  `CheckoutRequest.CycleDiscount`. `Invoice.Subtotal` is the price for the
  invoice's months before any discount and `Total` is `Subtotal -
  CycleDiscount - Discount`; the coupon takes its discount off the price the
  cycle left, and its minimum purchase is read against that price.
- The pure rules `PriceCycle`, `CyclePrice`, `Plan.Cycle`, `Plan.OfferedCycles`,
  `Plan.InstallmentsFor` and `Subscription.CycleMonthsFor`; `Price` is
  `PriceCycle` at the plan's own interval.
- The admin's plan form has a `cycles` field of `months=percent` lines
  (`3=5`, `12=17,5`), and the account's plans screen draws a cycle selector on a
  plan that sells more than one. `POST /planos/{id}/assinar` and `POST
  /assinaturas/{id}/pagamento` read an optional `cycle` field.
- The activity entries of an invoice issued and paid record the cycle
  (`cycle`, `cycle_discount`), and the plan entries record its cycles.
- The migration `20261122_0007_add_billing_cycles`: `cycles` on the plans,
  `cycle_months` on the subscriptions and `cycle_discount` on the invoices,
  every existing row keeping the plan's own interval. Run `aru migrate`.

## [0.2.1] - 2026-11-15

### Changed

- The six lists are drawn with Kyse's server-side data table: the views hand it
  a `State` (`TableState.State`) and a `Paginator` (`TableState.Pager`) instead of
  a query, a sort and the page numbers, and the columns a reader switched off
  survive a search, an order and a page through the `hide` parameter. A column
  that started hidden starts shown. `Screen.StatusFilter` no longer takes the
  status chosen. Requires Kyse v0.53.0.
- The `table` block of the catalogue names the sentences of the new table, in
  `en` and `pt-BR`; `table.showing`, `table.per_page` and `table.page` are gone.

## [0.1.0] - 2026-10-23

### Added

- The catalogue: plans (`Plan`: name, description, features, price in
  centavos of BRL, months per payment, trial and grace days, the installments
  policy -- from `InstallmentsFrom` months, one per month, capped at
  `InstallmentsCap` and `MaxInstallments` -- the consumer's `Meta`, active,
  position) and coupons (`Coupon`: percent or fixed, maximum uses, validity,
  `Renewals` that keep the discount, minimum purchase, an optional plan),
  Global tables kept by the platform's admin.
- Subscriptions per account and reference (`Subscription`: trialing, active,
  past_due, canceled, expired; the date it is paid until; cancellation at the
  period's end), invoices (`Invoice`), payments (`Payment`, one per invoice)
  and coupon usages (`CouponUsage`), scoped by the account's tenant.
- The pure rules: `Price`, `CheckCoupon`, `DiscountOf`, `Plan.Installments`,
  `AddMonths`, `NextPeriod` and `Due`.
- `Provider`, the gateway contract an application implements as a service of
  its own (`CreateCheckout`, `ParseNotification`, `QueryStatus`); a
  notification is believed only after `QueryStatus` answers `StatePaid` with
  the invoice's total, and an invoice is confirmed once, claimed by a
  conditional update and held by a unique index. `ManualProvider` is the
  gateway-free provider; no gateway ships in the module.
- `billingtest.Gateway`, an in-memory gateway for tests, and `walletcredit`,
  the optional listener that deposits a paid plan's credits in arandu-wallet
  once per invoice.
- `BillingService`: `ActivePlans`, `Plan`, `PlanBySlug`, `CreatePlan`,
  `UpdatePlan`, `CreateCoupon`, `UpdateCoupon`, `SetCouponActive`,
  `Subscribe`, `Renew`, `StartTrial`, `CancelAtPeriodEnd`,
  `ResumeSubscription`, `CancelNow`, `SweepDue`, `ServingSubscriptions`,
  `SubscriptionsOf`, `HandleNotification`, `RefreshInvoice`,
  `ConfirmManually`, `VoidInvoice`, `Invoice`, and the server-side pages
  `PlansPage`, `CouponsPage`, `SubscriptionsPage`, `InvoicesPage`,
  `AllSubscriptionsPage` and `AllInvoicesPage`.
- `Listener` and the events `subscription.trial_started`, `invoice.paid`,
  `subscription.activated`, `subscription.renewed`, `subscription.past_due`,
  `subscription.expired` and `subscription.canceled`, inside the act's
  transaction; every act recorded in arandu-activitylog (`ActivityLogName`).
- The actions `PlansView`, `PlansManage`, `CouponsManage`, `Subscribe`,
  `SubscriptionsView`, `SubscriptionsCancel`, `SubscriptionsManage`,
  `InvoicesView`, `InvoicesManage`, `PaymentsConfirm`, `SubscriptionsSweep`,
  `AdminAction` and `AccountAction`; the roles `AdminRole` and `AccountRole`.
- The account's screens (plans, subscriptions, invoices, one invoice) and the
  admin's (plans, one plan, coupons, subscriptions, invoices), published with
  `aru vendor:publish --tag=view`, every list a server-side Kyse DataTable;
  the catalogue of sentences in `en` and `pt-BR`; `PortuguesePaths`.
- The gateways' notification address, `POST NotificationPrefix/{provider}`,
  exempt from CSRF; the sweep scheduled every fifteen minutes.
- The migrations `20261023_0001_create_billing_plans`,
  `20261023_0002_create_billing_coupons`,
  `20261023_0003_create_billing_subscriptions`,
  `20261023_0004_create_billing_invoices`,
  `20261023_0005_create_billing_payments` and
  `20261023_0006_create_billing_coupon_usages`.
- Requires Framework `v0.65.0` and Hesape `v0.84.2`; the manifest floor is
  `framework = ">= 0.65"`.
