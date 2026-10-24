package billing

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/translation"
	hview "github.com/arandu-io/hesape/view"
	activitylog "github.com/hyz-is/arandu-activitylog"
)

// The defaults for the optional settings. They are constants rather than
// literals inside Config.withDefaults, so the value a reader finds here is the
// value the package uses.
const (
	// DefaultPrefix is where the account's routes are mounted when Config
	// leaves Prefix empty.
	DefaultPrefix = "/billing"
	// DefaultNotificationPrefix is where the gateways post their
	// notifications when Config leaves NotificationPrefix empty: one address
	// per provider, NotificationPrefix + "/" + Provider.Name().
	DefaultNotificationPrefix = "/api/billing/notifications"
	// DefaultPageSize is how many records one page answers with when Config
	// leaves PageSize at zero.
	DefaultPageSize = 25
	// MaxPageSize is the ceiling PageSize is refused above.
	MaxPageSize = 200
	// DefaultLocation is the timezone dates are shown in when Config leaves
	// Location nil.
	DefaultLocation = "America/Sao_Paulo"
)

// Config is what the application passes when it wires this package.
//
// It names no tenant, and that is deliberate. Every route of an account sits
// behind RequireAuth, so the tenant of every statement is the one the Grant
// carries, from the session of whoever is asking; a notification's tenant is
// the invoice's own.
type Config struct {
	// Prefix is the path the account's routes are mounted under. Empty means
	// DefaultPrefix.
	Prefix string

	// AdminPrefix is where the admin's routes are mounted. Empty means Prefix
	// followed by Paths.Admin. An application whose team works on another
	// side of the site -- an operator console at /admin -- mounts them there.
	AdminPrefix string

	// Paths are the segments every route is built from. A field left empty
	// keeps its DefaultPaths value; PortuguesePaths is the whole set in
	// Brazilian Portuguese.
	Paths Paths

	// NotificationPrefix is where the gateways post. Empty means
	// DefaultNotificationPrefix. It is exempt from the CSRF check, and no
	// session is asked for: a notification is believed only after the
	// gateway is asked again.
	NotificationPrefix string

	// PageSize is how many records one page answers with. Zero means
	// DefaultPageSize; above MaxPageSize is refused.
	PageSize int

	// Providers are the gateways the application implemented, each a service
	// of the application's own (see Provider). At least one is required; the
	// first is the one a checkout uses unless DefaultProvider names another.
	// ManualProvider is the gateway-free one this package ships.
	Providers []Provider

	// DefaultProvider is the Name of the provider a checkout uses. Empty means
	// the first of Providers.
	DefaultProvider string

	// Listener is told what happened to an account's billing -- a
	// subscription activated, renewed, past due, expired or canceled, an
	// invoice paid -- inside the transaction that did it, so what it writes
	// commits or rolls back with it. An error rolls the act back; a gateway's
	// notification is then answered with an error and sent again. Nil tells
	// nobody.
	Listener Listener

	// Customer fills in who pays, for the gateway's page: the account's name,
	// e-mail and document, read by the application. Nil sends an empty
	// Customer.
	Customer func(ctx context.Context, who security.Subject) (Customer, error)

	// PublicURL is the application's address as the gateway reaches it,
	// "https://alpr.sh", from which the return address of a checkout is
	// built. Empty sends no return address.
	PublicURL string

	// Clock is the time, for a test that holds it still. Nil is the system's
	// clock, which is what an application wants.
	Clock func() time.Time

	// Location is the timezone dates are shown in. Nil means DefaultLocation.
	Location *time.Location

	// Activity is arandu-activitylog's logger, the application's one instance
	// (activityModule.Logger()). Every act that changes something is recorded
	// under the Grant it was authorized with, in the log named
	// ActivityLogName. Nil records nothing.
	Activity *activitylog.Logger

	// Translator is the application's own catalogue, asked before the one this
	// package ships, key by key: "billing.screen.plans_title". Optional.
	Translator *translation.Translator

	// Locale is the language every screen, message and activity description
	// is written in: "pt-BR" for an application that speaks Brazilian
	// Portuguese and nothing else. Empty means the locale the request
	// negotiated, and FallbackLocale when there is none.
	Locale string

	// TitleSeparator joins a screen's title to the application's name in the
	// document title: " — " titles the plans "Planos — Alpr.sh". Empty titles a
	// screen with its own title alone.
	TitleSeparator string

	// Middleware runs on every route of an account and of the admin after
	// RequireAuth, and AdminMiddleware after it on the admin's routes only:
	// arandu-permission's Roles(), the operator console's own guard. A guard
	// answers "is this request on the right side" and stops; the policy still
	// decides.
	Middleware      []func(http.Handler) http.Handler
	AdminMiddleware []func(http.Handler) http.Handler

	// Chrome is what the application's layout draws around a screen of this
	// package: its sidebar, its navigation. It is called once per screen and
	// carried as Screen.Chrome. Nil draws the screens with hesape's page alone.
	Chrome func(ctx *fhttp.Context, req ChromeRequest) (any, error)
}

// ChromeRequest is what Config.Chrome is asked about one screen.
type ChromeRequest struct {
	// Title is the screen's title, in the request's language.
	Title string
	// Admin says the screen is one of the admin's.
	Admin bool
	// Subject is who is asking.
	Subject security.Subject
	// Page is the page hesape's view.New built for the request.
	Page hview.Page
}

// providerName is what Provider.Name() may be: it is a segment of an address.
var providerName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

// Validate reports what the configuration cannot be used with. New calls it,
// so a setting that cannot work fails where it is wired.
func (c Config) Validate() error {
	for _, field := range []struct{ name, value string }{
		{"Prefix", c.Prefix}, {"AdminPrefix", c.AdminPrefix}, {"NotificationPrefix", c.NotificationPrefix},
	} {
		if field.value != "" && (field.value[0] != '/' || strings.HasSuffix(field.value, "/")) {
			return fmt.Errorf("billing: Config.%s is %q and has to start with / and end without one", field.name, field.value)
		}
	}
	if err := c.Paths.validate(); err != nil {
		return err
	}
	for i, mw := range c.Middleware {
		if mw == nil {
			return fmt.Errorf("billing: Config.Middleware[%d] is nil", i)
		}
	}
	for i, mw := range c.AdminMiddleware {
		if mw == nil {
			return fmt.Errorf("billing: Config.AdminMiddleware[%d] is nil", i)
		}
	}
	if c.PageSize < 0 || c.PageSize > MaxPageSize {
		return fmt.Errorf("billing: Config.PageSize is %d, and has to be between 0 and %d, where 0 means %d", c.PageSize, MaxPageSize, DefaultPageSize)
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("billing: Config.Providers is empty: implement the gateway as a service of the application (billing.Provider) and pass it here, or pass billing.ManualProvider{} to confirm payments by hand")
	}
	seen := map[string]bool{}
	for i, p := range c.Providers {
		if p == nil {
			return fmt.Errorf("billing: Config.Providers[%d] is nil", i)
		}
		name := p.Name()
		if !providerName.MatchString(name) {
			return fmt.Errorf("billing: Config.Providers[%d] is named %q: a provider's name is a segment of its notification address, lowercase letters, digits, - and _", i, name)
		}
		if seen[name] {
			return fmt.Errorf("billing: two providers are named %q", name)
		}
		seen[name] = true
	}
	if c.DefaultProvider != "" && !seen[c.DefaultProvider] {
		return fmt.Errorf("billing: Config.DefaultProvider is %q and no provider has that name", c.DefaultProvider)
	}
	if c.PublicURL != "" && !strings.HasPrefix(c.PublicURL, "https://") && !strings.HasPrefix(c.PublicURL, "http://") {
		return fmt.Errorf("billing: Config.PublicURL is %q and has to be an absolute http(s) address", c.PublicURL)
	}
	return validateRoutes(c.withDefaults())
}

// validateRoutes asks the standard library to parse the exact patterns the
// module will register, so a collision is a configuration error at New rather
// than a panic at boot.
func validateRoutes(c Config) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("billing: Config.Prefix %q, Config.AdminPrefix %q, Config.NotificationPrefix %q and Config.Paths cannot be registered as routes: %v",
				c.Prefix, c.AdminPrefix, c.NotificationPrefix, recovered)
		}
	}()
	handler := http.NotFoundHandler()
	mux := http.NewServeMux()
	for _, route := range routeTable(c) {
		mux.Handle(route.method+" "+route.path, handler)
	}
	mux.Handle(http.MethodPost+" "+c.NotificationPrefix+"/{provider}", handler)
	return nil
}

// withDefaults returns the configuration with the optional fields filled in.
// It runs after Validate and never before.
func (c Config) withDefaults() Config {
	if c.Prefix == "" {
		c.Prefix = DefaultPrefix
	}
	c.Paths = c.Paths.withDefaults()
	if c.AdminPrefix == "" {
		c.AdminPrefix = c.Prefix + c.Paths.Admin
	}
	if c.NotificationPrefix == "" {
		c.NotificationPrefix = DefaultNotificationPrefix
	}
	if c.PageSize == 0 {
		c.PageSize = DefaultPageSize
	}
	if c.DefaultProvider == "" && len(c.Providers) > 0 && c.Providers[0] != nil {
		c.DefaultProvider = c.Providers[0].Name()
	}
	c.PublicURL = strings.TrimSuffix(c.PublicURL, "/")
	if c.Location == nil {
		if loc, err := time.LoadLocation(DefaultLocation); err == nil {
			c.Location = loc
		} else {
			c.Location = time.FixedZone("BRT", -3*60*60)
		}
	}
	return c
}
