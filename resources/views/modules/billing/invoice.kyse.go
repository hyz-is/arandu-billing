//go:build kyse

package billing

import (
	"github.com/arandu-io/kyse/components"

	billing "github.com/hyz-is/arandu-billing"
)

@go
// InvoiceData is what the handler hands this page.
type InvoiceData = billing.InvoicePageData
@endgo

@extends('layouts.app')

@section('content')
<section class="mx-auto w-full max-w-3xl px-4 py-8 sm:px-6 lg:px-8">
	{!! .AccountNav("invoices") !!}
	<header class="mt-6 flex flex-wrap items-start justify-between gap-4">
		<div>
			<p class="text-muted-foreground text-xs font-medium uppercase tracking-wide">{{ .Labels.T("screen.invoice_title") }} · <span class="font-mono">{{ .Invoice.ID }}</span></p>
			<h1 class="mt-1 text-2xl font-semibold tracking-tight">{{ .Invoice.Plan }} · {{ .Invoice.Period }}</h1>
		</div>
		{!! components.Badge(components.BadgeProps{Label: .Invoice.Status}) !!}
	</header>

	<dl class="card mt-6 grid gap-3 p-5 text-sm sm:grid-cols-2">
		<div><dt class="text-muted-foreground">{{ .Labels.T("field.subtotal") }}</dt><dd class="font-medium">{{ .Invoice.Subtotal }}</dd></div>
		<div><dt class="text-muted-foreground">{{ .Labels.T("field.discount") }}</dt><dd class="font-medium">{{ .Invoice.Discount }}</dd></div>
		<div><dt class="text-muted-foreground">{{ .Labels.T("field.total") }}</dt><dd class="text-lg font-semibold">{{ .Invoice.Total }}</dd></div>
		<div><dt class="text-muted-foreground">{{ .Labels.T("field.installments") }}</dt><dd class="font-medium">{{ .Invoice.Installments }}</dd></div>
		<div><dt class="text-muted-foreground">{{ .Labels.T("field.created_at") }}</dt><dd class="font-medium">{{ .Invoice.Created }}</dd></div>
		<div><dt class="text-muted-foreground">{{ .Labels.T("field.paid_at") }}</dt><dd class="font-medium">{{ .Invoice.Paid }}</dd></div>
	</dl>

	@if(.Invoice.Open)
	<div class="card mt-6 grid gap-3 p-5">
		<h2 class="text-base font-semibold">{{ .Labels.T("screen.how_to_pay") }}</h2>
		@if(.Invoice.PayURL != "")
		<p class="text-sm">{{ .Labels.T("screen.pay_on_gateway") }}</p>
		<div><a class="btn" href="{{ .Invoice.PayURL }}">{{ .Labels.T("control.pay") }}</a></div>
		@else
		@if(.Instructions != "")
		<p class="whitespace-pre-line text-sm">{{ .Instructions }}</p>
		@else
		<p class="text-sm">{{ .Labels.T("screen.pay_manually") }}</p>
		@endif
		@endif
	</div>
	@endif
</section>
@endsection
