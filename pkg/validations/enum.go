package validations

// unknownEnum is what the zero value of an exported enumeration in this package
// renders as. It is shared so that PKKind and TriggerEvent cannot drift into
// spelling "not populated" two different ways. grantStateUnconfirmedName is
// shared for the same reason between GrantState and MetadataVisibility.
const (
	unknownEnum               = "unknown"
	pkKindNone                = "none"
	grantStateUnconfirmedName = "unconfirmed"
)
