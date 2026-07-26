package validations

import (
	"errors"
	"fmt"
	"testing"
)

func TestObjectErrorTableAttributed(t *testing.T) {
	t.Parallel()

	cause := errors.New("lookup failed")
	err := &ObjectError{
		Op:     "primary_keys",
		Schema: "odd`schema",
		Table:  "odd`table",
		Err:    cause,
	}

	const want = "validations: primary_keys on `odd``schema`.`odd``table`: lookup failed"
	if got := err.Error(); got != want {
		t.Errorf("ObjectError.Error() = %q, want %q", got, want)
	}
}

func TestObjectErrorSchemaWide(t *testing.T) {
	t.Parallel()

	err := &ObjectError{
		Op:     "tables",
		Schema: "shop",
		Err:    errors.New("query failed"),
	}

	const want = "validations: tables in schema `shop`: query failed"
	if got := err.Error(); got != want {
		t.Errorf("ObjectError.Error() = %q, want %q", got, want)
	}
}

func TestObjectErrorEmptySchema(t *testing.T) {
	t.Parallel()

	err := &ObjectError{
		Op:  "tables",
		Err: ErrEmptySchema,
	}

	const want = "validations: tables: validations: empty schema name"
	if got := err.Error(); got != want {
		t.Errorf("ObjectError.Error() = %q, want %q", got, want)
	}
}

func TestObjectErrorUnwrap(t *testing.T) {
	t.Parallel()

	cause := errors.New("driver failed")
	objectErr := &ObjectError{
		Op:     "triggers",
		Schema: "shop",
		Err:    cause,
	}
	err := fmt.Errorf("outer one: %w", fmt.Errorf("outer two: %w", objectErr))

	if !errors.Is(err, cause) {
		t.Errorf("errors.Is(%v, cause) = false, want true", err)
	}

	var got *ObjectError
	if !errors.As(err, &got) {
		t.Fatalf("errors.As(%v, *ObjectError) = false, want true", err)
	}
	if got != objectErr {
		t.Errorf("errors.As extracted %p, want %p", got, objectErr)
	}
	if got.Unwrap() == nil {
		t.Error("ObjectError.Unwrap() = nil; ObjectError.Err must remain reachable")
	}
}
