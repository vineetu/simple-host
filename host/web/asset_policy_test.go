package web

import (
	"github.com/vsriram/simple-host/host/edition"
)

// testAssetPipeline builds the shared pipeline for a platform test. It
// installs no product files: a platform test never depends on a product.
// Tests of a product's own files and callbacks live with that product
// (products/hack/servertest goes through the real extension).
func testAssetPipeline(ed *edition.Edition, base string) *AssetPipeline {
	return NewAssetPipeline(ed, base)
}
