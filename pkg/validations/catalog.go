package validations

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
	// Phase names the implementation slice that delivers the check.
	Phase string
}

// Catalog returns every check this package defines or reserves, sorted by ID.
// Checks that are not implemented yet are included, carrying StatusDeferred, so
// that the catalog describes the whole published check vocabulary rather than
// only the part that happens to exist.
//
// The result is built fresh on each call: a caller may keep or modify it
// without affecting any other caller. Catalog is safe for concurrent use.
func Catalog() []CheckInfo {
	return []CheckInfo{
		{
			ID: IDCascadeRules,
			// Whether this is better modeled as a fact than a check is an open
			// question, deferred to the phase-1c plan along with the rest of the
			// foreign-key material.
			Rationale: "ON DELETE CASCADE removes rows from tables the caller never named.",
			Status:    StatusDeferred,
			Phase:     "1c",
		},
		{
			ID:        IDFKClosure,
			Rationale: "A foreign key pointing into the table set from outside it can block or cascade a delete the caller did not account for.",
			Status:    StatusDeferred,
			Phase:     "1c",
		},
		{
			ID:        IDFKIndexed,
			Rationale: "An unindexed foreign key column turns every referential check into a full scan of the child table.",
			Status:    StatusDeferred,
			Phase:     "1c",
		},
		{
			ID:        IDFKMetadataVisibility,
			Rationale: "Foreign key metadata is exposed only to accounts privileged on the child table, so incoming-key discovery cannot be proven complete without global SELECT.",
			Status:    StatusDeferred,
			Phase:     "1c",
		},
		{
			ID:        IDInvisibleColumns,
			Rationale: "SELECT * omits invisible columns, so their values are dropped from any copy and from any verification hash, and then deleted from the source.",
			Status:    StatusImplemented,
			Phase:     "1b",
		},
		{
			ID:        IDPKExists,
			Rationale: "Without a primary key no column provably identifies one row, so any delete by key over-matches.",
			Status:    StatusImplemented,
			Phase:     "1b",
		},
		{
			ID:        IDPKIntegerType,
			Rationale: "Checkpointing advances an ordered numeric high-water mark, which a UUID, varchar, decimal, or datetime key cannot be resumed from.",
			Status:    StatusImplemented,
			Phase:     "1b",
		},
		{
			ID:        IDPKMatchesExpected,
			Rationale: "A column that is not the primary key is probably not unique, so deleting by it over-matches.",
			Status:    StatusImplemented,
			Phase:     "1b",
		},
		{
			ID:        IDPKNameCase,
			Rationale: "information_schema.COLUMNS.COLUMN_NAME collates case-insensitively, so a configured log_id silently finds an actual LOG_ID.",
			Status:    StatusImplemented,
			Phase:     "1b",
		},
		{
			ID:        IDPKSingleColumn,
			Rationale: "A composite key cannot be filtered by one column without over-matching rows outside the intended set.",
			Status:    StatusImplemented,
			Phase:     "1b",
		},
		{
			ID:        IDSchemaPrivileges,
			Rationale: "Schema-level work fails partway through if the account lacks the privilege, after the operation has already begun.",
			Status:    StatusDeferred,
			Phase:     "1c",
		},
		{
			ID:        IDStorageEngine,
			Rationale: "Non-transactional engines cannot provide the integrity a copy-verify-delete cycle depends on; MyISAM discards the transaction silently.",
			Status:    StatusImplemented,
			Phase:     "1b",
		},
		{
			ID:        IDTablesExist,
			Rationale: "A configured table that does not exist fails at execution time, after the operation has already begun.",
			Status:    StatusImplemented,
			Phase:     "1b",
		},
		{
			ID:        IDTablePrivileges,
			Rationale: "An operation that discovers a missing table privilege at execution time fails after it has already begun.",
			Status:    StatusDeferred,
			Phase:     "1c",
		},
		{
			ID:        IDTriggersPresent,
			Rationale: "A trigger's logic is invisible to the caller; one that touches other tables produces effects outside the operation's model and outside its verification.",
			Status:    StatusImplemented,
			Phase:     "1b",
		},
	}
}

// LookupCheck returns the catalog entry for id, reporting whether one exists.
// Matching is exact: identifiers are upper-case and are never folded.
//
// LookupCheck is safe for concurrent use.
func LookupCheck(id string) (CheckInfo, bool) {
	for _, entry := range Catalog() {
		if entry.ID == id {
			return entry, true
		}
	}

	return CheckInfo{}, false
}
