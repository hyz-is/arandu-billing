# Working on Arandu Billing

This is the Arandu module for billing management: a catalogue of plans and
coupons, the subscriptions of each account and the date each one is paid until,
and the invoices a payment gateway collects. It is a Go module somebody `go
get`s and registers by hand in their own `bootstrap/app.go`. There is no
service provider, no container and no discovery -- if a line of wiring is not
written in the installer's repository, it does not happen.

Read `.agents/skills/` before writing code. Each skill is a procedure, and the
one you need is named by the situation you are in.

## No gateway lives here

This package ships no payment gateway, and that is the rule this repository
is built around. It defines the `Provider` interface (`provider.go`) -- create a
checkout, read a notification, ask a payment's status -- and the contract around
it: a notification is believed only after the gateway is asked again, and an
invoice is confirmed once. Each application implements its gateway as a service
of its own, over its own client and merchant account, and hands it to `New` in
`Config.Providers`.

So no gateway's code, stub, configuration field, environment variable or
documentation as a built-in belongs in this repository. Once other projects
depend on the module, removing a gateway from it would break them; a gateway
that lives in the application is the application's to change. What does belong
here is gateway-free: `ManualProvider` (the admin confirms a payment by hand)
and `billingtest.Gateway`, the in-memory gateway the suites run against.

## The gates

Nothing is finished until all of these exit zero.

```sh
export GOWORK=off
gofmt -l $(find . -name '*.go' -not -path '*/testdata/*' -not -name '*.kyse.go')
go build ./...
go vet ./...
go test -race -p 2 ./...
aru model:build --check
```

The feature suite runs on SQLite by default. It runs whole on PostgreSQL --
where two transactions really interleave, which is where a confirmation not
guarded by its own statement is caught -- when `ARANDU_TEST_POSTGRES_DSN`
names a server; each test opens a schema of its own and drops it:

```sh
ARANDU_TEST_POSTGRES_DSN="postgres://localhost:5432/arandu_billing_test?sslmode=disable" \
  go test -race -p 2 ./tests/Feature/
```

A change is not finished until it passes on both.

`GOWORK=off` is not borrowed from somewhere else, and here it is not a
preference either. This checkout may sit beside a Go workspace that lists the
framework repositories and does not list this one; when it does, every command
above fails before it compiles anything:

```
pattern ./...: directory prefix . does not contain modules listed in go.work
or their selected dependencies
```

With the workspace off, the module resolves the framework version in `go.mod` —
which is what CI compiles against, and what somebody's `go get` will get.

Both filters on `gofmt` are load-bearing in the toolchain even where this
repository has nothing for them to skip: `gofmt` is the only tool in the chain
that ignores build tags, and `testdata/` is where a fixture is allowed to be
invalid on purpose.

`aru doctor` is not one of the gates, and running it here costs a minute and
answers nothing:

```
this is not an Arandu project: no go.mod, main.go and arandu.toml together.
Run it from inside a project, or create one with `aru new`
```

It exits 1. It reads applications, and this is a library.

It does not read this one after it is installed either, and that is why
`tests/Unit/audit_test.go` exists. The doctor walks the application's own tree,
skips `vendor/`, and opens the one `arandu.mod.toml` at its root; it never loads
a dependency. So `tenant-from-request`, `system-grant-without-tenant` and
`permission-not-declared` never see a line of an installed package, and whatever
this one must prove about itself it proves in its own suite or nowhere.

## What this repository holds

| | measured with |
| --- | --- |
| 23 Go files in the package at the root, six of them generated | `ls *.go` |
| `internal/everytenant`: the Global read models, after the policy | `ls internal/everytenant` |
| `billingtest`: the in-memory gateway; `walletcredit`: the optional arandu-wallet adapter | `ls billingtest walletcredit` |
| 22 routes behind a session, one notification address without | `grep -c '{get\|{post\|{del' paths.go` |
| 13 actions the policy answers about | `grep -cE '^\t[A-Za-z]+ +security.Action = ' policy.go` |
| 6 migrations | `grep -c 'GetName() string' module.go` |

The layout is by role rather than by layer:

```
module.go               registration, routes, handlers and migrations
notifications.go        the gateways' notification address, the one route with no session
config.go               what the application passes in
paths.go                the addresses, and the route table
model.go                the entities, their tables, and their own rules
PlanQuery.go ...        the queries aru model:build generates from model.go
pricing.go              what a period costs, the next period, what the clock does
provider.go             the gateway contract, and the gateway-free ManualProvider
policy.go               who may do what
service.go              the catalogue: plans and coupons
subscription_service.go checkout, trial, renewal, cancellation, the sweep
payment_service.go      notifications, confirmation, manual payments
listing.go              the server-side pages the data tables read
events.go               the Listener's events, and the activity log
screens.go views.go     what the published views are drawn with
translation.go          the catalogue of sentences, en and pt-BR
```

`planTable`, in `model.go`, declares the catalogue's table once, Global
because every account reads the same plans; `Plans(db)` is generated from it
into `PlanQuery.go`. The coupons are Global as well. The subscriptions,
invoices, payments and coupon usages keep the default `tenant_id` scope: they
belong to the account whose Grant wrote them. The generated files are never
edited by hand, and `aru model:build --check` says when one is stale.

The pure rules -- the price with a coupon (`Price`), the installments a plan
offers (`Plan.Installments`), the next period a payment buys (`NextPeriod`),
what the clock does to a subscription (`Due`) -- live in `pricing.go` and the
custom block of `model.go`. The services validate, ask the policy for a Grant,
call those rules and save with the Grant.

## Tenancy

An account is a tenant. Its member reads its billing, its manager
(`AccountRole`) pays and cancels, and every row it owns is written with
`data.Tenant(g)` from the member's own Grant. The platform's admin
(`AdminRole`) keeps the catalogue and acts on every account: the lists across
accounts read `internal/everytenant` after the policy allowed it, and an act on
one account runs under a Grant the policy issued for that account, with the
admin as causer. A gateway's notification names an invoice and never a tenant:
the invoice is looked up across accounts, and everything that follows runs
under a Grant for the invoice's own tenant, as this package's system subject.

## What does not exist here

| A model reaches for | What is here instead |
| --- | --- |
| a gateway client, built in | `Provider`, implemented by the application |
| a balance, credits, a ledger | arandu-wallet; `walletcredit` deposits a paid plan's credits |
| a service provider, a container, a `Register()` that discovers things | `New(cfg, db, sessions)`, called by hand in `bootstrap/app.go` |
| a global `DB`, an `init()` that opens a connection | the `*data.DB` handed to `New` |
| a CRUD Repository beside the Model | `Plans(db)` and the other generated queries, reached only by `BillingService` after `security.Authorize` |
| a tenant read from the path, the body, the query or a header | `data.Tenant(g)`, from the Grant |
| a permit-all branch in the policy | nothing. The policy denies, and an action is opened by writing the rule that opens it |
| a price, a trial or an installment rule written in code | the plan's own row, which the application seeds or the admin edits |
| a `panic` on bad wiring | an `error` from `New` |

## The properties

1. **The policy denies by default.** `tests/Unit/policy_test.go` walks the
   subjects -- a guest, a member, the manager, the platform's admin, another
   account -- against every action.
2. **Every Service method authorizes before it reaches the Model.**
   `TestEveryServiceMethodAuthorizesBeforeTheModel` reads the source, and
   `TestTheServiceRefusesBeforeReachingTheModel` gives the Service a nil handle
   so a statement issued before the refusal would panic.
3. **The tenant comes from `data.Tenant(g)`**, read and write.
   `TestTheServiceWritesTenantOnlyFromTheGrant` reads every tenant assignment,
   and `TestOneAccountReachesNoOtherAccountsBilling` drives it.
4. **An invoice is confirmed once, and only after the gateway is asked.**
   `TestANotificationConfirmsOnceHoweverOftenItArrives`,
   `TestNotificationsRacingConfirmOnce` and
   `TestANotificationIsBelievedOnlyAfterTheGatewayIsAsked`.

`tests/Unit/audit_test.go` also compares `arandu.mod.toml` against what the
code *calls*. The package makes no outbound call -- the gateway is the
application's -- so it declares `network = false`, and the suite fails the day
that stops being true.

## Writing code

Everything in the source is in English: identifiers, doc comments, internal
comments, error messages, log messages, and the names and messages of tests.
`pkg.go.dev` publishes the doc comments and its readers are users of this
package.

Every exported symbol carries a doc comment, and the comment documents the
symbol and nothing else. Why a signature is what it is belongs there when it is
a fact about the code — *"the value is held because Go does not build a type
from a string"* stays. A date, an issue number, a version in progress or the
name of another repository does not.

Tests go under `tests/`, in a capitalized category directory declaring a
lowercase external package: `tests/Unit` holds `package unit_test`,
`tests/Feature` holds `package feature_test`. Both import the package by its
module path, which is what makes them see exactly what a caller sees. A test
that genuinely needs something unexported goes beside the code as
`*_internal_test.go`, and the suffix is how it says so.
