package unit_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/arandu-io/framework/validation"
	hview "github.com/arandu-io/hesape/view"

	billing "github.com/hyz-is/arandu-billing"
)

// These tests hold the catalogue to what the package draws. A key one locale
// has and another does not is a screen that shows its key; a line left in
// English in pt-BR is the screen a Brazilian product shows its customers in
// English; a message the code writes that no line reads back is a message no
// locale ever translates.

// englishLeak are words a sentence in Portuguese never carries, and every
// validation message the framework, this package or arandu-wallet writes in
// English does.
var englishLeak = regexp.MustCompile(`(?i)\b(is|are|must|required|not|be|the|cannot|choose|could|has|an|than|and|or|of|to|valid)\b`)

// sameInBothLanguages are the lines that are spelled alike in English and in
// Brazilian Portuguese: acronyms, a dash, and a placeholder alone.
var sameInBothLanguages = map[string]bool{
	"billing.field.subtotal":       true,
	"billing.field.total":          true,
	"billing.screen.invoice_line":  true, // :plan — :months
	"billing.control.search_label": false,
}

func TestEveryShippedLocaleHasEveryKey(t *testing.T) {
	t.Parallel()

	locales := billing.Locales()
	if len(locales) < 2 || !contains(locales, "en") || !contains(locales, "pt-BR") {
		t.Fatalf("the package ships %v; want en and pt-BR at least", locales)
	}
	reference := billing.Lines("en")
	for _, locale := range locales {
		lines := billing.Lines(locale)
		for key := range reference {
			if strings.TrimSpace(lines[key]) == "" {
				t.Errorf("%s has no line for %s", locale, key)
			}
		}
		for key := range lines {
			if _, ok := reference[key]; !ok {
				t.Errorf("%s has %s, which en does not", locale, key)
			}
		}
	}
}

func TestThePortugueseCatalogueLeavesNothingInEnglish(t *testing.T) {
	t.Parallel()

	en, pt := billing.Lines("en"), billing.Lines("pt-BR")
	for _, key := range sortedKeys(pt) {
		line := pt[key]
		if line == en[key] && !sameInBothLanguages[key] {
			t.Errorf("pt-BR %s is the English line %q", key, line)
		}
		if strings.HasPrefix(key, "billing.validation.") || strings.HasPrefix(key, "billing.report.") ||
			strings.HasPrefix(key, "billing.screen.") || strings.HasPrefix(key, "billing.control.") {
			if word := englishLeak.FindString(withoutPlaceholders(line)); word != "" {
				t.Errorf("pt-BR %s says %q, which is English: %q", key, word, line)
			}
		}
	}
}

func TestEveryPlaceholderOfALineIsOneTheEnglishLineFills(t *testing.T) {
	t.Parallel()

	en := billing.Lines("en")
	for _, locale := range billing.Locales() {
		for key, line := range billing.Lines(locale) {
			for _, name := range placeholders(line) {
				if !contains(placeholders(en[key]), name) {
					t.Errorf("%s %s uses :%s, which the English line %q does not carry", locale, key, name, en[key])
				}
			}
		}
	}
}

// TestEveryValidationLineIsReadBackToItsOwnKey writes each English message the
// way the code does, placeholders filled, and holds that pt-BR draws it from
// the same key -- so no line is shadowed by a looser one -- and that English
// draws it back unchanged, so an API client reads what it always read.
func TestEveryValidationLineIsReadBackToItsOwnKey(t *testing.T) {
	t.Parallel()

	en, pt := billing.Lines("en"), billing.Lines("pt-BR")
	english, portuguese := billing.Labels{Locale: "en"}, billing.Labels{Locale: "pt-BR"}
	for _, key := range sortedKeys(en) {
		item, ok := strings.CutPrefix(key, "billing.validation.")
		if !ok || item == "summary" {
			continue
		}
		values := map[string]string{}
		translated := map[string]string{}
		for i, name := range placeholders(en[key]) {
			values[name] = "VALUE" + strconv.Itoa(i)
			translated[name] = values[name]
		}
		msg := fill(en[key], values)
		if got := english.Message(msg); got != msg {
			t.Errorf("%s: English reads %q back as %q", item, msg, got)
		}
		if got, want := portuguese.Message(msg), fill(pt[key], translated); got != want {
			t.Errorf("%s: pt-BR reads %q as %q, want %q", item, msg, got, want)
		}
	}
}

// TestTheFrameworkValidationMessagesReadInPortuguese holds the half of the
// catalogue this package does not write: the sentences the framework's
// helpers produce. A helper that rewords one is a test that fails here, not a
// screen that turns English in production.
func TestTheFrameworkValidationMessagesReadInPortuguese(t *testing.T) {
	t.Parallel()

	errs := validation.Errors{}
	validation.Required(errs, "name", "")
	validation.MinLen(errs, "branch", "1", 3)
	validation.MaxLen(errs, "pix_key", strings.Repeat("x", 9), 4)
	validation.Email(errs, "email", "ana")
	want := map[string]string{
		"name":    "Preencha este campo.",
		"branch":  "Use pelo menos 3 caracteres.",
		"pix_key": "Use no máximo 4 caracteres.",
		"email":   "Informe um e-mail válido.",
	}
	got := billing.Labels{Locale: "pt-BR"}.Errors(errs)
	for field, message := range want {
		if len(got[field]) != 1 || got[field][0] != message {
			t.Errorf("%s reads %v in pt-BR, want %q (from %v)", field, got[field], message, errs[field])
		}
	}
	if errs["name"][0] != "is required" {
		t.Errorf("Errors changed the value it was given: %v", errs["name"])
	}
	english := billing.Labels{Locale: "en"}.Errors(errs)
	for field := range want {
		if english[field][0] != errs[field][0] {
			t.Errorf("%s reads %q in English, want the framework's own %q", field, english[field][0], errs[field][0])
		}
	}
}

func TestTheErrorBannerNamesEachFieldByItsLabel(t *testing.T) {
	t.Parallel()

	errs := validation.Errors{"plan_id": {"is not a plan of the catalogue"}, "coupon": {"is required"}}
	pt := billing.Screen{Page: hview.Page{Errors: errs}, Labels: billing.Labels{Locale: "pt-BR"}}
	want := []string{
		"Cupom: Preencha este campo.",
		"Plano: Não é um plano do catálogo.",
	}
	if got := pt.All(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("the pt-BR banner reads %q, want %q", got, want)
	}
	if got := pt.ErrorSummary(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("ErrorSummary reads %q, want what All reads, %q", got, want)
	}
	en := billing.Screen{Page: hview.Page{Errors: errs}, Labels: billing.Labels{Locale: "en"}}
	if got := en.All(); len(got) != 2 || got[0] != "Coupon is required" {
		t.Errorf("the English banner reads %q", got)
	}
	if got := (billing.Screen{}).All(); got != nil {
		t.Errorf("a screen with nothing rejected draws a banner: %q", got)
	}
}

// outsideFields are inputs refused somewhere other than this package's source
// whose messages reach the same screens. None, today.
var outsideFields = []string{}

// TestEveryMessageTheSourceWritesComesFromTheCatalogue reads the package's
// own code: a message added to validation.Errors as a literal is one no
// locale draws, so every message goes through message(key), every key it
// names is in every catalogue, and every field it refuses has a label.
func TestEveryMessageTheSourceWritesComesFromTheCatalogue(t *testing.T) {
	t.Parallel()

	root := packageRoot(t)
	files, err := filepath.Glob(filepath.Join(root, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]bool{}
	fields := map[string]bool{}
	for _, field := range outsideFields {
		fields[field] = true
	}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, source, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		name := filepath.Base(path)
		ast.Inspect(file, func(node ast.Node) bool {
			switch n := node.(type) {
			case *ast.CallExpr:
				if key, ok := messageKey(n); ok {
					keys[key] = true
				}
				callee := calleeName(n.Fun)
				switch {
				case callee == "Add" && len(n.Args) == 2 && isStringLiteral(n.Args[0]):
					fields[unquote(n.Args[0])] = true
					if _, ok := messageKey(n.Args[1]); !ok {
						t.Errorf("%s adds a message to %s that is not message(key)", name, unquote(n.Args[0]))
					}
				case validationHelpers[callee] && len(n.Args) >= 2 && isStringLiteral(n.Args[1]):
					fields[unquote(n.Args[1])] = true
				}
			case *ast.CompositeLit:
				if calleeName(n.Type) != "Errors" {
					return true
				}
				for _, element := range n.Elts {
					pair, ok := element.(*ast.KeyValueExpr)
					if !ok || !isStringLiteral(pair.Key) {
						continue
					}
					fields[unquote(pair.Key)] = true
					messages, ok := pair.Value.(*ast.CompositeLit)
					if !ok {
						continue
					}
					for _, msg := range messages.Elts {
						if _, ok := messageKey(msg); !ok {
							t.Errorf("%s writes a message for %s that is not message(key)", name, unquote(pair.Key))
						}
					}
				}
			}
			return true
		})
	}
	if len(keys) < 20 {
		t.Fatalf("found %d message keys in the source; the reading is broken", len(keys))
	}
	for _, locale := range billing.Locales() {
		lines := billing.Lines(locale)
		for key := range keys {
			if lines["billing.validation."+key] == "" {
				t.Errorf("the code writes message(%q) and %s has no line for it", key, locale)
			}
		}
	}
	pt := billing.Labels{Locale: "pt-BR"}
	for field := range fields {
		humanized := strings.ToUpper(field[:1]) + strings.ReplaceAll(field[1:], "_", " ")
		if pt.Field(field) == humanized {
			t.Errorf("the field %s has no label in the catalogue, so the banner names it %q", field, humanized)
		}
	}
}

// validationHelpers are the framework helpers that take the field as their
// second argument.
var validationHelpers = map[string]bool{"Required": true, "MaxLen": true, "MinLen": true, "Email": true, "NotZero": true}

func messageKey(expr ast.Expr) (string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	ident, ok := call.Fun.(*ast.Ident)
	if !ok || ident.Name != "message" || len(call.Args) == 0 || !isStringLiteral(call.Args[0]) {
		return "", false
	}
	return unquote(call.Args[0]), true
}

func calleeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.Ident:
		return e.Name
	}
	return ""
}

func isStringLiteral(expr ast.Expr) bool {
	lit, ok := expr.(*ast.BasicLit)
	return ok && lit.Kind == token.STRING
}

func unquote(expr ast.Expr) string {
	s, _ := strconv.Unquote(expr.(*ast.BasicLit).Value)
	return s
}

var placeholderPattern = regexp.MustCompile(`:([A-Za-z][A-Za-z0-9_]*)`)

func placeholders(line string) []string {
	var out []string
	for _, m := range placeholderPattern.FindAllStringSubmatch(line, -1) {
		out = append(out, m[1])
	}
	return out
}

func withoutPlaceholders(line string) string { return placeholderPattern.ReplaceAllString(line, "") }

// fill writes the values into a line, the longest name first so :max never
// eats the front of :maxlength.
func fill(line string, values map[string]string) string {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	for _, name := range names {
		line = strings.ReplaceAll(line, ":"+name, values[name])
	}
	return line
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
