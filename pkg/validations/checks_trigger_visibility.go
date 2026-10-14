package validations

// CheckTriggerVisibility reports each requested table whose trigger metadata
// the account is not proven to see in full.
//
// The server lists a table's triggers only to an account holding TRIGGER on
// that table, so without that privilege an empty Triggers result for the table
// cannot be told from "no triggers" (docs/COMPAT.md entry 34). For each table
// the TRIGGER state is resolved exactly as CheckTablePrivileges resolves a
// table privilege, and only GrantPresent passes. Every other state — absent,
// unconfirmed, unknown (a zero Grants included), or undeclared — yields one
// finding carrying a PrivilegeFact for PrivilegeTrigger, so consumers branch on
// State and never on message wording. Findings preserve requested table order
// and duplicates; nil or empty tables returns nil.
//
// A pass is a sufficient proof, not a necessary one: a role-held or pattern
// grant may work on the server while this check reports it unconfirmed. A pass
// does not prove the table exists, that a trigger read succeeded, or that the
// grants and the trigger read are one snapshot. Read Grants and Triggers as the
// same account in the same session, with no role change between them; a later
// REVOKE or DDL is not covered. TRIGGER also permits creating and dropping
// triggers on the table, and no read-only privilege exposes trigger rows.
//
// CheckTriggerVisibility performs no I/O and is safe for concurrent use when
// tables is not mutated concurrently.
func CheckTriggerVisibility(grants Grants, schema string, tables []string) []Finding {
	memo := &schemaScanMemo{}
	var findings []Finding
	for _, table := range tables {
		state := grants.tableState(schema, table, PrivilegeTrigger, memo)
		if state == GrantPresent {
			continue
		}
		findings = append(findings, Finding{
			Check: IDTriggerMetadataVisibility,
			Message: findingMessage(
				IDTriggerMetadataVisibility,
				triggerVisibilitySummary(state),
			),
			Tables: []string{table},
			Facts: PrivilegeFact{
				Schema: schema, Table: table, Privilege: PrivilegeTrigger, State: state,
			},
		})
	}

	return findings
}

// triggerVisibilitySummary never says the server hides triggers: a role can
// make rows visible that the grants fact cannot prove.
func triggerVisibilitySummary(state GrantState) string {
	const summary = "trigger metadata completeness is not established"
	switch state {
	case GrantAbsent:
		return summary + ": the TRIGGER privilege is absent"
	case GrantUnconfirmed:
		return summary + ": the TRIGGER privilege is unconfirmed"
	default:
		return summary + ": the TRIGGER privilege state is unknown"
	}
}
