package billing

import (
	"errors"
	stdhttp "net/http"

	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
)

// The gateways' notification address. It is the one route of this package
// with no session behind it, and it is kept apart from the routes in module.go
// for that reason: a gateway is nobody's session, and nothing it posts is
// believed. The provider reads the request, the gateway is asked again, and
// only its answer confirms anything.

var _ foundation.CSRFExempt = (*Module)(nil)

// NotificationRoutes registers the notification address alone:
// POST NotificationPrefix + "/{provider}", one per configured provider, for
// an application that mounts it on its own API side.
func (m *Module) NotificationRoutes(r *fhttp.Router) {
	r.Action(stdhttp.MethodPost, m.cfg.NotificationPrefix+"/{provider}", m.notify).Name("billing.notifications")
}

// CSRFExemptPaths exempts the notification address from the CSRF check: a
// gateway posts from its own servers, with no token to send.
func (m *Module) CSRFExemptPaths() []string { return []string{m.cfg.NotificationPrefix + "/"} }

// notify answers a gateway. The statuses are written here, and not left to
// the router, because the client is a gateway that retries on anything but a
// 2xx: 200 for a notification handled (or handled before), 400 for one the
// provider does not understand, 404 for an invoice or a provider that is not
// there, 409 for a payment whose amount is not the invoice's, and an error --
// a 500, sent again later -- for anything else.
func (m *Module) notify(ctx *fhttp.Context) error {
	inv, err := m.svc.HandleNotification(ctx.Ctx(), ctx.Param("provider"), ctx.Request)
	switch {
	case err == nil:
		return ctx.JSON(stdhttp.StatusOK, fieldsResource{"id": inv.ID, "status": string(inv.Status)})
	case errors.Is(err, ErrBadNotification):
		return ctx.JSON(stdhttp.StatusBadRequest, fieldsResource{"error": "bad_notification"})
	case missing(err):
		return ctx.JSON(stdhttp.StatusNotFound, fieldsResource{"error": "not_found"})
	case errors.Is(err, ErrAmountMismatch):
		return ctx.JSON(stdhttp.StatusConflict, fieldsResource{"error": "amount_mismatch"})
	}
	return err
}
