// Package cppstudio holds the shipped model catalogue. Embedding the same
// tracked file preserves model aliases in standalone configurations without
// maintaining a second catalogue in handlers.
package cppstudio

import _ "embed"

// ModelManifest is the default model metadata; it contains no model weights.
//
//go:embed models.json
var ModelManifest []byte
