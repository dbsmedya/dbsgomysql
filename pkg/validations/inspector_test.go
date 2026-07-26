package validations

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/dbsmedya/dbsgomysql/internal/testsupport"
)

func TestArgumentValidation(t *testing.T) {
	t.Parallel()

	t.Run("nil querier", func(t *testing.T) {
		t.Parallel()

		inspector := NewInspector(nil, "shop")
		_, err := inspector.Tables(t.Context(), []string{"orders"})
		assertObjectErrorCause(t, err, ErrNilQuerier, opTables, "shop")
	})

	t.Run("empty schema", func(t *testing.T) {
		t.Parallel()

		inspector := NewInspector(panicQuerier{}, "")
		_, err := inspector.PrimaryKeys(t.Context(), []string{"orders"})
		assertObjectErrorCause(t, err, ErrEmptySchema, opPrimaryKeys, "")
	})

	t.Run("empty table name", func(t *testing.T) {
		t.Parallel()

		inspector := NewInspector(panicQuerier{}, "shop")
		calls := []struct {
			name string
			call func() error
			op   string
		}{
			{
				name: "tables",
				call: func() error {
					_, err := inspector.Tables(t.Context(), []string{"orders", ""})

					return err
				},
				op: opTables,
			},
			{
				name: "primary keys",
				call: func() error {
					_, err := inspector.PrimaryKeys(t.Context(), []string{"orders", ""})

					return err
				},
				op: opPrimaryKeys,
			},
			{
				name: "invisible columns",
				call: func() error {
					_, err := inspector.InvisibleColumns(t.Context(), []string{"orders", ""})

					return err
				},
				op: opInvisibleColumns,
			},
			{
				name: "triggers",
				call: func() error {
					_, err := inspector.Triggers(t.Context(), []string{"orders", ""}, TriggerDelete)

					return err
				},
				op: opTriggers,
			},
		}

		for _, call := range calls {
			t.Run(call.name, func(t *testing.T) {
				err := call.call()
				assertObjectErrorCause(t, err, ErrEmptyTableName, call.op, "shop")
				if !strings.Contains(err.Error(), "index 1") {
					t.Errorf("error %q does not name offending table index 1", err)
				}
			})
		}
	})

	t.Run("invalid trigger event", func(t *testing.T) {
		t.Parallel()

		inspector := NewInspector(panicQuerier{}, "shop")
		for _, event := range []TriggerEvent{TriggerEventUnknown, TriggerEvent(99)} {
			_, err := inspector.Triggers(t.Context(), []string{"orders"}, event)
			assertObjectErrorCause(t, err, ErrInvalidTriggerEvent, opTriggers, "shop")
		}
	})
}

func TestEmptyTableSetsDoNotQuery(t *testing.T) {
	t.Parallel()

	inspector := NewInspector(panicQuerier{}, "shop")
	calls := []struct {
		name string
		call func() (int, error)
	}{
		{
			name: "tables",
			call: func() (int, error) {
				got, err := inspector.Tables(t.Context(), nil)

				return len(got), err
			},
		},
		{
			name: "primary keys",
			call: func() (int, error) {
				got, err := inspector.PrimaryKeys(t.Context(), nil)

				return len(got), err
			},
		},
		{
			name: "invisible columns",
			call: func() (int, error) {
				got, err := inspector.InvisibleColumns(t.Context(), nil)

				return len(got), err
			},
		},
		{
			name: "triggers",
			call: func() (int, error) {
				got, err := inspector.Triggers(t.Context(), nil, TriggerDelete)

				return len(got), err
			},
		},
	}

	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			got, err := call.call()
			if err != nil {
				t.Fatalf("empty facts call returned error: %v", err)
			}
			if got != 0 {
				t.Errorf("empty facts call returned %d facts, want 0", got)
			}
		})
	}
}

func TestFactQueryErrorsAreWrapped(t *testing.T) {
	t.Parallel()

	cause := errors.New("driver query failed")
	db := testsupport.OpenFailingDB(cause)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close failing database: %v", err)
		}
	})

	inspector := NewInspector(db, "shop")
	calls := []struct {
		name string
		call func() error
		op   string
	}{
		{
			name: "tables",
			call: func() error {
				_, err := inspector.Tables(t.Context(), []string{"orders"})

				return err
			},
			op: opTables,
		},
		{
			name: "primary keys",
			call: func() error {
				_, err := inspector.PrimaryKeys(t.Context(), []string{"orders"})

				return err
			},
			op: opPrimaryKeys,
		},
		{
			name: "invisible columns",
			call: func() error {
				_, err := inspector.InvisibleColumns(t.Context(), []string{"orders"})

				return err
			},
			op: opInvisibleColumns,
		},
		{
			name: "triggers",
			call: func() error {
				_, err := inspector.Triggers(t.Context(), []string{"orders"}, TriggerDelete)

				return err
			},
			op: opTriggers,
		},
	}

	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			err := call.call()
			assertObjectErrorCause(t, err, cause, call.op, "shop")

			var objectErr *ObjectError
			if !errors.As(err, &objectErr) {
				t.Fatalf("error has type %T, want *ObjectError", err)
			}
			if objectErr.Table != "" {
				t.Errorf("ObjectError.Table = %q, want empty for set-wide query", objectErr.Table)
			}
		})
	}
}

func TestFactContextCancellationSurvivesWrapping(t *testing.T) {
	t.Parallel()

	db := testsupport.OpenFailingDB(context.Canceled)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close failing database: %v", err)
		}
	})

	_, err := NewInspector(db, "shop").Tables(t.Context(), []string{"orders"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("errors.Is(%v, context.Canceled) = false, want true", err)
	}
}

type panicQuerier struct{}

func (panicQuerier) QueryContext(
	context.Context,
	string,
	...any,
) (*sql.Rows, error) {
	panic("QueryContext called during argument validation")
}

func (panicQuerier) QueryRowContext(
	context.Context,
	string,
	...any,
) *sql.Row {
	panic("QueryRowContext called during argument validation")
}

func assertObjectErrorCause(
	t *testing.T,
	err error,
	cause error,
	op string,
	schema string,
) {
	t.Helper()

	if err == nil {
		t.Fatal("got nil error, want *ObjectError")
	}
	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, %v) = false, want true", err, cause)
	}
	var objectErr *ObjectError
	if !errors.As(err, &objectErr) {
		t.Fatalf("error has type %T, want *ObjectError", err)
	}
	if objectErr.Op != op {
		t.Errorf("ObjectError.Op = %q, want %q", objectErr.Op, op)
	}
	if objectErr.Schema != schema {
		t.Errorf("ObjectError.Schema = %q, want %q", objectErr.Schema, schema)
	}
}
