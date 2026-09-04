package validations

// CheckTablePrivileges reports each requested table for which priv is absent,
// unconfirmed, or unknown.
//
// Discovering a missing privilege only after an operation starts can leave
// partial work behind. Findings preserve requested table order and duplicates,
// and carry the exact GrantState so consumers do not parse message wording.
// CheckTablePrivileges is safe for concurrent use when tables is not mutated
// concurrently.
func CheckTablePrivileges(
	grants Grants,
	schema string,
	tables []string,
	priv Privilege,
) []Finding {
	memo := &schemaScanMemo{}
	var findings []Finding
	for _, table := range tables {
		state := grants.tableState(schema, table, priv, memo)
		if state == GrantPresent {
			continue
		}
		fact := PrivilegeFact{
			Schema: schema, Table: table, Privilege: priv, State: state,
		}
		findings = append(findings, Finding{
			Check: IDTablePrivileges,
			Message: findingMessage(
				IDTablePrivileges,
				privilegeFindingSummary("table", state),
			),
			Tables: []string{table},
			Facts:  fact,
		})
	}

	return findings
}

// CheckSchemaPrivileges reports each requested privilege that is absent,
// unconfirmed, or unknown for schema.
//
// Schema-level work can fail after it begins if a required privilege is not
// established. Findings preserve requested privilege order and duplicates and
// carry the exact GrantState. CheckSchemaPrivileges is safe for concurrent use
// when privs is not mutated concurrently.
func CheckSchemaPrivileges(
	grants Grants,
	schema string,
	privs []Privilege,
) []Finding {
	var findings []Finding
	for _, priv := range privs {
		state := grants.Schema(schema, priv)
		if state == GrantPresent {
			continue
		}
		fact := PrivilegeFact{
			Schema: schema, Privilege: priv, State: state,
		}
		findings = append(findings, Finding{
			Check: IDSchemaPrivileges,
			Message: findingMessage(
				IDSchemaPrivileges,
				privilegeFindingSummary("schema", state),
			),
			Facts: fact,
		})
	}

	return findings
}

func privilegeFindingSummary(scope string, state GrantState) string {
	switch state {
	case GrantAbsent:
		return scope + " privilege is absent"
	case GrantUnconfirmed:
		return scope + " privilege is unconfirmed"
	default:
		return scope + " privilege state is unknown"
	}
}
