package validations

import "testing"

func TestTriggerEventZeroValueIsUnknown(t *testing.T) {
	t.Parallel()

	var zero TriggerEvent
	if zero != TriggerEventUnknown {
		t.Errorf("the TriggerEvent zero value is %d, want TriggerEventUnknown (%d); a caller who forgets "+
			"the argument must be rejected rather than defaulted into an event", zero, TriggerEventUnknown)
	}
}

func TestTriggerEventString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event TriggerEvent
		want  string
	}{
		{name: "unknown", event: TriggerEventUnknown, want: "unknown"},
		{name: "insert", event: TriggerInsert, want: "INSERT"},
		{name: "update", event: TriggerUpdate, want: "UPDATE"},
		{name: "delete", event: TriggerDelete, want: "DELETE"},
		{name: "undeclared", event: TriggerEvent(99), want: "TriggerEvent(99)"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := test.event.String(); got != test.want {
				t.Errorf("TriggerEvent(%d).String() = %q, want %q", test.event, got, test.want)
			}
		})
	}
}

// The three declared events must render exactly as MySQL spells
// EVENT_MANIPULATION, because a finding reports the event back to a consumer
// who will compare it against the server's own vocabulary.
func TestTriggerEventStringsMatchServerVocabulary(t *testing.T) {
	t.Parallel()

	for _, event := range []TriggerEvent{TriggerInsert, TriggerUpdate, TriggerDelete} {
		got := event.String()
		if got != "INSERT" && got != "UPDATE" && got != "DELETE" {
			t.Errorf("TriggerEvent(%d).String() = %q, which is not an EVENT_MANIPULATION value", event, got)
		}
	}
}

func TestTriggerEventStringsAreDistinct(t *testing.T) {
	t.Parallel()

	seen := make(map[string]TriggerEvent)
	for _, event := range []TriggerEvent{TriggerEventUnknown, TriggerInsert, TriggerUpdate, TriggerDelete} {
		got := event.String()
		if other, dup := seen[got]; dup {
			t.Errorf("TriggerEvent(%d) and TriggerEvent(%d) both render as %q", other, event, got)
		}
		seen[got] = event
	}
}
