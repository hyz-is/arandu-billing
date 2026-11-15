package unit_test

import (
	"testing"

	"github.com/arandu-io/kyse/components"

	billing "github.com/hyz-is/arandu-billing"
)

// TestTheTableStateKeepsWhatTheRequestAsked reads a list's state the way a data
// table does: the address of the next step carries the search, the status, the
// order and the columns switched off, so a step never loses one of them.
func TestTheTableStateKeepsWhatTheRequestAsked(t *testing.T) {
	t.Parallel()

	state := billing.TableState{
		Query: "camera", Status: "paid", Sort: "total", Dir: "desc", Page: 2, Pages: 4, Total: 90, Rows: 25, PerPage: 25,
		Hidden: []string{"provider"}, URL: "/painel/faturas",
	}.State()

	got := state.Values(components.DataTableChange{Page: 3})
	if got.Get("q") != "camera" || got.Get("sort") != "total" || got.Get("dir") != "desc" || got.Get("status") != "paid" || got.Get("page") != "3" {
		t.Errorf("the next page lost the search, the status or the order: %v", got)
	}
	if hide := got["hide"]; len(hide) != 1 || hide[0] != "provider" {
		t.Errorf("the next page lost the hidden column: %v", got)
	}
	if got.Get("per_page") != "" {
		t.Errorf("the page size in force is the default and is left out: %v", got)
	}
}

// TestTheTablePagerKnowsWhereTheListStands is the footer's source: the page
// being shown and how many rows the whole list holds.
func TestTheTablePagerKnowsWhereTheListStands(t *testing.T) {
	t.Parallel()

	pager := billing.TableState{Page: 2, Pages: 4, Total: 90, Rows: 25, PerPage: 25, URL: "/painel/faturas"}.Pager()
	if !pager.HasPages() || pager.OnFirstPage() || !pager.HasMorePages() {
		t.Errorf("page 2 of 4 reads as first %v, last %v, several %v", pager.OnFirstPage(), !pager.HasMorePages(), pager.HasPages())
	}
}
