//go:build kyse

package billing

import (
	"html/template"

	"github.com/arandu-io/kyse/components"

	billing "github.com/hyz-is/arandu-billing"
)

@go
// SubscriptionsData is what the handler hands this page.
type SubscriptionsData = billing.SubscriptionsPageData

// statusVariant is the badge a status is drawn with.
func statusVariant(key string) string {
	switch key {
	case "active", "paid":
		return ""
	case "past_due", "failed":
		return "destructive"
	case "canceled", "expired":
		return "outline"
	}
	return "secondary"
}

// subscriptionsTable is the account's subscriptions, paged by the server.
func subscriptionsTable(data SubscriptionsData) components.DataTableProps {
	rows := make([]components.TableRow, 0, len(data.Rows))
	for _, s := range data.Rows {
		actions := template.HTML("")
		if data.CanManage {
			actions = data.CellForm(s.RenewURL, "post", data.Labels.T("control.renew"), "")
			if s.Serving && !s.Cancelling {
				actions += data.CellForm(s.CancelURL, "post", data.Labels.T("control.cancel"), "outline")
			}
			if s.Serving && s.Cancelling {
				actions += data.CellForm(s.CancelURL, "delete", data.Labels.T("control.resume"), "outline")
			}
		}
		status := components.Badge(components.BadgeProps{Label: s.Status, Variant: statusVariant(s.StatusKey)})
		if s.Cancelling {
			status += template.HTML(` <span class="text-muted-foreground text-xs">` + template.HTMLEscapeString(data.Labels.T("screen.cancelling")) + `</span>`)
		}
		rows = append(rows, components.TableRow{Key: s.ID, Cells: []components.TableCell{
			{Text: s.Plan},
			{Text: s.Reference},
			{HTML: status},
			{Text: s.PaidUntil},
			{Text: s.Started},
			{HTML: actions},
		}})
	}
	return components.DataTableProps{
		ID: "billing-subscriptions", Label: data.Labels.T("screen.subscriptions_title"), URL: data.Table.URL,
		ColumnsLabel: data.Labels.T("control.columns"), Lines: data.TableLines(),
		Query: data.Table.Query, SearchName: "q", SearchPlaceholder: data.Labels.T("control.search_placeholder"),
		SearchLabel: data.SearchLabel(data.Labels.T("screen.subscriptions_title")),
		Filters: data.StatusFilter(data.SubscriptionStatusOptions(), data.Table.Status),
		SortKey: data.Table.Sort, SortDir: data.Table.Dir,
		Page: data.Table.Page, Pages: data.Table.Pages, Total: data.Table.Total,
		Columns: []components.TableColumn{
			{Label: data.Labels.T("field.plan"), Key: "plan"},
			{Label: data.Labels.T("field.reference"), Key: "reference", Hideable: true},
			{Label: data.Labels.T("field.status"), Key: "status", Sortable: true},
			{Label: data.Labels.T("field.paid_until"), Key: "paid_until", Sortable: true},
			{Label: data.Labels.T("field.created_at"), Key: "created_at", Sortable: true, Hideable: true},
			{Label: "", Key: "actions"},
		},
		Rows:  rows,
		Empty: components.EmptyProps{Title: data.Labels.T("screen.subscriptions_empty")},
	}
}
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
	{!! .AccountNav("subscriptions") !!}
	<header class="mt-6">
		<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.subscriptions_title") }}</h1>
		<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.subscriptions_lead") }}</p>
	</header>
	<div class="mt-6">{!! components.DataTable(subscriptionsTable(.)) !!}</div>
</section>
@endsection
