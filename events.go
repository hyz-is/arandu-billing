package billing

import (
	"context"
	"time"

	"github.com/arandu-io/framework/security"
	activitylog "github.com/hyz-is/arandu-activitylog"
)

// EventName is what happened to an account's billing.
type EventName string

// The events the consumer's Listener is told about.
const (
	// EventTrialStarted is a trial begun.
	EventTrialStarted EventName = "subscription.trial_started"
	// EventActivated is a subscription paid for the first time, or again after
	// it ended.
	EventActivated EventName = "subscription.activated"
	// EventRenewed is a subscription paid for another period.
	EventRenewed EventName = "subscription.renewed"
	// EventPastDue is a period ended without a payment, inside the grace.
	EventPastDue EventName = "subscription.past_due"
	// EventExpired is a subscription ended without a payment.
	EventExpired EventName = "subscription.expired"
	// EventCanceled is a subscription stopped: at the end of the period the
	// customer asked it to, or at once by the admin.
	EventCanceled EventName = "subscription.canceled"
	// EventInvoicePaid is an invoice confirmed, once. It comes before the
	// subscription's own event of the same payment.
	EventInvoicePaid EventName = "invoice.paid"
)

// Event is what the Listener is told.
type Event struct {
	// Name is what happened.
	Name EventName
	// Tenant is the account it happened to.
	Tenant string
	// At is when, in UTC.
	At time.Time
	// Subscription is the subscription as it stands after the event.
	Subscription SubscriptionResource
	// Plan is the subscription's plan, with the consumer's Meta.
	Plan PlanResource
	// Invoice is the invoice that caused it, nil for the clock's events.
	Invoice *InvoiceResource
	// Grant is the Grant the act ran under, scoped to Tenant: what the
	// Listener hands arandu-wallet (wallet.OperatorFor) or its own services
	// to act inside the same account.
	Grant security.Grant
}

// Listener is told about each Event, inside the transaction of the act.
type Listener func(ctx context.Context, e Event) error

// ActivityLogName is the log every entry of this package is recorded in.
const ActivityLogName = "billing"

// The activity events this package records, one per act that changes
// something.
const (
	activityPlanCreated         = "plan.created"
	activityPlanUpdated         = "plan.updated"
	activityCouponCreated       = "coupon.created"
	activityCouponUpdated       = "coupon.updated"
	activityInvoiceCreated      = "invoice.created"
	activityInvoicePaid         = "invoice.paid"
	activityInvoiceFailed       = "invoice.failed"
	activityInvoiceCanceled     = "invoice.canceled"
	activityCancelScheduled     = "subscription.cancel_scheduled"
	activityCancelWithdrawn     = "subscription.cancel_withdrawn"
	activitySubscriptionMoved   = "subscription.moved"
	activitySubscriptionStarted = "subscription.started"
)

// record writes one entry of the act just done, under the Grant it was
// authorized with: the tenant of the entry is the act's, and the causer is the
// subject who acted. Without Config.Activity it records nothing.
func (s *BillingService) record(ctx context.Context, g security.Grant, actor security.Subject, event, kind, id string, properties map[string]any) error {
	if s.activity == nil {
		return nil
	}
	description := event
	if s.describe != nil {
		description = s.describe(event)
	}
	entry := s.activity.In(ctx, g, ActivityLogName).Event(event).
		OnRef(activitylog.Ref{Type: kind, ID: id})
	if actor.ID != "" && actor.ID != systemSubjectID {
		entry = entry.ByRef(activitylog.Ref{Type: "user", ID: actor.ID})
	}
	if len(properties) > 0 {
		entry = entry.WithProperties(properties)
	}
	_, err := entry.Log(description)
	return err
}

// emit tells the Listener about one event.
func (s *BillingService) emit(ctx context.Context, e Event) error {
	if s.listener == nil {
		return nil
	}
	return s.listener(ctx, e)
}
