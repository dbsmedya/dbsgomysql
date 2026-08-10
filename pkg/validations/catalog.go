package validations

import "strconv"

// Stable check identifiers. A finding names the check that produced it with one
// of these values, so they are part of the compatibility contract: consumers
// branch on them rather than on message text.
const (
	IDCascadeRules         = "CASCADE_RULES"
	IDFKClosure            = "FK_CLOSURE"
	IDFKIndexed            = "FK_INDEXED"
	IDFKMetadataVisibility = "FK_METADATA_VISIBILITY"
	IDInvisibleColumns     = "INVISIBLE_COLUMNS"
	IDPKExists             = "PK_EXISTS"
	IDPKIntegerType        = "PK_INTEGER_TYPE"
	IDPKMatchesExpected    = "PK_MATCHES_EXPECTED"
	IDPKNameCase           = "PK_NAME_CASE"
	IDPKSingleColumn       = "PK_SINGLE_COLUMN"
	IDSchemaPrivileges     = "SCHEMA_PRIVILEGES"
	IDStorageEngine        = "STORAGE_ENGINE"
	IDTablesExist          = "TABLES_EXIST"
	IDTablePrivileges      = "TABLE_PRIVILEGES"
	IDTriggersPresent      = "TRIGGERS_PRESENT"
)

// CheckStatus records whether a cataloged check is callable in this release.
// The zero value is not a valid status, so an unpopulated CheckInfo is
// detectable.
//
// CheckStatus is a plain value and is safe for concurrent use.
type CheckStatus uint8

const (
	// StatusImplemented means the check is callable now.
	StatusImplemented CheckStatus = iota + 1
	// StatusDeferred means the identifier is reserved and documented but the
	// check is not implemented yet. Its Phase names the slice that delivers it.
	StatusDeferred
)

// String returns the status's name. CheckStatus has no declared zero-value
// member, so 0 renders as CheckStatus(0) rather than as "unknown" — that
// spelling is reserved for types with a declared unknown member, and using it
// here would present an unpopulated CheckInfo as though it were a real,
// nameable status.
//
// String is safe for concurrent use.
func (s CheckStatus) String() string {
	switch s {
	case StatusImplemented:
		return "implemented"
	case StatusDeferred:
		return "deferred"
	default:
		return "CheckStatus(" + strconv.Itoa(int(s)) + ")"
	}
}

// CheckInfo describes one cataloged check. It carries no severity; whether a
// finding matters is the consumer's decision, and what the library offers
// instead is Rationale.
//
// CheckInfo is a plain value and is safe for concurrent use.
type CheckInfo struct {
	// ID is the stable identifier a finding carries.
	ID string
	// Rationale is the failure mode the check protects against, in one
	// sentence. Each check's own documentation carries the longer form.
	Rationale string
	// Status reports whether the check is callable in this release.
	Status CheckStatus
	// Phase names the implementation slice that delivered the check.
	Phase string
}

// Catalog returns every check this package defines or reserves, sorted by ID.
// The catalog describes the whole published check vocabulary.
//
// The result is built fresh on each call: a caller may keep or modify it
// without affecting any other caller. Catalog is safe for concurrent use.
func Catalog() []CheckInfo {
	return []CheckInfo{
		checkInfo(IDCascadeRules),
		checkInfo(IDFKClosure),
		checkInfo(IDFKIndexed),
		checkInfo(IDFKMetadataVisibility),
		checkInfo(IDInvisibleColumns),
		checkInfo(IDPKExists),
		checkInfo(IDPKIntegerType),
		checkInfo(IDPKMatchesExpected),
		checkInfo(IDPKNameCase),
		checkInfo(IDPKSingleColumn),
		checkInfo(IDSchemaPrivileges),
		checkInfo(IDStorageEngine),
		checkInfo(IDTablesExist),
		checkInfo(IDTablePrivileges),
		checkInfo(IDTriggersPresent),
	}
}

// checkInfo is the allocation-free source of truth shared by Catalog,
// LookupCheck, and finding message assembly. Keeping the rationales here avoids
// rebuilding the whole catalog for every finding.
func checkInfo(id string) CheckInfo {
	switch id {
	case IDCascadeRules:
		return CheckInfo{
			ID: id, Rationale: "ON DELETE CASCADE removes rows from tables the caller never named.",
			Status: StatusImplemented, Phase: "1c",
		}
	case IDFKClosure:
		return CheckInfo{
			ID: id, Rationale: "A foreign key pointing into the table set from outside it can block or cascade a delete the caller did not account for.",
			Status: StatusImplemented, Phase: "1c",
		}
	case IDFKIndexed:
		return CheckInfo{
			ID: id, Rationale: "MySQL guarantees a leftmost-prefix child index for every foreign key and refuses to drop it, so a finding means the fact did not come from a conforming InnoDB source.",
			Status: StatusImplemented, Phase: "1c",
		}
	case IDFKMetadataVisibility:
		return CheckInfo{
			ID: id, Rationale: "Without a successful PROCESS-gated InnoDB metadata query, the visibility-filtered fallback cannot prove incoming-key discovery complete.",
			Status: StatusImplemented, Phase: "1c",
		}
	case IDInvisibleColumns:
		return CheckInfo{
			ID: id, Rationale: "SELECT * omits invisible columns, so their values are dropped from any copy and from any verification hash, and then deleted from the source.",
			Status: StatusImplemented, Phase: "1b",
		}
	case IDPKExists:
		return CheckInfo{
			ID: id, Rationale: "Without a primary key no column provably identifies one row, so any delete by key over-matches.",
			Status: StatusImplemented, Phase: "1b",
		}
	case IDPKIntegerType:
		return CheckInfo{
			ID: id, Rationale: "Checkpointing advances an ordered numeric high-water mark, which a UUID, varchar, decimal, or datetime key cannot be resumed from.",
			Status: StatusImplemented, Phase: "1b",
		}
	case IDPKMatchesExpected:
		return CheckInfo{
			ID: id, Rationale: "A column that is not the primary key is probably not unique, so deleting by it over-matches.",
			Status: StatusImplemented, Phase: "1b",
		}
	case IDPKNameCase:
		return CheckInfo{
			ID: id, Rationale: "information_schema.COLUMNS.COLUMN_NAME collates case-insensitively, so a configured log_id silently finds an actual LOG_ID.",
			Status: StatusImplemented, Phase: "1b",
		}
	case IDPKSingleColumn:
		return CheckInfo{
			ID: id, Rationale: "A composite key cannot be filtered by one column without over-matching rows outside the intended set.",
			Status: StatusImplemented, Phase: "1b",
		}
	case IDSchemaPrivileges:
		return CheckInfo{
			ID: id, Rationale: "Schema-level work fails partway through if the account lacks the privilege, after the operation has already begun.",
			Status: StatusImplemented, Phase: "1c",
		}
	case IDStorageEngine:
		return CheckInfo{
			ID: id, Rationale: "Non-transactional engines cannot provide the integrity a copy-verify-delete cycle depends on; MyISAM discards the transaction silently.",
			Status: StatusImplemented, Phase: "1b",
		}
	case IDTablesExist:
		return CheckInfo{
			ID: id, Rationale: "A configured table that does not exist fails at execution time, after the operation has already begun.",
			Status: StatusImplemented, Phase: "1b",
		}
	case IDTablePrivileges:
		return CheckInfo{
			ID: id, Rationale: "An operation that discovers a missing table privilege at execution time fails after it has already begun.",
			Status: StatusImplemented, Phase: "1c",
		}
	case IDTriggersPresent:
		return CheckInfo{
			ID: id, Rationale: "A trigger's logic is invisible to the caller; one that touches other tables produces effects outside the operation's model and outside its verification.",
			Status: StatusImplemented, Phase: "1b",
		}
	default:
		return CheckInfo{}
	}
}

// LookupCheck returns the catalog entry for id, reporting whether one exists.
// Matching is exact: identifiers are upper-case and are never folded.
//
// LookupCheck is safe for concurrent use.
func LookupCheck(id string) (CheckInfo, bool) {
	entry := checkInfo(id)
	return entry, entry.ID != ""
}
