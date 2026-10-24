//go:build kyse

package admin

import (
	"github.com/arandu-io/kyse/components"

	billing "github.com/hyz-is/arandu-billing"
)

@go
// PlanData is what the handler hands this page.
type PlanData = billing.AdminPlanPageData
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-4xl px-4 py-8 sm:px-6 lg:px-8">
	{!! .AdminNav("plans") !!}
	<header class="mt-6 flex flex-wrap items-end justify-between gap-4">
		<div>
			<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.admin_plan_title") }}</h1>
			<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.admin_plan_lead") }}</p>
		</div>
		{!! components.Link(components.LinkProps{Label: .Labels.T("control.back"), URL: .BackURL, Variant: "hover"}) !!}
	</header>

	<form class="card mt-6 grid gap-3 p-5" method="post" action="{{ .UpdateURL }}">
		@csrf
		<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
			@foreach(planFields(.Labels, .Plan, .Form()) as field)
			{!! components.Field(field) !!}
			@endforeach
		</div>
		{!! components.Textarea(components.TextareaProps{Name: "description", Label: .Labels.T("field.description"), Value: .Plan.Description, Rows: 2, Page: .Form()}) !!}
		{!! components.Textarea(components.TextareaProps{Name: "features", Label: .Labels.T("field.features"), Value: .Plan.Features, Hint: .Labels.T("screen.features_hint"), Rows: 4, Page: .Form()}) !!}
		{!! components.Textarea(components.TextareaProps{Name: "meta", Label: .Labels.T("field.meta"), Value: .Plan.Meta, Hint: .Labels.T("screen.meta_hint"), Rows: 3, Page: .Form()}) !!}
		{!! components.Checkbox(components.CheckboxProps{Name: "active", Label: .Labels.T("field.active"), Value: "1", Checked: .Plan.Active, Page: .Form()}) !!}
		<div>{!! components.Button(components.ButtonProps{Label: .Labels.T("control.save"), Type: "submit"}) !!}</div>
	</form>
</section>
@endsection
