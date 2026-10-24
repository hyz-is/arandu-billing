//go:build kyse

package admin

import (
	"github.com/arandu-io/kyse/components"

	billing "github.com/hyz-is/arandu-billing"
)

@go
// CouponsData is what the handler hands this page.
type CouponsData = billing.AdminCouponsPageData

// couponsTable is the coupons, paged, searched and ordered by the server.
func couponsTable(data CouponsData) components.DataTableProps {
	rows := make([]components.TableRow, 0, len(data.Rows))
	for _, c := range data.Rows {
		variant, label, next := "outline", data.Labels.T("control.activate"), "1"
		if c.Active {
			variant, label, next = "", data.Labels.T("control.deactivate"), "0"
		}
		rows = append(rows, components.TableRow{Key: c.ID, Cells: []components.TableCell{
			{Text: c.Code},
			{Text: c.Discount},
			{Text: c.Uses},
			{Text: c.Validity},
			{Text: c.Renewals},
			{Text: c.Plan},
			{Text: c.Minimum},
			{HTML: components.Badge(components.BadgeProps{Label: c.Status, Variant: variant})},
			{HTML: data.CellForm(c.ToggleURL, "post", label, "outline", billing.CellInput{Name: "active", Type: "hidden", Value: next})},
		}})
	}
	return components.DataTableProps{
		ID: "billing-admin-coupons", Label: data.Labels.T("screen.admin_coupons_title"), URL: data.Table.URL,
		ColumnsLabel: data.Labels.T("control.columns"), Lines: data.TableLines(),
		Query: data.Table.Query, SearchName: "q", SearchPlaceholder: data.Labels.T("control.search_placeholder"),
		SearchLabel: data.SearchLabel(data.Labels.T("screen.admin_coupons_title")),
		Filters: data.StatusFilter(data.ActiveOptions(), data.Table.Status),
		SortKey: data.Table.Sort, SortDir: data.Table.Dir,
		Page: data.Table.Page, Pages: data.Table.Pages, Total: data.Table.Total,
		Columns: []components.TableColumn{
			{Label: data.Labels.T("field.code"), Key: "code", Sortable: true},
			{Label: data.Labels.T("field.value"), Key: "value", Sortable: true},
			{Label: data.Labels.T("field.uses"), Key: "uses", Sortable: true},
			{Label: data.Labels.T("field.validity"), Key: "valid_until", Sortable: true, Hideable: true},
			{Label: data.Labels.T("field.renewals"), Key: "renewals", Hideable: true},
			{Label: data.Labels.T("field.plan"), Key: "plan", Hideable: true},
			{Label: data.Labels.T("field.min_purchase"), Key: "minimum", Hideable: true, Hidden: true},
			{Label: data.Labels.T("field.status"), Key: "status"},
			{Label: "", Key: "toggle"},
		},
		Rows:  rows,
		Empty: components.EmptyProps{Title: data.Labels.T("screen.coupons_empty")},
	}
}
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
	{!! .AdminNav("coupons") !!}
	<header class="mt-6">
		<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.admin_coupons_title") }}</h1>
		<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.admin_coupons_lead") }}</p>
	</header>

	<div class="mt-6">{!! components.DataTable(couponsTable(.)) !!}</div>

	<form class="card mt-8 grid gap-3 p-5" method="post" action="{{ .StoreURL }}">
		@csrf
		<h2 class="text-base font-semibold">{{ .Labels.T("screen.new_coupon") }}</h2>
		<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
			{!! components.Field(components.FieldProps{Name: "code", Label: .Labels.T("field.code"), Required: true, Page: .Form()}) !!}
			{!! components.Select(components.SelectProps{Name: "kind", Label: .Labels.T("field.kind"), Options: .CouponKindOptions(), Required: true, Page: .Form()}) !!}
			{!! components.Field(components.FieldProps{Name: "value", Label: .Labels.T("field.value"), Hint: .Labels.T("screen.coupon_value_hint"), Placeholder: "10", Required: true, Page: .Form()}) !!}
			{!! components.Select(components.SelectProps{Name: "plan_id", Label: .Labels.T("field.plan"), Options: .PlanOptions, Page: .Form()}) !!}
			{!! components.Field(components.FieldProps{Name: "max_uses", Label: .Labels.T("field.max_uses"), Type: "number", Value: "0", Hint: .Labels.T("screen.max_uses_hint"), Page: .Form()}) !!}
			{!! components.Field(components.FieldProps{Name: "renewals", Label: .Labels.T("field.renewals"), Type: "number", Value: "0", Hint: .Labels.T("screen.renewals_hint"), Page: .Form()}) !!}
			{!! components.Field(components.FieldProps{Name: "min_purchase_brl", Label: .Labels.T("field.min_purchase"), Placeholder: "0,00", Page: .Form()}) !!}
			{!! components.Field(components.FieldProps{Name: "valid_from", Label: .Labels.T("field.valid_from"), Type: "date", Page: .Form()}) !!}
			{!! components.Field(components.FieldProps{Name: "valid_until", Label: .Labels.T("field.valid_until"), Type: "date", Page: .Form()}) !!}
			{!! components.Field(components.FieldProps{Name: "description", Label: .Labels.T("field.description"), Page: .Form()}) !!}
		</div>
		{!! components.Checkbox(components.CheckboxProps{Name: "active", Label: .Labels.T("field.active"), Value: "1", Checked: true, Page: .Form()}) !!}
		<div>{!! components.Button(components.ButtonProps{Label: .Labels.T("control.save"), Type: "submit"}) !!}</div>
	</form>
</section>
@endsection
