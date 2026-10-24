// Package walletcredit turns a paid invoice into a deposit in arandu-wallet:
// the optional adapter for an application whose plans sell credits, minutes or
// any other balance. Billing keeps no balance of its own; the wallet is where
// it lives, and this is the one line between the two.
//
// The plan says what it deposits, in its Meta:
//
//	wallet_slug   = "creditos"   the slug of the account's wallet
//	wallet_amount = "1000"       the decimal to deposit, at the wallet's scale
//
// A plan without both deposits nothing. The deposit runs as the wallet's
// operator of the invoice's own account (wallet.OperatorFor on the Event's
// Grant), inside the transaction that confirmed the invoice, under the
// idempotency key "billing:invoice:<id>": an invoice deposits once, and a
// deposit that fails rolls the confirmation back so the gateway sends again.
package walletcredit

import (
	"context"
	"fmt"
	"strings"

	billing "github.com/hyz-is/arandu-billing"
	wallet "github.com/hyz-is/arandu-wallet"
)

// The Meta names a plan carries its deposit in, unless Options names others.
const (
	DefaultSlugKey   = "wallet_slug"
	DefaultAmountKey = "wallet_amount"
)

// Options say where in the plan the deposit is written, and whose wallet it
// goes to.
type Options struct {
	// SlugKey and AmountKey are the Meta names; empty is the default.
	SlugKey, AmountKey string
	// Holder is whose wallet it is: the invoice's account itself when nil.
	Holder func(e billing.Event) string
	// Next is told about every event after the deposit, for an application
	// that listens to more than this: nil tells nobody.
	Next billing.Listener
}

// Listener is the billing.Listener that deposits a paid plan's balance.
func Listener(wallets *wallet.WalletService, opts Options) billing.Listener {
	if opts.SlugKey == "" {
		opts.SlugKey = DefaultSlugKey
	}
	if opts.AmountKey == "" {
		opts.AmountKey = DefaultAmountKey
	}
	return func(ctx context.Context, e billing.Event) error {
		if err := deposit(ctx, wallets, opts, e); err != nil {
			return err
		}
		if opts.Next != nil {
			return opts.Next(ctx, e)
		}
		return nil
	}
}

func deposit(ctx context.Context, wallets *wallet.WalletService, opts Options, e billing.Event) error {
	if e.Name != billing.EventInvoicePaid || e.Invoice == nil {
		return nil
	}
	slug := strings.TrimSpace(e.Plan.Meta[opts.SlugKey])
	amount := strings.TrimSpace(e.Plan.Meta[opts.AmountKey])
	if slug == "" || amount == "" {
		return nil
	}
	if wallets == nil {
		return fmt.Errorf("walletcredit: plan %s deposits into %q and no wallet service was wired", e.Plan.Slug, slug)
	}
	holder := e.Tenant
	if opts.Holder != nil {
		holder = opts.Holder(e)
	}
	operator, err := wallet.OperatorFor(e.Grant)
	if err != nil {
		return err
	}
	w, err := wallets.FindBySlug(ctx, operator, holder, slug)
	if err != nil {
		return fmt.Errorf("walletcredit: the wallet %q of %s: %w", slug, holder, err)
	}
	_, err = wallets.Deposit(ctx, operator, wallet.DepositRequest{
		IdempotencyKey: "billing:invoice:" + e.Invoice.ID,
		WalletID:       w.ID,
		Amount:         amount,
		Meta:           wallet.Meta{"billing_invoice": e.Invoice.ID, "billing_plan": e.Plan.Slug},
	})
	return err
}
