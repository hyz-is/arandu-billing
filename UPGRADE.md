# Upgrade Guide

## Unreleased

### The lists are drawn as Kyse's data table now draws them

This release builds against Kyse v0.53.0, whose `DataTable` takes one `State`
where it took a query, a sort, a page and a list of chosen values. The services,
`PageQuery`, `Page`, the routes and the JSON the lists answer do not change. What
changes is `TableState`, `Screen.StatusFilter`, the views and the catalogue.

- `TableState` gains `Rows`, the rows of the page, `PerPage`, how many a page may
  hold, and `Hidden`, the keys of the columns the reader switched off (the `hide`
  parameter). It gains two methods the views hand to the table: `State()`, the
  state every address of the table is built from, and `Pager()`, the page it
  reads its position and its total from. The old fields stay and are filled as
  before.
- `Screen.StatusFilter(options, chosen)` is `Screen.StatusFilter(options)`: the
  status chosen is the table state's now, and the filter no longer carries it.
- The six list views (`subscriptions`, `invoices`, `admin/plans`, `admin/coupons`,
  `admin/subscriptions`, `admin/invoices`) set `Searchable`, `State`, `Paginator`,
  `FullPager` and `HeaderMenu`, and no longer set `Query`, `SearchName`,
  `SortKey`, `SortDir`, `Page`, `Pages` or `Total`, which Kyse removed. A column
  that started hidden (`provider_status` of the admin invoices, `minimum` of the
  coupons) starts shown, because a table with a state hides what the state says,
  and the reader hides it from the columns menu. A project that published the
  views republishes them:

```sh
aru vendor:publish --tag=view
aru vendor:publish --tag=view --apply
aru view:build
```

- `Screen.TableLines` fills the sentences of Kyse's new table from the `table`
  block of the catalogue. `table.showing`, `table.per_page` and `table.page` are
  no longer asked for; `table.filter`, `rows`, `rows_one`, `rows_many`,
  `selected_count`, `rows_per_page`, `first`, `last`, `select_row`, `reset`,
  `clear_filters`, `no_facet_results`, `facet_selected`, `open_menu`, `actions`,
  `toggle_columns`, `asc`, `desc` and `hide` are, and ship in `en` and `pt-BR`.
  An application that overrides the table's words in its own catalogue adds the
  new keys under `billing.table.`; a line it leaves out says the English.

The lists are still searched, ordered and paged by the services with an
allowlist of sort keys, and not by a `pagination.Listing`: moving them means a
new `PageQuery` and a new `Page` for every list, which are this package's public
contract.

## v0.1.0

The first release. `go get github.com/hyz-is/arandu-billing@v0.1.0`, wire it in
`bootstrap/app.go` with the application's own `Provider` (README), run `aru
migrate`, publish the views with `aru vendor:publish --tag=view --apply` and run
`aru view:build`. There is nothing to upgrade from.
