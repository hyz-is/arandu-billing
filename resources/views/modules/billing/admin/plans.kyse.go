//go:build kyse

package admin

import (
	"github.com/arandu-io/kyse/components"

	billing "github.com/hyz-is/arandu-billing"
)

@go
// PlansData is what the handler hands this page.
type PlansData = billing.AdminPlansPageData

// plansTable is the catalogue, paged, searched and ordered by the server.
func plansTable(data PlansData) components.DataTableProps {
	rows := make([]components.TableRow, 0, len(data.Rows))
	for _, p := range data.Rows {
		variant := "outline"
		if p.Active {
			variant = ""
		}
		rows = append(rows, components.TableRow{Key: p.ID, Cells: []components.TableCell{
			{Text: p.Name},
			{Text: p.Slug},
			{Text: p.Price},
			{Text: p.Period},
			{Text: p.Installments},
			{Text: p.Trial},
			{HTML: components.Badge(components.BadgeProps{Label: p.Status, Variant: variant})},
			{HTML: components.Link(components.LinkProps{Label: data.Labels.T("control.edit"), URL: p.URL, Variant: "hover"})},
		}})
	}
	return components.DataTableProps{
		ID: "billing-admin-plans", Label: data.Labels.T("screen.admin_plans_title"), URL: data.Table.URL,
		ColumnsLabel: data.Labels.T("control.columns"), Lines: data.TableLines(),
		Query: data.Table.Query, SearchName: "q", SearchPlaceholder: data.Labels.T("control.search_placeholder"),
		SearchLabel: data.SearchLabel(data.Labels.T("screen.admin_plans_title")),
		Filters: data.StatusFilter(data.ActiveOptions(), data.Table.Status),
		SortKey: data.Table.Sort, SortDir: data.Table.Dir,
		Page: data.Table.Page, Pages: data.Table.Pages, Total: data.Table.Total,
		Columns: []components.TableColumn{
			{Label: data.Labels.T("field.name"), Key: "name", Sortable: true},
			{Label: data.Labels.T("field.slug"), Key: "slug", Hideable: true},
			{Label: data.Labels.T("field.price"), Key: "price", Sortable: true, Align: "right"},
			{Label: data.Labels.T("field.interval_months"), Key: "months", Sortable: true},
			{Label: data.Labels.T("field.installments"), Key: "installments", Hideable: true},
			{Label: data.Labels.T("field.trial_days"), Key: "trial", Hideable: true},
			{Label: data.Labels.T("field.status"), Key: "status"},
			{Label: "", Key: "edit"},
		},
		Rows:  rows,
		Empty: components.EmptyProps{Title: data.Labels.T("screen.plans_empty")},
	}
}

// planFields are the fields of a plan's form, filled with what it holds.
func planFields(labels billing.Labels, form billing.PlanForm, page billing.FormState) []components.FieldProps {
	return []components.FieldProps{
		{Name: "name", Label: labels.T("field.name"), Value: form.Name, Required: true, Page: page},
		{Name: "slug", Label: labels.T("field.slug"), Value: form.Slug, Hint: labels.T("screen.slug_hint"), Page: page},
		{Name: "price_brl", Label: labels.T("field.price"), Value: form.Price, Placeholder: "0,00", Required: true, Page: page},
		{Name: "interval_months", Label: labels.T("field.interval_months"), Type: "number", Value: form.IntervalMonths, Required: true, Page: page},
		{Name: "trial_days", Label: labels.T("field.trial_days"), Type: "number", Value: form.TrialDays, Page: page},
		{Name: "grace_days", Label: labels.T("field.grace_days"), Type: "number", Value: form.GraceDays, Page: page},
		{Name: "installments_from", Label: labels.T("field.installments_from"), Type: "number", Value: form.InstallmentsFrom, Hint: labels.T("screen.installments_from_hint"), Page: page},
		{Name: "installments_cap", Label: labels.T("field.installments_cap"), Type: "number", Value: form.InstallmentsCap, Hint: labels.T("screen.installments_cap_hint"), Page: page},
		{Name: "position", Label: labels.T("field.position"), Type: "number", Value: form.Position, Page: page},
	}
}
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-7xl px-4 py-8 sm:px-6 lg:px-8">
	{!! .AdminNav("plans") !!}
	<header class="mt-6">
		<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.admin_plans_title") }}</h1>
		<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.admin_plans_lead") }}</p>
	</header>

	<div class="mt-6">{!! components.DataTable(plansTable(.)) !!}</div>

	<form class="card mt-8 grid gap-3 p-5" method="post" action="{{ .StoreURL }}">
		@csrf
		<h2 class="text-base font-semibold">{{ .Labels.T("screen.new_plan") }}</h2>
		<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
			@foreach(planFields(.Labels, .NewPlan, .Form()) as field)
			{!! components.Field(field) !!}
			@endforeach
		</div>
		{!! components.Textarea(components.TextareaProps{Name: "description", Label: .Labels.T("field.description"), Value: .NewPlan.Description, Rows: 2, Page: .Form()}) !!}
		{!! components.Textarea(components.TextareaProps{Name: "features", Label: .Labels.T("field.features"), Value: .NewPlan.Features, Hint: .Labels.T("screen.features_hint"), Rows: 4, Page: .Form()}) !!}
		{!! components.Textarea(components.TextareaProps{Name: "meta", Label: .Labels.T("field.meta"), Value: .NewPlan.Meta, Hint: .Labels.T("screen.meta_hint"), Rows: 3, Page: .Form()}) !!}
		{!! components.Checkbox(components.CheckboxProps{Name: "active", Label: .Labels.T("field.active"), Value: "1", Checked: .NewPlan.Active, Page: .Form()}) !!}
		<div>{!! components.Button(components.ButtonProps{Label: .Labels.T("control.save"), Type: "submit"}) !!}</div>
	</form>
</section>
@endsection
