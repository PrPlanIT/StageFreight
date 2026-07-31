// Package render holds scribe's element producers.
//
// Modules are pluggable content producers (badges, shields, text, include, component,
// build-contents, k8s-inventory, props) that render to inline markdown for a single
// stencil. Composition — joining elements into rows/regions — is NOT here: a scribe
// files region is rendered by scribe.renderRegion (items sugar) or the stencil engine
// (a freeform body:). This package only defines the producer interface and its concrete
// implementations.
package render

// Module produces inline markdown content for a single element.
type Module interface {
	Render() string
}
