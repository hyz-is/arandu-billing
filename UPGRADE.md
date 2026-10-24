# Upgrade Guide

## Unreleased

## v0.1.0

The first release. `go get github.com/hyz-is/arandu-billing@v0.1.0`, wire it in
`bootstrap/app.go` with the application's own `Provider` (README), run `aru
migrate`, publish the views with `aru vendor:publish --tag=view --apply` and run
`aru view:build`. There is nothing to upgrade from.
