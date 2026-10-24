//go:build kyse

package billing

import (
	"html/template"

	"github.com/arandu-io/kyse/components"

	billing "github.com/hyz-is/arandu-billing"
)

@go
// InvoicesData is what the handler hands this page.
type InvoicesData = billing.InvoicesPageData

// invoicesTable is the account's invoices, paged by the server.
func invoicesTable(data InvoicesData) components.DataTableProps {
	rows := make([]components.TableRow, 0, len(data.Rows))
	for _, i := range data.Rows {
		action := components.Link(components.LinkProps{Label: data.Labels.T("control.open"), URL: i.URL, Variant: "hover"})
		if i.PayURL != "" {
			action += template.HTML(" · ") + components.Link(components.LinkProps{Label: data.Labels.T("control.pay"), URL: i.PayURL, Variant: "hover"})
		}
		rows = append(rows, components.TableRow{Key: i.ID, Cells: []components.TableCell{
			{Text: i.Created},
			{Text: i.Plan},
			{Text: i.Period},
			{Text: i.Discount},
			{Text: i.Total},
			{HTML: components.Badge(components.BadgeProps{Label: i.Status, Variant: statusVariant(i.StatusKey)})},
			{Text: i.Paid},
			{HTML: action},
		}})
	}
	return components.DataTableProps{
		ID: "billing-invoices", Label: data.Labels.T("screen.invoices_title"), URL: data.Table.URL,
		ColumnsLabel: data.Labels.T("control.columns"), Lines: data.TableLines(),
		Query: data.Table.Query, SearchName: "q", SearchPlaceholder: data.Labels.T("control.search_placeholder"),
		SearchLabel: data.SearchLabel(data.Labels.T("screen.invoices_title")),
		Filters: data.StatusFilter(data.InvoiceStatusOptions(), data.Table.Status),
		SortKey: data.Table.Sort, SortDir: data.Table.Dir,
		Page: data.Table.Page, Pages: data.Table.Pages, Total: data.Table.Total,
		Columns: []components.TableColumn{
			{Label: data.Labels.T("field.created_at"), Key: "created_at", Sortable: true},
			{Label: data.Labels.T("field.plan"), Key: "plan"},
			{Label: data.Labels.T("field.months"), Key: "months", Hideable: true},
			{Label: data.Labels.T("field.discount"), Key: "discount", Hideable: true, Align: "right"},
			{Label: data.Labels.T("field.total"), Key: "total", Sortable: true, Align: "right"},
			{Label: data.Labels.T("field.status"), Key: "status", Sortable: true},
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
<section class="mx-auto w-full max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
	{!! .AccountNav("invoices") !!}
	<header class="mt-6">
		<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.invoices_title") }}</h1>
		<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.invoices_lead") }}</p>
	</header>
	<div class="mt-6">{!! components.DataTable(invoicesTable(.)) !!}</div>
</section>
@endsection
