package billing

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"
)

// The actions of this package. Constants rather than strings at the call site:
// a typo in an action name would silently authorize nothing, or worse,
// everything.
const (
	// PlansView is reading the active plans of the catalogue.
	PlansView security.Action = "billing.plans.view"
	// PlansManage is keeping the catalogue's plans: the platform's admin.
	PlansManage security.Action = "billing.plans.manage"
	// CouponsManage is keeping the coupons: the platform's admin.
	CouponsManage security.Action = "billing.coupons.manage"
	// Subscribe is an account paying for a plan or starting its trial.
	Subscribe security.Action = "billing.subscribe"
	// SubscriptionsView is an account reading its own subscriptions.
	SubscriptionsView security.Action = "billing.subscriptions.view"
	// SubscriptionsCancel is an account asking its own subscription to stop at
	// the end of the period, or taking that back.
	SubscriptionsCancel security.Action = "billing.subscriptions.cancel"
	// SubscriptionsManage is the platform's admin reading every account's
	// subscriptions and canceling one at once.
	SubscriptionsManage security.Action = "billing.subscriptions.manage"
	// InvoicesView is an account reading its own invoices and paying one.
	InvoicesView security.Action = "billing.invoices.view"
	// InvoicesManage is the platform's admin reading every account's invoices,
	// confirming a payment by hand and canceling an open invoice.
	InvoicesManage security.Action = "billing.invoices.manage"
	// PaymentsConfirm is confirming a payment a gateway reported. It is
	// answered for this package's own system subject alone, after the gateway
	// was asked again.
	PaymentsConfirm security.Action = "billing.payments.confirm"
	// SubscriptionsSweep is moving the subscriptions whose period ended. It is
	// answered for this package's own system subject alone.
	SubscriptionsSweep security.Action = "billing.subscriptions.sweep"
	// AdminAction is being this package's admin, held as an action: what an
	// application whose permissions arandu-permission administers puts in a
	// group. It opens what AdminRole opens, and nothing more.
	AdminAction security.Action = "billing.admin"
	// AccountAction is managing an account's billing, held as an action. It
	// opens what AccountRole opens, and nothing more.
	AccountAction security.Action = "billing.account"
)

// AdminRole is the role an application grants to whoever runs the platform's
// billing: the catalogue, the coupons, and every account's subscriptions and
// invoices. It is the platform's team, not any customer's staff.
const AdminRole = "billing.admin"

// AccountRole is the role an application grants, inside an account, to whoever
// may pay for it and cancel it: the account's owner, its finance. Any member
// of the account reads its plans, subscriptions and invoices without it.
const AccountRole = "billing.account"

// IsAdmin says whether the subject runs the platform's billing: it holds
// AdminRole, or AdminAction among the actions arandu-permission resolved for
// it. A subject inside somebody else's account is not, whatever it holds: an
// admin acting there acts as the account.
func IsAdmin(s security.Subject) bool {
	if s.IsGuest() || s.Impersonated() {
		return false
	}
	return s.HasRole(AdminRole) || s.Can(AdminAction)
}

// IsAccountManager says whether the subject may pay for its account and cancel
// its subscriptions: it holds AccountRole, or AccountAction.
func IsAccountManager(s security.Subject) bool {
	return !s.IsGuest() && (s.HasRole(AccountRole) || s.Can(AccountAction))
}

// systemRole and systemSubjectID are the subject this package acts as when a
// gateway's notification or the clock moves an account's billing: there is
// no session then, and the act is decided by the policy like any other.
const (
	systemRole      = "billing.system"
	systemSubjectID = "billing:system"
)

// lookupTenant is the tenant of the Grant a notification's invoice is looked
// up with, across every tenant, before the tenant is known: the lookup reads a
// Global model, which any valid tenant opens, and writes nothing. Every write
// that follows runs under a Grant for the invoice's own tenant.
const lookupTenant = "billing"

// systemSubject is this package's own subject inside one tenant.
func systemSubject(tenant string) security.Subject {
	return security.Subject{ID: systemSubjectID, Tenant: tenant, Roles: []string{systemRole}}
}

// Account is what the policy decides about: the account (the tenant) an act
// touches. The empty Account is the catalogue, which belongs to no account.
type Account struct {
	// TenantID is the account's tenant, empty for the catalogue.
	TenantID string
}

// BillingPolicy is the only authority over who does what in this package.
//
// It denies unless a rule below says otherwise, and the rules are written
// around three subjects: an account's member, who reads the account's own
// billing, and its manager, who pays for it; the platform's admin, who keeps
// the catalogue and acts on every account; and this package's own system
// subject, which confirms what a gateway reported and moves the periods the
// clock ended.
type BillingPolicy struct{}

// Compile-time proof that the policy answers about accounts and says which
// actions write.
var (
	_ security.Policy[Account] = BillingPolicy{}
	_ interface {
		Writes(security.Action) bool
	} = BillingPolicy{}
)

// Writes says which actions change something: every one but the three that
// read. A Grant issued to read cannot save.
func (BillingPolicy) Writes(a security.Action) bool {
	switch a {
	case PlansView, SubscriptionsView, InvoicesView:
		return false
	}
	return true
}

// Can decides whether the subject may perform the action on the account.
func (BillingPolicy) Can(ctx context.Context, s security.Subject, a security.Action, record Account) error {
	// There is no billing a visitor with no session owns.
	if s.IsGuest() {
		return fmt.Errorf("a guest has no billing")
	}

	// arandu:begin custom
	// This package's own subject, inside the one tenant it was built for, and
	// for the two acts that have no session behind them.
	if s.ID == systemSubjectID && s.HasRole(systemRole) {
		if record.TenantID != "" && record.TenantID != s.Tenant {
			return fmt.Errorf("the system subject of %s acts on no other account", s.Tenant)
		}
		switch a {
		case PaymentsConfirm, SubscriptionsSweep:
			return nil
		}
		return fmt.Errorf("no rule allows %s to the system subject", a)
	}

	// The platform's admin acts on every account, for the admin's actions
	// alone, enumerated so an action added later is refused until a rule names
	// it.
	if IsAdmin(s) {
		switch a {
		case PlansManage, CouponsManage, SubscriptionsManage, InvoicesManage:
			return nil
		}
	}
	// arandu:end custom

	// Tenant isolation, for everybody else. Without it a member could read an
	// account that is not theirs as soon as they knew its name.
	if record.TenantID != "" && record.TenantID != s.Tenant {
		return fmt.Errorf("the account belongs to another tenant")
	}

	// arandu:begin custom
	switch a {
	case PlansView, SubscriptionsView, InvoicesView:
		// Any member of the account reads its own billing and the catalogue.
		if s.Tenant != "" {
			return nil
		}
	case Subscribe, SubscriptionsCancel:
		if s.Tenant != "" && IsAccountManager(s) {
			return nil
		}
	}
	// arandu:end custom

	return fmt.Errorf("no rule allows %s", a)
}
