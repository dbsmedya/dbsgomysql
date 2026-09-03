package validations

import (
	"context"
	"fmt"
	"sort"
)

// TriggerInfo describes one trigger.
//
// TriggerInfo is a plain value and is safe for concurrent use.
type TriggerInfo struct {
	// Table is the table's exact server-side spelling.
	Table string `json:"table"`
	// Name is the trigger's exact server-side spelling.
	Name string `json:"name"`
	// Event is information_schema.TRIGGERS.EVENT_MANIPULATION verbatim.
	Event string `json:"event"`
	// Timing is information_schema.TRIGGERS.ACTION_TIMING verbatim.
	Timing string `json:"timing"`
}

// Triggers returns triggers for event on requested tables. Results preserve
// requested table order and sort each table's triggers by firing order —
// BEFORE ahead of AFTER — and then by exact name, compared as bytes. Both
// halves of the order are made in Go; the server's own name order is
// case-insensitive and its timing order is a pinned observation the fact does
// not depend on. Missing or invisible tables are absent.
//
// Triggers is safe for concurrent use when the Inspector's Querier is safe for
// concurrent use and tables is not mutated concurrently.
func (i *Inspector) Triggers(
	ctx context.Context,
	tables []string,
	event TriggerEvent,
) ([]TriggerInfo, error) {
	if err := i.validate(opTriggers, tables); err != nil {
		return nil, err
	}
	serverEvent, ok := event.mysqlValue()
	if !ok {
		cause := fmt.Errorf("%w: %s", ErrInvalidTriggerEvent, event)

		return nil, newObjectError(opTriggers, i.schema, "", cause)
	}
	if len(tables) == 0 {
		return nil, nil
	}

	byTable := make(map[string][]TriggerInfo)
	// A schema rejected by representable cannot be the exact spelling of anything
	// information_schema reports, so this request is answerable without asking.
	// A supplementary character would make the fixed comparison fail with
	// ER_IMPOSSIBLE_STRING_CONVERSION (3988); invalid UTF-8 already matches
	// nothing. See representable and docs/COMPAT.md entry 8. Skipping the
	// statement preserves Triggers' absence result for both cases and repairs the
	// supplementary one.
	if representable(i.schema) {
		// ORDER BY fixes the row order the scan sees; it does not decide the
		// order the fact returns, which sortTriggers makes in Go for every
		// table (timing via triggerTimingOrder, then name as bytes) so that the
		// fact and CheckTriggersPresent agree by construction. The server's own
		// ACTION_TIMING sort — BEFORE ahead of AFTER, because the column is
		// ENUM('BEFORE','AFTER') and MySQL orders an ENUM by declaration index —
		// is a pinned server observation, not a dependency: docs/COMPAT.md
		// entry 10, TestTriggerTimingEnumOrderIntegration.
		query := `
			SELECT EVENT_OBJECT_TABLE, TRIGGER_NAME, EVENT_MANIPULATION, ACTION_TIMING
			FROM information_schema.TRIGGERS AS tr
			WHERE tr.EVENT_OBJECT_SCHEMA = ?
			  AND tr.EVENT_MANIPULATION = ?
			ORDER BY tr.EVENT_OBJECT_TABLE, tr.ACTION_TIMING, tr.TRIGGER_NAME`
		args := []any{i.schema, serverEvent}
		if requested, requestedArgs, ok := requestedObjects(i.schema, tables); ok {
			query = `
			SELECT tr.EVENT_OBJECT_TABLE, tr.TRIGGER_NAME, tr.EVENT_MANIPULATION, tr.ACTION_TIMING
			FROM ` + requested + `
			JOIN information_schema.TRIGGERS AS tr
			  ON tr.EVENT_OBJECT_SCHEMA = requested.TABLE_SCHEMA
			 AND tr.EVENT_OBJECT_TABLE = requested.TABLE_NAME
			WHERE tr.EVENT_MANIPULATION = ?
			ORDER BY tr.EVENT_OBJECT_TABLE, tr.ACTION_TIMING, tr.TRIGGER_NAME`
			args = requestedArgs
			args = append(args, serverEvent)
		}

		rows, err := i.q.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, newObjectError(opTriggers, i.schema, "", fmt.Errorf("query metadata: %w", err))
		}
		defer rows.Close()

		for rows.Next() {
			var trigger TriggerInfo
			if err := rows.Scan(&trigger.Table, &trigger.Name, &trigger.Event, &trigger.Timing); err != nil {
				return nil, newObjectError(
					opTriggers,
					i.schema,
					"",
					fmt.Errorf("scan metadata: %w", err),
				)
			}
			byTable[trigger.Table] = append(byTable[trigger.Table], trigger)
		}
		if err := rows.Err(); err != nil {
			return nil, newObjectError(opTriggers, i.schema, "", fmt.Errorf("iterate metadata: %w", err))
		}
	}
	for _, triggers := range byTable {
		sortTriggers(triggers)
	}

	found := make([]TriggerInfo, 0)
	for _, table := range tables {
		found = append(found, byTable[table]...)
	}

	return found, nil
}

// CheckTriggersPresent reports each table having a trigger for event.
//
// Trigger logic is invisible to the caller and can produce effects outside the
// operation's model and verification. The payload is sorted by timing and then
// trigger name. An event that names no event — TriggerEventUnknown or an
// undeclared value — yields one finding carrying the event as Facts and no
// Tables, because a nil result would read as passed. CheckTriggersPresent is
// safe for concurrent use when trg is not mutated concurrently.
func CheckTriggersPresent(trg []TriggerInfo, event TriggerEvent) []Finding {
	serverEvent, ok := event.mysqlValue()
	if !ok {
		// An event that names nothing cannot be answered, and a nil result
		// would read as "no triggers found" — the one outcome doc.go defines
		// as passed. The fact method rejects the same argument with
		// ErrInvalidTriggerEvent; a check has no error return, so it reports
		// the malformed question as a finding carrying the argument.
		return []Finding{{
			Check: IDTriggersPresent,
			Message: findingMessage(
				IDTriggersPresent,
				"trigger presence is unconfirmed because the requested event names no event",
			),
			Facts: event,
		}}
	}

	tableOrder := make([]string, 0)
	byTable := make(map[string][]TriggerInfo)
	for _, trigger := range trg {
		if trigger.Event != serverEvent {
			continue
		}
		if _, exists := byTable[trigger.Table]; !exists {
			tableOrder = append(tableOrder, trigger.Table)
		}
		byTable[trigger.Table] = append(byTable[trigger.Table], trigger)
	}

	var findings []Finding
	for _, table := range tableOrder {
		triggers := byTable[table]
		sortTriggers(triggers)
		findings = append(findings, Finding{
			Check: IDTriggersPresent,
			Message: findingMessage(
				IDTriggersPresent,
				"table has triggers whose effects are outside the caller's operation model",
			),
			Tables: []string{table},
			Facts:  triggers,
		})
	}

	return findings
}

// sortTriggers orders one table's triggers by firing order and then by exact
// name, compared as bytes. The fact method and CheckTriggersPresent both use
// it, so the two agree by construction: the server's ORDER BY TRIGGER_NAME
// collates case-insensitively (docs/COMPAT.md entry 2) and would put a_trg
// ahead of B_trg, which the fact's "exact name" promise does not.
func sortTriggers(triggers []TriggerInfo) {
	sort.Slice(triggers, func(left, right int) bool {
		if triggers[left].Timing != triggers[right].Timing {
			return triggerTimingOrder(triggers[left].Timing) <
				triggerTimingOrder(triggers[right].Timing)
		}

		return triggers[left].Name < triggers[right].Name
	})
}

func triggerTimingOrder(timing string) uint8 {
	switch timing {
	case triggerTimingBefore:
		return 1
	case triggerTimingAfter:
		return 2
	default:
		return 3
	}
}
