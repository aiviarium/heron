package resolve

import "sync"

// BuiltinTypes are the primitive types of the core, which no package
// declares because everything else is declared with them. The Core format
// and Data tabs name them as core types; their meaning is in package types.
var BuiltinTypes = []string{
	"String", "Bool", "Int", "Float", "Decimal", "Bytes",
	"Any", "Object", "Null", "Prose", "List", "Set", "Map", "Self",
}

// ShapeBuiltin marks a primitive type.
const ShapeBuiltin Shape = ShapeElement + 1

// ShapeStep marks a step or an extension of a use case, named by its
// number, as `3` or `2a`.
const ShapeStep Shape = ShapeElement + 2

var (
	builtinsOnce  sync.Once
	builtinsScope *Scope
)

// Builtins returns the outermost scope: the primitive types.
func Builtins() *Scope {
	builtinsOnce.Do(func() {
		root := &Node{Shape: ShapeRoot}
		for _, n := range BuiltinTypes {
			root.add(&Node{Name: n, Shape: ShapeBuiltin})
		}
		builtinsScope = &Scope{Node: root}
	})
	return builtinsScope
}

// IsBuiltin reports whether n is a primitive type.
func IsBuiltin(n *Node) bool { return n != nil && n.Shape == ShapeBuiltin }
