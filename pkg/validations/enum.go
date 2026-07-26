package validations

// unknownEnum is what the zero value of an exported enumeration in this package
// renders as. It is shared so that PKKind and TriggerEvent cannot drift into
// spelling "not populated" two different ways.
const unknownEnum = "unknown"
