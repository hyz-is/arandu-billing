---
name: billing-policy
description: Decide who may do what in Arandu Billing, and open an action that is currently closed. Use when the request is to "let the account's members pay", "let finance confirm payments", "add an action", "add a permission", "everything returns 403", "the tests say ErrForbidden", "the admin cannot see another account", "add a Model-backed service method", or when a change touches policy.go or a *_service.go. Covers the Account the policy decides about, the three subjects and the system subject, authorization before the Model, the Grant for another account's rows, and why the tenant is never read from the request.
license: MIT
---

# The billing policy

`BillingPolicy` decides about an `Account` -- the tenant an act touches -- and
nothing else. The empty `Account{}` is the catalogue, which belongs to no
account. The policy denies unless a rule names the action, and the rules are
written around four subjects:

| subject | rule |
| --- | --- |
| a guest | refused, always |
| a member of the account (`s.Tenant`) | `PlansView`, `SubscriptionsView`, `InvoicesView` on their own account |
| the account's manager (`AccountRole` / `AccountAction`) | also `Subscribe` and `SubscriptionsCancel` on their own account |
| the platform's admin (`AdminRole` / `AdminAction`, never while impersonating) | `PlansManage`, `CouponsManage`, `SubscriptionsManage`, `InvoicesManage` on any account |
| this package's system subject (`systemSubject(tenant)`) | `PaymentsConfirm`, `SubscriptionsSweep`, inside its one tenant |

Every rule sits inside an `arandu:begin custom` block, after the guest refusal;
the tenant check runs for everybody but the admin's actions and the system
subject. What is not written stays closed, including every action added later:
the function ends in a refusal and there is no other exit.

`Writes` says which actions change something -- all but the three views -- so a
Grant issued to read cannot save.

## How a request gets through

1. The service validates the input. A rejected input never reaches the policy.
2. The service calls `security.Authorize(ctx, s.policy, actor, ACTION, Account{TenantID: ...})`.
3. `Can` returns nil, or the reason it will not.
4. Only then does a `security.Grant` exist, and the service reaches the
   generated query with it: `Plans(s.db)`, `Invoices(s.db)` and the rest.

`TestEveryServiceMethodAuthorizesBeforeTheModel` reads every exported
`BillingService` method and refuses one that constructs a generated query
before `Authorize`, or never reaches one. The runtime twin,
`TestTheServiceRefusesBeforeReachingTheModel`, builds the service over a nil
database, so a statement issued before the refusal panics.

## Writing another account's rows

The admin's Grant carries the admin's own tenant. An act on a customer's
subscription or invoice first finds the row across accounts through
`internal/everytenant` -- Global read models, under the probe Grant the policy
issued for `Account{}` -- and then authorizes again for that account:

```go
	probe, err := security.Authorize(ctx, s.policy, actor, InvoicesManage, Account{})
	row, err := everytenant.Invoices(s.db).WhereKey(id).FirstOrFail(ctx, probe)
	g, err := security.Authorize(ctx, s.policy, actingIn(actor, row.TenantID), InvoicesManage, Account{TenantID: row.TenantID})
	inv, err := Invoices(s.db).WhereKey(row.ID).FirstOrFail(ctx, g)
```

Every write after that runs under `g`, scoped to the account, with the admin as
causer in the activity log. A gateway's notification does the same with
`systemSubject(row.TenantID)`. `security.SystemGrant` is not used: a Grant no
policy issued is a Grant nobody decided.

## Adding a Model-backed service method

Validate first, call `security.Authorize`, and only then reach the query.
Create builds the row with `Plans(s.db).New()` (or the entity's own
constructor), keeps its pointer, fills it field by field, writes `TenantID`
from `data.Tenant(g)` for a tenant-scoped row, and calls `Save(ctx, g)`.
`TestTheServiceWritesTenantOnlyFromTheGrant` reads every assignment to a
`TenantID` and refuses anything but `data.Tenant(g)`.

Use `FirstOrFail` where a missing row is an answer: `First` answers nil and no
error when there is none. `notFound(err)` turns the model's missing row into
`ErrNotFound`, which the router answers with 404 -- the same answer as a row of
another account, so a probe learns nothing.

## Opening an action

Write the rule in the custom block, add the action to `everyAction` in
`tests/Unit/policy_test.go`, and change the tests that assert the refusal into
tests that assert who is now allowed and who is still refused. Name the action
in the CHANGELOG entry of the release that adds it; the release suite checks.

## Three shapes that look like authorization and are not

- **A tenant taken from the request.** The path, the body, the query string, a
  header -- all of them are values the caller chose. `data.Tenant(g)` is the
  only source; `TestNoTenantIsReadOutOfTheRequest` fails on the others.
- **A check in the handler.** Handlers read the input and `ctx.User()`, ask the
  service and return what it returned.
- **A notification believed.** A gateway's post names an invoice and confirms
  nothing: the provider is asked again with `QueryStatus`, and only its answer,
  with the invoice's total, confirms.
