package validations

// Finding reports that a check's predicate was not satisfied. It carries no
// severity; whether the finding matters is the consumer's decision.
//
// Finding is safe for concurrent reads. Callers must synchronize mutations to
// its Tables slice or to mutable data stored in Facts.
type Finding struct {
	// Check is a stable identifier from Catalog.
	Check string `json:"check"`
	// Message is human-readable and includes the check's rationale. Its wording
	// is not part of the compatibility contract.
	Message string `json:"message"`
	// Tables names the affected objects using the spelling supplied by the
	// server, except for TABLES_EXIST, where no server spelling exists. An
	// FK_CLOSURE finding for an external key names the child table unqualified;
	// its schema is in Facts as ForeignKey.ChildSchema, and is the requested
	// schema for a same-schema child outside the target set. The FK_CLOSURE
	// finding for incomplete visibility instead lists the requested targets,
	// with the MetadataVisibility value in Facts. For TABLE_PRIVILEGES it echoes
	// the caller's requested spelling, since the check never reads a server row
	// for the table; TestPrivilegeChecksTotalStateTable pins that exception.
	Tables []string `json:"tables"`
	// Facts is the typed payload that caused the finding. It is nil only for a
	// missing table.
	Facts any `json:"facts"`
}

func findingMessage(id, summary string) string {
	info, ok := LookupCheck(id)
	if !ok {
		return summary
	}

	return summary + ". " + info.Rationale
}
