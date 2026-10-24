package feature_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	hedatabase "github.com/arandu-io/hesape/database"
	pgxconnector "github.com/arandu-io/hesape/database/connectors/pgx"
	sqliteconnector "github.com/arandu-io/hesape/database/connectors/sqlite"
	hhttp "github.com/arandu-io/hesape/http"

	activitylog "github.com/hyz-is/arandu-activitylog"
	billing "github.com/hyz-is/arandu-billing"
	"github.com/hyz-is/arandu-billing/billingtest"
)

// dsn opens a database file the way the tests need it: the write lock taken
// when a transaction begins, readers beside a writer, and a wait instead of an
// error when the lock is held.
const dsn = "?_txlock=immediate&_pragma=journal_mode(WAL)&_pragma=busy_timeout(20000)"

// appKey is any thirty-two bytes.
const appKey = "0123456789abcdef0123456789abcdef"

// The two accounts the tests run as, and the platform's own tenant.
const (
	acme     = "acme"
	globex   = "globex"
	platform = "platform"
)

// clock is a time the test moves by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// events is what the Listener was told, in order.
type events struct {
	mu   sync.Mutex
	list []billing.Event
	fail error
}

func (e *events) listen(_ context.Context, ev billing.Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail != nil {
		err := e.fail
		e.fail = nil
		return err
	}
	e.list = append(e.list, ev)
	return nil
}

func (e *events) names() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.list))
	for _, ev := range e.list {
		out = append(out, string(ev.Name)+"@"+ev.Tenant)
	}
	return out
}

func (e *events) count(name billing.EventName) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, ev := range e.list {
		if ev.Name == name {
			n++
		}
	}
	return n
}

// world is one application with this package installed over a real database.
type world struct {
	sql      *sql.DB
	db       *data.DB
	module   *billing.Module
	svc      *billing.BillingService
	gateway  *billingtest.Gateway
	clock    *clock
	events   *events
	router   *fhttp.Router
	sessions *security.SessionStore
}

// signedIn are the subjects the tests started sessions for: what the session
// store's resolver answers, as an application's reads the roles again.
var signedIn sync.Map

func resolveSubjects(_ context.Context, who security.Subject) (security.Subject, error) {
	if s, ok := signedIn.Load(who.Tenant + "/" + who.ID); ok {
		return s.(security.Subject), nil
	}
	return security.Subject{}, hhttp.ErrSubjectGone
}

// postgresDSN names a PostgreSQL server to run the whole suite on. Empty runs
// it on SQLite, which serializes writers; PostgreSQL interleaves them, which is
// where a confirmation that is not guarded by its own statement is caught.
const postgresDSN = "ARANDU_TEST_POSTGRES_DSN"

// install opens a fresh database -- a schema of its own on PostgreSQL when
// ARANDU_TEST_POSTGRES_DSN is set, a SQLite file otherwise -- applies the
// migrations and builds the module over it, with the in-memory gateway first
// and the manual one second.
func install(t *testing.T) *world { return installWith(t, nil) }

// extension is what a test adds to the wiring: another module beside this
// one, its migrations, and the Config fields it changes.
type extension func(cfg *billing.Config, db *data.DB, sessions *security.SessionStore) []foundation.Migration

// installWith is install with an extension.
func installWith(t *testing.T, extend extension) *world {
	t.Helper()

	handle, connection, db := openDatabase(t)
	sessions := security.NewSessionStore([]byte(appKey), time.Hour, false, security.NewMemoryBackend())
	sessions.ResolveSubjects(resolveSubjects)
	activity, err := activitylog.New(activitylog.Config{Tenant: platform}, db, sessions)
	if err != nil {
		t.Fatalf("building arandu-activitylog: %v", err)
	}
	w := &world{sql: handle, db: db, gateway: billingtest.New("fake"), events: &events{}, sessions: sessions,
		clock: &clock{now: time.Date(2026, 10, 23, 13, 0, 0, 0, time.UTC)}}
	cfg := billing.Config{
		Prefix: "/painel", AdminPrefix: "/admin/cobranca", Paths: billing.PortuguesePaths,
		Providers: []billing.Provider{w.gateway, billing.ManualProvider{Instructions: "Pix para a chave da empresa."}},
		Listener:  w.events.listen, Clock: w.clock.Now, Activity: activity.Logger(), Locale: "pt-BR",
		PublicURL: "https://app.example",
	}
	extra := activity.Migrations()
	if extend != nil {
		extra = append(extra, extend(&cfg, db, sessions)...)
	}
	module, err := billing.New(cfg, db, sessions)
	if err != nil {
		t.Fatalf("building the module: %v", err)
	}
	migrations := hedatabase.ForMigrations(connection)
	for _, migration := range append(extra, module.Migrations()...) {
		if err := migration.Up(context.Background(), migrations); err != nil {
			t.Fatalf("applying %s: %v", migration.GetName(), err)
		}
	}
	w.module, w.svc = module, module.Service()
	w.router = fhttp.NewRouter().WithFlash(security.NewFlash([]byte(appKey), false))
	module.Routes(w.router.ForModule(module.Name()))
	return w
}

// openDatabase is the engine the suite runs on.
func openDatabase(t *testing.T) (*sql.DB, *hedatabase.Connection, *data.DB) {
	t.Helper()
	if dsn := os.Getenv(postgresDSN); dsn != "" {
		return openPostgres(t, dsn)
	}
	path := filepath.Join(t.TempDir(), "billing.sqlite")
	handle, err := sql.Open(sqliteconnector.SQLiteConnector{}.DriverName(), "file:"+path+dsn)
	if err != nil {
		t.Fatalf("opening SQLite: %v", err)
	}
	handle.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = handle.Close() })
	connection := hedatabase.NewConnection(handle, path, "", map[string]any{
		"driver": string(hedatabase.DialectSQLite),
		"name":   "billing-test",
	})
	return handle, connection, data.Wrap(handle, data.DialectSQLite)
}

// openPostgres opens a schema of its own for the test, dropped afterwards.
func openPostgres(t *testing.T, dsn string) (*sql.DB, *hedatabase.Connection, *data.DB) {
	t.Helper()
	schema := "billing_" + strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		}
		return '_'
	}, t.Name())
	if len(schema) > 60 {
		schema = schema[:60]
	}
	ctx := context.Background()
	bootstrap, err := sql.Open(pgxconnector.PostgresConnector{}.DriverName(), dsn)
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	for _, statement := range []string{`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`, `CREATE SCHEMA ` + schema} {
		if _, err := bootstrap.ExecContext(ctx, statement); err != nil {
			_ = bootstrap.Close()
			t.Fatalf("%s: %v", statement, err)
		}
	}
	_ = bootstrap.Close()
	t.Cleanup(func() {
		cleanup, err := sql.Open(pgxconnector.PostgresConnector{}.DriverName(), dsn)
		if err != nil {
			return
		}
		defer func() { _ = cleanup.Close() }()
		_, _ = cleanup.ExecContext(context.Background(), `DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	})
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("reading %s: %v", postgresDSN, err)
	}
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = strings.ReplaceAll(query.Encode(), "+", "%20")
	handle, err := sql.Open(pgxconnector.PostgresConnector{}.DriverName(), parsed.String())
	if err != nil {
		t.Fatalf("opening PostgreSQL: %v", err)
	}
	handle.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = handle.Close() })
	connection := hedatabase.NewConnection(handle, schema, "", map[string]any{
		"driver": string(hedatabase.DialectPostgres),
		"name":   "billing-test",
	})
	return handle, connection, data.Wrap(handle, data.DialectPostgres)
}

// admin runs the platform's billing.
func admin() security.Subject {
	return security.Subject{ID: "team-1", Tenant: platform, Roles: []string{billing.AdminRole}, Verified: true}
}

// owner pays for an account.
func owner(tenant string) security.Subject {
	return security.Subject{ID: "owner-" + tenant, Tenant: tenant, Roles: []string{billing.AccountRole}, Verified: true}
}

// member reads an account's billing and pays for nothing.
func member(tenant string) security.Subject {
	return security.Subject{ID: "member-" + tenant, Tenant: tenant, Verified: true}
}

// plan adds a plan to the catalogue.
func (w *world) plan(t *testing.T, in billing.PlanRequest) *billing.Plan {
	t.Helper()
	if in.Name == "" {
		in.Name = "Câmera mensal"
	}
	if in.IntervalMonths == 0 {
		in.IntervalMonths = 1
	}
	in.Active = true
	p, err := w.svc.CreatePlan(context.Background(), admin(), in)
	if err != nil {
		t.Fatalf("creating the plan: %v", err)
	}
	return p
}

// coupon adds a coupon.
func (w *world) coupon(t *testing.T, in billing.CouponRequest) *billing.Coupon {
	t.Helper()
	in.Active = true
	c, err := w.svc.CreateCoupon(context.Background(), admin(), in)
	if err != nil {
		t.Fatalf("creating the coupon: %v", err)
	}
	return c
}

// subscribe checks out a plan for an account with the in-memory gateway.
func (w *world) subscribe(t *testing.T, who security.Subject, planID, coupon string) *billing.CheckoutResult {
	t.Helper()
	r, err := w.svc.Subscribe(context.Background(), who, billing.SubscribeRequest{PlanID: planID, CouponCode: coupon})
	if err != nil {
		t.Fatalf("subscribing: %v", err)
	}
	return r
}

// signIn starts a session for the subject and answers its cookies.
func (w *world) signIn(t *testing.T, s security.Subject) []*http.Cookie {
	t.Helper()
	signedIn.Store(s.Tenant+"/"+s.ID, s)
	rec := httptest.NewRecorder()
	if _, err := w.sessions.Start(context.Background(), rec, security.Subject{ID: s.ID, Tenant: s.Tenant}); err != nil {
		t.Fatalf("starting a session: %v", err)
	}
	return rec.Result().Cookies()
}

// do sends a request through the module's routes. A JSON client asks with
// Accept: application/json; a body is a form.
func (w *world) do(t *testing.T, method, target, body string, json bool, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if json {
		req.Header.Set("Accept", "application/json")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	return w.serve(req)
}

// serve answers a request as a deployed application does: an error nobody
// expected reaches the Recover middleware, which answers 500.
func (w *world) serve(req *http.Request) (rec *httptest.ResponseRecorder) {
	rec = httptest.NewRecorder()
	defer func() {
		if recovered := recover(); recovered != nil {
			rec = httptest.NewRecorder()
			rec.WriteHeader(http.StatusInternalServerError)
		}
	}()
	w.router.ServeHTTP(rec, req)
	return rec
}

// notify posts the gateway's notification about an invoice.
func (w *world) notify(t *testing.T, invoiceID string) *httptest.ResponseRecorder {
	t.Helper()
	address := w.module.URLs().Notification(w.gateway.Name())
	return w.serve(w.gateway.Notification(address, invoiceID))
}

// count is how many rows a table holds, for an assertion about the database
// itself.
func (w *world) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if w.db.Dialect() == data.DialectPostgres {
		query = numbered(query)
	}
	if err := w.sql.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// numbered writes a query's ? placeholders as PostgreSQL's $1, $2.
func numbered(query string) string {
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
