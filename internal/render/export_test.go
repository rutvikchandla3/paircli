package render

import "flag"

// update rewrites the golden files in testdata/golden when set:
//
//	go test ./internal/render/... -update
var update = flag.Bool("update", false, "rewrite golden files")
