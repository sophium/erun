// Nested module so this Windows-only test stub stays out of the erun-ui
// module's `go build ./...` graph. Built on demand by fixtures/seedRoot.ts.
module erun-ui-playwright-winstub

go 1.26
