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
// requested table order and sort each table's triggers by timing then exact
// name. Missing or invisible tables are absent.
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

	const query = `
		SELECT EVENT_OBJECT_TABLE, TRIGGER_NAME, EVENT_MANIPULATION, ACTION_TIMING
		FROM information_schema.TRIGGERS
		WHERE EVENT_OBJECT_SCHEMA = ?
		  AND EVENT_MANIPULATION = ?
		ORDER BY EVENT_OBJECT_TABLE, ACTION_TIMING, TRIGGER_NAME`

	rows, err := i.q.QueryContext(ctx, query, i.schema, serverEvent)
	if err != nil {
		return nil, newObjectError(opTriggers, i.schema, "", fmt.Errorf("query metadata: %w", err))
	}
	defer rows.Close()

	byTable := make(map[string][]TriggerInfo)
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
// trigger name. CheckTriggersPresent is safe for concurrent use when trg is not
// mutated concurrently.
func CheckTriggersPresent(trg []TriggerInfo, event TriggerEvent) []Finding {
	serverEvent, ok := event.mysqlValue()
	if !ok {
		return nil
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
		sort.Slice(triggers, func(left, right int) bool {
			if triggers[left].Timing != triggers[right].Timing {
				return triggerTimingOrder(triggers[left].Timing) <
					triggerTimingOrder(triggers[right].Timing)
			}

			return triggers[left].Name < triggers[right].Name
		})
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
