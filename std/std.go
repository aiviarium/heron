// Package std embeds the standard packages, which are Heron source. They're
// read and checked like any other package; the embedded copy is the fallback
// a newer copy from the registry replaces.
package std

import "embed"

// FS holds every standard package as <name>/package.heron and the files it
// includes, such as doc/package.heron for std/doc.
//
//go:embed */*.heron docs/icons
var FS embed.FS
