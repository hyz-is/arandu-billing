package billing

import (
	"context"
	"embed"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/arandu-io/framework/validation"
	"github.com/arandu-io/hesape/translation"
)

// The sentences of this package's screens, messages and reports. They are
// embedded and not published: an application that wants other words writes
// them in its own catalogue, under the same keys, and Config.Translator is
// asked first. One override path, the one the application already has.

//go:embed resources/lang
var langSources embed.FS

const (
	// TranslationGroup is the file every key of this package lives in, and the
	// prefix of every key: "billing.screen.plans_title".
	TranslationGroup = "billing"
	// FallbackLocale answers a request that negotiated no locale, when
	// Config.Locale names none either, and every line a locale does not have.
	// It is English, as in the other hyz-is modules; an application that
	// speaks one language sets Config.Locale.
	FallbackLocale = "en"
)

var shipped, shippedLocales = mustShipped()

func mustShipped() (*translation.Translator, []string) {
	root, err := fs.Sub(langSources, "resources/lang")
	if err != nil {
		panic("billing: the embedded catalogue has no lang directory: " + err.Error())
	}
	loader, err := translation.NewFileLoader(root, ".")
	if err != nil {
		panic("billing: the embedded catalogue does not parse: " + err.Error())
	}
	entries, err := fs.ReadDir(root, ".")
	if err != nil {
		panic("billing: the embedded catalogue cannot be listed: " + err.Error())
	}
	locales := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			locales = append(locales, entry.Name())
		}
	}
	sort.Strings(locales)
	if len(locales) == 0 {
		panic("billing: the embedded catalogue holds no locale, so every screen would draw its keys")
	}
	return translation.New(loader, FallbackLocale, FallbackLocale), locales
}

// Locales are the languages the package ships.
func Locales() []string { return append([]string(nil), shippedLocales...) }

// Lines are the sentences this package ships for one locale, keyed the way a
// translator asks for them ("billing.validation.required"), and nil for a
// locale it does not ship. An application reads them to see what there is to
// override, or to check a catalogue of its own against them.
func Lines(locale string) translation.Lines {
	lines := shipped.GetLoader().Load(locale, TranslationGroup, "*")
	if len(lines) == 0 {
		return nil
	}
	out := make(translation.Lines, len(lines))
	for item, line := range lines {
		out[TranslationGroup+"."+item] = line
	}
	return out
}

// Labels are the sentences of this package's screens in one locale.
type Labels struct {
	Locale string
	module *Module
}

// T reads a key of this package: "screen.dashboard_title".
func (l Labels) T(key string) string { return l.line(key, nil) }

// line reads a key with its placeholders filled, asking the application's
// translator first and the shipped catalogue after it.
func (l Labels) line(key string, replace translation.Replace) string {
	full := TranslationGroup + "." + key
	if l.module != nil && l.module.cfg.Translator != nil && l.module.cfg.Translator.Has(l.Locale, full) {
		return l.module.cfg.Translator.Get(l.Locale, full, replace)
	}
	return shipped.Get(l.Locale, full, replace)
}

// has says whether any catalogue has a line for the key.
func (l Labels) has(key string) bool {
	full := TranslationGroup + "." + key
	if l.module != nil && l.module.cfg.Translator != nil && l.module.cfg.Translator.Has(l.Locale, full) {
		return true
	}
	return shipped.Has(l.Locale, full) || shipped.Has(FallbackLocale, full)
}

// InvoiceStatus is the label of an invoice's status.
func (l Labels) InvoiceStatus(s InvoiceStatus) string { return l.T("invoice_status." + string(s)) }

// SubscriptionStatus is the label of a subscription's status.
func (l Labels) SubscriptionStatus(s SubscriptionStatus) string {
	return l.T("subscription_status." + string(s))
}

// Months is a period in months as a sentence: "1 mês", "12 meses".
func (l Labels) Months(n int) string {
	if n == 1 {
		return l.line("screen.one_month", nil)
	}
	return l.line("screen.months", translation.Replace{"count": strconv.Itoa(n)})
}

// describeInvoice is the line a gateway's page shows for an invoice: the plan
// and its months.
func (l Labels) describeInvoice(plan string, months int) string {
	return l.line("screen.invoice_line", translation.Replace{"plan": plan, "months": l.Months(months)})
}

// fieldAliases are the inputs whose label is written under another name.
var fieldAliases = map[string]string{
	"plan_id":          "plan",
	"price_brl":        "price",
	"value_brl":        "value",
	"coupon_code":      "coupon",
	"min_purchase_brl": "min_purchase",
	"paid_on":          "paid_at",
}

// Field is what a person reads in place of an input's name, in a summary of
// the rejected fields: "coupon" reads "Cupom". A field with no line reads
// the way hesape's page would have drawn it.
func (l Labels) Field(name string) string {
	key := name
	if alias, ok := fieldAliases[name]; ok {
		key = alias
	}
	if l.has("field." + key) {
		return l.T("field." + key)
	}
	words := strings.ReplaceAll(name, "_", " ")
	if words == "" {
		return words
	}
	return strings.ToUpper(words[:1]) + words[1:]
}

// Message is one validation message in the labels' locale.
//
// The messages are written in English where they are produced -- by this
// package from its own English lines, by the framework's validation helpers
// ("is required") and by arandu-wallet -- because validation.Errors carries
// sentences and not keys, and an API client reads them as they always were.
// Here each one is read back against the English lines of the "validation"
// group, placeholders and all, and drawn again from the same key in the
// locale asked for. A message no line matches comes back as it was.
func (l Labels) Message(msg string) string {
	for _, p := range messagePatterns {
		found := p.re.FindStringSubmatch(msg)
		if found == nil {
			continue
		}
		replace := translation.Replace{}
		for i, name := range p.names {
			replace[name] = l.param(name, found[i+1])
		}
		return l.line(validationGroup+p.key, replace)
	}
	return msg
}

// param is a value read out of an English message, as the locale's sentence
// carries it.
func (l Labels) param(_, value string) string { return value }

// Errors are the messages of a rejected input in the labels' locale, field by
// field. The value given is not changed.
func (l Labels) Errors(errs validation.Errors) validation.Errors {
	if errs == nil {
		return nil
	}
	out := make(validation.Errors, len(errs))
	for field, messages := range errs {
		translated := make([]string, 0, len(messages))
		for _, msg := range messages {
			translated = append(translated, l.Message(msg))
		}
		out[field] = translated
	}
	return out
}

// Summary is one line per rejected message, the field named before it, for
// the banner above a form: "Cupom: Preencha este campo.". Nil when
// nothing was rejected, so a layout that ranges over it draws no banner.
func (l Labels) Summary(errs validation.Errors) []string {
	if len(errs) == 0 {
		return nil
	}
	out := make([]string, 0, len(errs))
	for _, field := range errs.Keys() {
		for _, msg := range errs.Get(field) {
			out = append(out, l.line(validationGroup+"summary", translation.Replace{
				"field": l.Field(field), "message": l.Message(msg),
			}))
		}
	}
	return out
}

// Labels answers the sentences in a locale: the one given, else Config.Locale,
// else FallbackLocale.
func (m *Module) Labels(locale string) Labels {
	if strings.TrimSpace(locale) == "" && m != nil {
		locale = m.cfg.Locale
	}
	if strings.TrimSpace(locale) == "" {
		locale = FallbackLocale
	}
	return Labels{Locale: locale, module: m}
}

// locale is the language a request is answered in: Config.Locale when the
// application fixed one, else the one translation.Middleware or
// translation.InLocale put on the request, else FallbackLocale.
func (m *Module) locale(ctx context.Context) string {
	if m.cfg.Locale != "" {
		return m.cfg.Locale
	}
	if negotiated := translation.Locale(ctx); negotiated != "" {
		return negotiated
	}
	return FallbackLocale
}

// validationGroup is where the validation messages live in the catalogue.
const validationGroup = "validation."

// message is a validation message of this package, written from its English
// line so the reading-back in Labels.Message cannot drift from what was
// written.
func message(key string, replace ...translation.Replace) string {
	var r translation.Replace
	if len(replace) > 0 {
		r = replace[0]
	}
	return shipped.Get(FallbackLocale, TranslationGroup+"."+validationGroup+key, r)
}

// messagePattern is one English validation line turned into the expression
// that reads its placeholders back out of a message.
type messagePattern struct {
	key     string
	re      *regexp.Regexp
	names   []string
	literal int
}

// placeholderName is a placeholder of the catalogue, ":max".
var placeholderName = regexp.MustCompile(`:([A-Za-z][A-Za-z0-9_]*)`)

// messagePatterns are every English validation line, the most literal first:
// "must be a day in the form :layout, or empty" is tried before "must be a day
// in the form :layout", which would otherwise read ", or empty" as part of the
// layout.
var messagePatterns = compileMessages()

func compileMessages() []messagePattern {
	var out []messagePattern
	for item, line := range shipped.GetLoader().Load(FallbackLocale, TranslationGroup, "*") {
		key, ok := strings.CutPrefix(item, validationGroup)
		if !ok || key == "summary" {
			continue
		}
		var (
			expr    strings.Builder
			names   []string
			literal int
			last    int
		)
		expr.WriteString("^")
		for _, at := range placeholderName.FindAllStringSubmatchIndex(line, -1) {
			expr.WriteString(regexp.QuoteMeta(line[last:at[0]]))
			literal += at[0] - last
			expr.WriteString("(.+?)")
			names = append(names, line[at[2]:at[3]])
			last = at[1]
		}
		expr.WriteString(regexp.QuoteMeta(line[last:]))
		literal += len(line) - last
		expr.WriteString("$")
		out = append(out, messagePattern{key: key, re: regexp.MustCompile(expr.String()), names: names, literal: literal})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].literal != out[j].literal {
			return out[i].literal > out[j].literal
		}
		return out[i].key < out[j].key
	})
	return out
}
