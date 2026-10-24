//go:build kyse

package billing

import (
	"github.com/arandu-io/kyse/components"

	billing "github.com/hyz-is/arandu-billing"
)

@go
// PlansData is what the handler hands this page.
type PlansData = billing.PlansPageData

// couponField is the coupon a customer types before paying.
func couponField(data PlansData, id string) components.FieldProps {
	return components.FieldProps{
		Name: "coupon", ID: "coupon-" + id, Label: data.Labels.T("field.coupon"),
		Placeholder: data.Labels.T("screen.coupon_placeholder"), Page: data.Form(),
	}
}
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-6xl px-4 py-8 sm:px-6 lg:px-8">
	{!! .AccountNav("plans") !!}

	<header class="mt-6">
		<h1 class="text-2xl font-semibold tracking-tight">{{ .Labels.T("screen.plans_title") }}</h1>
		<p class="text-muted-foreground mt-1 max-w-prose text-sm">{{ .Labels.T("screen.plans_lead") }}</p>
	</header>

	@if(len(.Plans) == 0)
	<div class="mt-6">{!! components.Empty(components.EmptyProps{Title: .Labels.T("screen.plans_empty")}) !!}</div>
	@endif

	<div class="mt-6 grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
		@foreach(.Plans as plan)
		<article class="card flex flex-col gap-4 p-5">
			<div>
				<h2 class="text-lg font-semibold">{{ plan.Name }}</h2>
				<p class="text-muted-foreground mt-1 text-sm">{{ plan.Description }}</p>
			</div>
			<p><span class="text-3xl font-semibold tracking-tight">{{ plan.Price }}</span> <span class="text-muted-foreground text-sm">/ {{ plan.Period }}</span></p>
			@if(plan.Installments != "")
			<p class="text-muted-foreground text-sm">{{ plan.Installments }}</p>
			@endif
			<ul class="grid gap-1 text-sm">
				@foreach(plan.Features as feature)
				<li>{{ feature }}</li>
				@endforeach
			</ul>
			@if(.CanSubscribe)
			<form class="mt-auto grid gap-2" method="post" action="{{ plan.CheckoutURL }}">
				@csrf
				{!! components.Field(couponField(., plan.ID)) !!}
				{!! components.Button(components.ButtonProps{Label: .Labels.T("control.subscribe"), Type: "submit"}) !!}
			</form>
			@if(plan.TrialDays > 0)
			<form method="post" action="{{ plan.TrialURL }}">
				@csrf
				{!! components.Button(components.ButtonProps{Label: .Labels.T("control.start_trial"), Type: "submit", Variant: "outline"}) !!}
			</form>
			@endif
			@endif
		</article>
		@endforeach
	</div>
</section>
@endsection
