//go:build kyse

package admin

import (
	"html/template"

	"github.com/arandu-io/kyse/components"

	billing "github.com/hyz-is/arandu-billing"
)

@go
// InvoicesData is what the handler hands this page.
type InvoicesData = billing.AdminInvoicesPageData

// invoicesTable is every account's invoices, paged by the server.
func invoicesTable(data InvoicesData) components.DataTableProps {
	rows := make([]components.TableRow, 0, len(data.Rows))
	for _, i := range data.Rows {
		actions := template.HTML("")
		if i.Open {
			actions = data.CellForm(i.ConfirmURL, "post", data.Labels.T("control.confirm_payment"), "",
				billing.CellInput{Name: "method", Placeholder: data.Labels.T("field.method"), Label: data.Labels.T("field.method")},
				billing.CellInput{Name: "reference", Placeholder: data.Labels.T("field.payment_reference"), Label: data.Labels.T("field.payment_reference")},
				billing.CellInput{Name: "paid_on", Type: "date", Label: data.Labels.T("field.paid_at")})
			actions += data.CellForm(i.VoidURL, "post", data.Labels.T("control.void"), "outline")
		}
		rows = append(rows, components.TableRow{Key: i.ID, Cells: []components.TableCell{
			{Text: i.Created},
			{Text: i.Tenant},
			{Text: i.Plan},
			{Text: i.Coupon},
			{Text: i.Total},
			{HTML: components.Badge(components.BadgeProps{Label: i.Status, Variant: statusVariant(i.StatusKey)})},
			{Text: i.Provider},
			{Text: i.ProviderStatus},
			{Text: i.Paid},
			{HTML: actions},
		}})
	}
	return components.DataTableProps{
		ID: "billing-admin-invoices", Label: data.Labels.T("screen.admin_invoices_title"), URL: data.Table.URL,
		ColumnsLabel: data.Labels.T("control.columns"), Lines: data.TableLines(),
		Query: data.Table.Query, SearchName: "q", SearchPlaceholder: data.Labels.T("control.search_placeholder"),
		SearchLabel: data.SearchLabel(data.Labels.T("screen.admin_invoices_title")),
		Filters: data.StatusFilter(data.InvoiceStatusOptions(), data.Table.Status),
		SortKey: data.Table.Sort, SortDir: data.Table.Dir,
		Page: data.Table.Page, Pages: data.Table.Pages, Total: data.Table.Total,
		Columns: []components.TableColumn{
			{Label: data.Labels.T("field.created_at"), Key: "created_at", Sortable: true},
			{Label: data.Labels.T("field.tenant"), Key: "tenant", Sortable: true},
			{Label: data.Labels.T("field.plan"), Key: "plan"},
			{Label: data.Labels.T("field.coupon"), Key: "coupon", Hideable: true},
			{Label: data.Labels.T("field.total"), Key: "total", Sortable: true, Align: "right"},
			{Label: data.Labels.T("field.status"), Key: "status", Sortable: true},
			{Label: data.Labels.T("field.provider"), Key: "provider", Hideable: true},
			{Label: data.Labels.T("field.provider_status"), Key: "provider_status", Hideable: true, Hidden: true},
			{Label: data.Labels.T("field.paid_at"), Key: "paid_at", Sortable: true, Hideable: true},
			{Label: "", Key: "actions"},
		},
		Rows:  rows,
		Empty: components.EmptyProps{Title: data.Labels.T("screen.invoices_empty")},
	}
}
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
	{!! .AdminNav("invoices") !!}
	<header class="mt-6">
		<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.admin_invoices_title") }}</h1>
		<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.admin_invoices_lead") }}</p>
	</header>
	<div class="mt-6">{!! components.DataTable(invoicesTable(.)) !!}</div>
</section>
@endsection
