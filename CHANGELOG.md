# Changelog

Everything worth knowing about a release of Arandu Billing is recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A published module version is immutable: Go serves it from the proxy forever, so
a release is corrected by another release and never by moving a tag.

## [Unreleased]

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
