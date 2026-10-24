package feature_test

import (
	"context"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/foundation"
	"github.com/arandu-io/framework/security"

	billing "github.com/hyz-is/arandu-billing"
	"github.com/hyz-is/arandu-billing/walletcredit"
	wallet "github.com/hyz-is/arandu-wallet"
)

// TestAPaidPlanDepositsItsCreditsInTheWalletOnce holds the optional adapter:
// the balance a plan sells lives in arandu-wallet, deposited once per invoice
// however often the gateway posts.
func TestAPaidPlanDepositsItsCreditsInTheWalletOnce(t *testing.T) {
	t.Parallel()
	var wallets *wallet.WalletService
	w := installWith(t, func(cfg *billing.Config, db *data.DB, sessions *security.SessionStore) []foundation.Migration {
		module, err := wallet.New(wallet.Config{Tenant: acme}, db, sessions)
		if err != nil {
			t.Fatalf("building arandu-wallet: %v", err)
		}
		wallets = module.Service()
		cfg.Listener = walletcredit.Listener(wallets, walletcredit.Options{Next: cfg.Listener})
		return module.Migrations()
	})
	ctx := context.Background()
	operator, err := wallet.OperatorFor(security.SystemGrant(wallet.WalletCreate, acme))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wallets.Open(ctx, operator, wallet.OpenRequest{HolderID: acme, Slug: "creditos", Name: "Créditos", Currency: "CREDIT", DecimalPlaces: 0}); err != nil {
		t.Fatalf("opening the wallet: %v", err)
	}
	plan := w.plan(t, billing.PlanRequest{Name: "Pacote 1000", Price: 19900, Meta: billing.Meta{"wallet_slug": "creditos", "wallet_amount": "1000"}})
	r := w.subscribe(t, owner(acme), plan.ID, "")
	w.gateway.Pay(r.Invoice.ID, 19900, 1)
	w.notify(t, r.Invoice.ID)
	w.notify(t, r.Invoice.ID)

	found, err := wallets.FindBySlug(ctx, operator, acme, "creditos")
	if err != nil {
		t.Fatal(err)
	}
	if got := int64(found.Balance); got != 1000 {
		t.Fatalf("the wallet holds %d after one paid invoice, want 1000", got)
	}
	if w.events.count(billing.EventInvoicePaid) != 1 {
		t.Errorf("the next listener heard %v", w.events.names())
	}
}
