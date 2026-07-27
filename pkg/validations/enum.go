package validations

// unknownEnum is what the zero value of an exported enumeration in this package
// renders as. It is shared so that PKKind, TriggerEvent, and GeneratedKind
// cannot drift into spelling "not populated" three different ways.
// enumNoneName and grantStateUnconfirmedName are shared for the same reason —
// enumNoneName between PKKind and GeneratedKind, grantStateUnconfirmedName
// between GrantState and MetadataVisibility.
const (
	unknownEnum               = "unknown"
	enumNoneName              = "none"
	grantStateUnconfirmedName = "unconfirmed"
)
