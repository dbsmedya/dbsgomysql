package replication

// Finding reports that a check's predicate was not satisfied. It carries no
// severity; whether the finding matters is the consumer's decision.
//
// Finding is safe for concurrent reads. Callers must synchronize mutations to
// its Channels slice or to mutable data stored in Facts.
type Finding struct {
	// Check is a stable identifier from Catalog.
	Check string `json:"check"`
	// Message is human-readable and includes the check's rationale. Its wording
	// is not part of the compatibility contract.
	Message string `json:"message"`
	// Channels names the affected replication channels using the spelling
	// supplied by the server, where the empty name is the default channel. It
	// is nil for server-scoped checks, which concern no channel in particular,
	// and encodes as JSON null.
	Channels []string `json:"channels"`
	// Facts is the typed payload that caused the finding.
	Facts any `json:"facts"`
}

func findingMessage(id, summary string) string {
	info, ok := LookupCheck(id)
	if !ok {
		return summary
	}

	return summary + ". " + info.Rationale
}
