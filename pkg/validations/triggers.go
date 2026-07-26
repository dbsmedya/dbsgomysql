package validations

import "strconv"

const (
	triggerEventDelete  = "DELETE"
	triggerEventInsert  = "INSERT"
	triggerEventUpdate  = "UPDATE"
	triggerTimingAfter  = "AFTER"
	triggerTimingBefore = "BEFORE"
)

// TriggerEvent is the DML event a trigger fires on.
//
// The zero value, TriggerEventUnknown, is not a usable event: a caller who
// omits the argument is rejected rather than silently defaulted into one, since
// guessing would answer a question about DELETE triggers with facts about
// INSERT ones.
type TriggerEvent uint8

const (
	// TriggerEventUnknown is the zero value and names no event.
	TriggerEventUnknown TriggerEvent = iota
	// TriggerInsert selects triggers that fire on INSERT.
	TriggerInsert
	// TriggerUpdate selects triggers that fire on UPDATE.
	TriggerUpdate
	// TriggerDelete selects triggers that fire on DELETE.
	TriggerDelete
)

// String returns the event as MySQL spells it in
// information_schema.TRIGGERS.EVENT_MANIPULATION, so the value a finding
// reports matches the server's own vocabulary. An undeclared value renders as
// TriggerEvent(n) rather than as "unknown", keeping a garbage value
// distinguishable from the zero value.
//
// String is safe for concurrent use.
func (e TriggerEvent) String() string {
	if event, ok := e.mysqlValue(); ok {
		return event
	}
	if e == TriggerEventUnknown {
		return unknownEnum
	}

	return "TriggerEvent(" + strconv.Itoa(int(e)) + ")"
}

func (e TriggerEvent) mysqlValue() (string, bool) {
	switch e {
	case TriggerInsert:
		return triggerEventInsert, true
	case TriggerUpdate:
		return triggerEventUpdate, true
	case TriggerDelete:
		return triggerEventDelete, true
	default:
		return "", false
	}
}
