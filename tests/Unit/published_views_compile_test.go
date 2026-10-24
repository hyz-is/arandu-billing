package unit_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/framework/foundation"
)

// viewCompiler is the command line whose view compiler the published views are
// held against, at the release an application builds them with.
//
// It is a version and never "latest", so this test changes its answer when this
// line changes and never because somebody published something. Moving it is
// how a stricter compiler is taken on: raise it, run the suite, and fix the
// view it refuses here rather than in every application that publishes it.
const viewCompiler = "github.com/arandu-io/aru@v0.79.0"

// TestEveryPublishedViewCompiles publishes the views into a project of their
// own, builds them with the view compiler an application runs, and compiles
// what it wrote.
//
// Nothing else in this repository reads the markup. The sources open with a
// build tag, so go build, go vet and go test never look past it, and the view
// compiler refuses what the Go compiler would accept -- a value written into an
// address behind text it cannot read, a value in a position no escape covers.
// A view it refuses is a view that stops the build of every application that
// publishes it, and without this test the first place to say so is somebody
// else's terminal.
//
// The project is laid out as an application's is once it has published: each
// file where the publication writes it, and a go.mod that resolves this package
// to the checkout under test. What the view compiler writes then goes through
// the Go compiler, which is what catches a field the markup names and the page
// data does not have.
//
// It does not skip. A gate that steps aside when its input is missing is how
// the views stopped compiling with nothing here saying so.
func TestEveryPublishedViewCompiles(t *testing.T) {
	root := packageRoot(t)
	project := t.TempDir()

	publications, err := foundation.Publications(module(t))
	if err != nil {
		t.Fatalf("reading what the module publishes: %v", err)
	}
	var published int
	for _, publication := range publications {
		from := publication.From
		if from == "" {
			from = "."
		}
		err := fs.WalkDir(publication.Files, from, func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			relative := name
			if from != "." {
				relative = strings.TrimPrefix(strings.TrimPrefix(name, from), "/")
			}
			body, err := fs.ReadFile(publication.Files, name)
			if err != nil {
				return err
			}
			target := filepath.Join(project, filepath.FromSlash(path.Join(publication.To, relative)))
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			published++
			return os.WriteFile(target, body, 0o644)
		})
		if err != nil {
			t.Fatalf("publishing the %s files: %v", publication.Tag, err)
		}
	}
	if published == 0 {
		t.Fatal("the module publishes no file, so this gate would compile nothing")
	}

	// The application's go.mod names this package and points it at the
	// checkout, so the views compile against the code beside them rather than
	// against a release. The sums come from here: the application needs no
	// module this package does not already require.
	goMod := readReleaseFile(t, root, "go.mod")
	language := captureReleaseValue(t, goMod, `(?m)^go ([0-9.]+)$`, "go directive in go.mod")
	modulePath := captureReleaseValue(t, goMod, `(?m)^module (\S+)$`, "module path in go.mod")
	manifest := "module example.com/application\n\n" +
		"go " + language + "\n\n" +
		"require " + modulePath + " v0.0.0-00010101000000-000000000000\n\n" +
		"replace " + modulePath + " => " + strconv.Quote(root) + "\n"
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "go.sum"), []byte(readReleaseFile(t, root, "go.sum")), 0o644); err != nil {
		t.Fatal(err)
	}

	inProject(t, project, "the published views do not compile with "+viewCompiler+", so no application that publishes them can build",
		"go", "run", viewCompiler, "view:build")
	inProject(t, project, "the views "+viewCompiler+" compiled do not build against this package",
		"go", "build", "./...")

	// Compiling is not drawing. Each screen is rendered once, with a row of
	// every kind and every action switched on, under a layout that draws the
	// content section alone -- so a page that compiles and then fails on its
	// own data, or draws a key instead of a sentence, fails here.
	probe := filepath.Join(project, "renderprobe")
	if err := os.MkdirAll(probe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probe, "main.go"), []byte(renderProbe), 0o644); err != nil {
		t.Fatal(err)
	}
	inProject(t, project, "a published view does not render the page data its handler hands it",
		"go", "run", "./renderprobe")
}

// renderProbe renders every screen with sample data and checks what it drew.
const renderProbe = `package main

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/arandu-io/hesape/view"
	"github.com/arandu-io/kyse/components"
	billing "github.com/hyz-is/arandu-billing"

	_ "example.com/application/storage/framework/views/modules/billing"
	_ "example.com/application/storage/framework/views/modules/billing/admin"
)

func main() {
	view.RegisterLayout("layouts.app", func(w io.Writer, _ any, sections map[string]func(io.Writer) error) error {
		return sections["content"](w)
	})
	urls := billing.NewURLs("/painel", "/admin/cobranca", "", billing.PortuguesePaths)
	sc := billing.Screen{Labels: billing.Labels{Locale: "pt-BR"}, URLs: urls}
	table := billing.TableState{Page: 1, Pages: 3, Total: 61, Status: "active", URL: "/painel/assinaturas"}
	subscription := billing.SubscriptionRow{ID: "s1", Tenant: "acme", Plan: "Câmera mensal", Reference: "camera:42", Status: "Ativa",
		StatusKey: "active", PaidUntil: "23/11/2026", Started: "23/10/2026", Serving: true, Cancelling: true,
		RenewURL: urls.Renew("s1"), CancelURL: urls.Cancel("s1"), AdminCancelURL: urls.AdminCancel("s1")}
	invoice := billing.InvoiceRow{ID: "i1", Tenant: "acme", Plan: "Câmera semestral", Period: "6 meses", Subtotal: "R$ 600,00",
		Discount: "R$ 60,00", Total: "R$ 540,00", Coupon: "BEMVINDO", Status: "Em aberto", StatusKey: "open", Created: "23/10/2026",
		Paid: "—", Provider: "gateway", Installments: "em até 6x", Open: true, URL: urls.Invoice("i1"),
		PayURL: "https://pagamento.example/i1", ConfirmURL: urls.AdminConfirm("i1"), VoidURL: urls.AdminVoid("i1")}
	pages := []struct {
		name string
		data any
		want []string
	}{
		{billing.ViewPlans, billing.PlansPageData{Screen: sc, CanSubscribe: true, Plans: []billing.PlanCard{{ID: "p1", Name: "Câmera semestral",
			Description: "Uma câmera por seis meses", Price: "R$ 600,00", Period: "6 meses", Installments: "ou em até 6x de R$ 100,00",
			Features: []string{"Leituras ilimitadas"}, TrialDays: 7, CheckoutURL: urls.Checkout("p1"), TrialURL: urls.Trial("p1")}}},
			[]string{"Câmera semestral", "R$ 600,00", "ou em até 6x de R$ 100,00", "Leituras ilimitadas", "/painel/planos/p1/assinar",
				"/painel/planos/p1/teste-gratis", "Assinar", "Testar grátis", "coupon"}},
		{billing.ViewSubscriptions, billing.SubscriptionsPageData{Screen: sc, Rows: []billing.SubscriptionRow{subscription}, Table: table, CanManage: true},
			[]string{"Câmera mensal", "23/11/2026", "/painel/assinaturas/s1/renovacao", "/painel/assinaturas/s1/cancelamento", "DELETE",
				"Manter assinatura", "termina no fim do período", "Buscar em Assinaturas"}},
		{billing.ViewInvoices, billing.InvoicesPageData{Screen: sc, Rows: []billing.InvoiceRow{invoice}, Table: table},
			[]string{"R$ 540,00", "Em aberto", "https://pagamento.example/i1", "/painel/faturas/i1"}},
		{billing.ViewInvoice, billing.InvoicePageData{Screen: sc, Invoice: invoice},
			[]string{"R$ 540,00", "Como pagar", "https://pagamento.example/i1", "em até 6x"}},
		{billing.ViewInvoice, billing.InvoicePageData{Screen: sc, Invoice: billing.InvoiceRow{ID: "i2", Plan: "Mensal", Open: true}, Instructions: "Pix: chave 00.000.000/0001-00"},
			[]string{"Pix: chave 00.000.000/0001-00"}},
		{billing.ViewAdminPlans, billing.AdminPlansPageData{Screen: sc, Table: table, StoreURL: urls.AdminPlans(),
			NewPlan: billing.PlanForm{Active: true, IntervalMonths: "1"},
			Rows: []billing.PlanRow{{ID: "p1", Name: "Câmera semestral", Slug: "camera-semestral", Price: "R$ 600,00", Period: "6 meses",
				Installments: "6x", Trial: "7 dias", Status: "Ativo", Active: true, URL: urls.AdminPlan("p1")}}},
			[]string{"camera-semestral", "/admin/cobranca/planos/p1", "Novo plano", "price_brl", "installments_from", "features"}},
		{billing.ViewAdminPlan, billing.AdminPlanPageData{Screen: sc, UpdateURL: urls.AdminPlan("p1"), BackURL: urls.AdminPlans(),
			Plan: billing.PlanForm{Name: "Câmera semestral", Price: "600,00", IntervalMonths: "6", Features: "Leituras ilimitadas", Meta: "cameras=1", Active: true}},
			[]string{"Editar plano", "600,00", "cameras=1", "/admin/cobranca/planos/p1"}},
		{billing.ViewAdminCoupons, billing.AdminCouponsPageData{Screen: sc, Table: table, StoreURL: urls.AdminCoupons(),
			PlanOptions: []components.SelectOption{{Value: "", Label: "Todos os planos"}},
			Rows: []billing.CouponRow{{ID: "c1", Code: "BEMVINDO", Discount: "10%", Uses: "3 / 100", Validity: "— — —", Renewals: "2",
				Plan: "Todos os planos", Minimum: "—", Status: "Ativo", Active: true, ToggleURL: urls.AdminCoupon("c1")}}},
			[]string{"BEMVINDO", "3 / 100", "Desativar", "Novo cupom", "/admin/cobranca/cupons/c1", "min_purchase_brl"}},
		{billing.ViewAdminSubscriptions, billing.AdminSubscriptionsPageData{Screen: sc, Table: table, Rows: []billing.SubscriptionRow{subscription}},
			[]string{"acme", "Cancelar agora", "/admin/cobranca/assinaturas/s1/cancelamento"}},
		{billing.ViewAdminInvoices, billing.AdminInvoicesPageData{Screen: sc, Table: table, Rows: []billing.InvoiceRow{invoice}},
			[]string{"acme", "BEMVINDO", "Confirmar pagamento", "/admin/cobranca/faturas/i1/pagamento", "/admin/cobranca/faturas/i1/cancelamento", "paid_on"}},
	}
	failed := false
	for _, p := range pages {
		rec := httptest.NewRecorder()
		if err := view.NewRenderer().Render(context.Background(), rec, 200, p.name, p.data); err != nil {
			fmt.Printf("%s: %v\n", p.name, err)
			failed = true
			continue
		}
		body := rec.Body.String()
		for _, want := range p.want {
			if !strings.Contains(body, want) {
				fmt.Printf("%s: the page does not say %q\n", p.name, want)
				failed = true
			}
		}
		if strings.Contains(body, "billing.screen.") || strings.Contains(body, "billing.field.") || strings.Contains(body, "billing.control.") {
			fmt.Printf("%s: the page draws a catalogue key instead of its sentence\n", p.name)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
}
`

// inProject runs one command in the scratch project, outside any workspace,
// and fails with what it printed.
//
// -mod=mod lets the go command fill in the requirements the application's
// go.mod leaves to this package's, which is what an application's own `go mod
// tidy` would have written.
func inProject(t *testing.T, dir, failure string, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS="+strings.TrimSpace(os.Getenv("GOFLAGS")+" -mod=mod"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s:\n$ %s %s\n%s", failure, name, strings.Join(args, " "), output)
	}
}
