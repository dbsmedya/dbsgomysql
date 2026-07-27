package validations

import "strconv"

// PKKind classifies a table's primary key.
//
// The zero value, PKUnknown, means "not populated": a fact never returns it, so
// a PKInfo that carries it was built by something other than an inspection.
//
// PKKind is a plain value and is safe for concurrent use.
type PKKind uint8

const (
	// PKUnknown is the zero value and means the kind was never determined.
	PKUnknown PKKind = iota
	// PKNone means the table has no PRIMARY KEY.
	PKNone
	// PKSingle means the PRIMARY KEY spans exactly one column.
	PKSingle
	// PKComposite means the PRIMARY KEY spans more than one column.
	PKComposite
)

// String returns the kind's name. An undeclared value renders as PKKind(n)
// rather than as "unknown", so a garbage value is distinguishable from the zero
// value in a message.
//
// String is safe for concurrent use.
func (k PKKind) String() string {
	switch k {
	case PKUnknown:
		return unknownEnum
	case PKNone:
		return enumNoneName
	case PKSingle:
		return "single"
	case PKComposite:
		return "composite"
	default:
		return "PKKind(" + strconv.Itoa(int(k)) + ")"
	}
}
