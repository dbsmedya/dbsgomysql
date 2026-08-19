package replication

import "strconv"

// Stable check identifiers. A finding names the check that produced it with one
// of these values, so they are part of the compatibility contract: consumers
// branch on them rather than on message text.
const (
	IDBinaryLogEnabled           = "BINARY_LOG_ENABLED"
	IDGTIDModeOn                 = "GTID_MODE_ON"
	IDReplicationChannelsRunning = "REPLICATION_CHANNELS_RUNNING"
	IDReplicationConfigured      = "REPLICATION_CONFIGURED"
	IDSecondsBehindSourceWithin  = "SECONDS_BEHIND_SOURCE_WITHIN"
)

// phaseV110 is the release that delivered every check in this catalog.
const phaseV110 = "v1.1.0"

// CheckStatus records whether a cataloged check is callable in this release.
// The zero value is not a valid status, so an unpopulated CheckInfo is
// detectable.
//
// This package reserves no identifiers: every check it catalogs is implemented,
// so StatusImplemented is the only member. There is deliberately no deferred
// status, because a reserved-but-absent check would be a promise this package
// does not make.
//
// CheckStatus is a plain value and is safe for concurrent use.
type CheckStatus uint8

// StatusImplemented means the check is callable now.
const StatusImplemented CheckStatus = 1

// String returns the status's name. CheckStatus has no declared zero-value
// member, so 0 renders as CheckStatus(0) rather than as "unknown" — that
// spelling is reserved for types with a declared unknown member, and using it
// here would present an unpopulated CheckInfo as though it were a real,
// nameable status.
//
// String is safe for concurrent use.
func (s CheckStatus) String() string {
	if s == StatusImplemented {
		return "implemented"
	}

	return "CheckStatus(" + strconv.Itoa(int(s)) + ")"
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
	// Phase names the release that delivered the check.
	Phase string
}

// Catalog returns every check this package defines, sorted by ID. The catalog
// describes the whole published check vocabulary.
//
// The result is built fresh on each call: a caller may keep or modify it
// without affecting any other caller. Catalog is safe for concurrent use.
func Catalog() []CheckInfo {
	return []CheckInfo{
		checkInfo(IDBinaryLogEnabled),
		checkInfo(IDGTIDModeOn),
		checkInfo(IDReplicationChannelsRunning),
		checkInfo(IDReplicationConfigured),
		checkInfo(IDSecondsBehindSourceWithin),
	}
}

// LookupCheck returns the catalog entry for id. The match is exact and never
// case-folded, because the identifier is the contract.
//
// LookupCheck is safe for concurrent use.
func LookupCheck(id string) (CheckInfo, bool) {
	entry := checkInfo(id)

	return entry, entry.ID != ""
}

// checkInfo is the allocation-free source of truth shared by Catalog,
// LookupCheck, and finding message assembly. Keeping the rationales here avoids
// rebuilding the whole catalog for every finding.
func checkInfo(id string) CheckInfo {
	switch id {
	case IDBinaryLogEnabled:
		return CheckInfo{
			ID: id,
			Rationale: "A server without binary logging cannot serve as a replication source " +
				"and supports no binlog-based capture or point-in-time recovery.",
			Status: StatusImplemented, Phase: phaseV110,
		}
	case IDGTIDModeOn:
		return CheckInfo{
			ID: id,
			Rationale: "Consumers that coordinate by GTID (resume, failover, CDC positioning) " +
				"need ON; in other modes the GTID sets describe only part of the write history.",
			Status: StatusImplemented, Phase: phaseV110,
		}
	case IDReplicationChannelsRunning:
		return CheckInfo{
			ID: id,
			Rationale: "A stopped channel accumulates lag and drift silently; the finding carries " +
				"any recorded last errors, which may help diagnose the stop — the server records " +
				"none for a deliberate stop, and Connecting is a failing state that may carry no " +
				"error.",
			Status: StatusImplemented, Phase: phaseV110,
		}
	case IDReplicationConfigured:
		return CheckInfo{
			ID: id,
			Rationale: "A consumer expecting to monitor replication here is pointed at a server " +
				"that has none.",
			Status: StatusImplemented, Phase: phaseV110,
		}
	case IDSecondsBehindSourceWithin:
		return CheckInfo{
			ID: id,
			Rationale: "The server's reported lag estimate beyond the caller's bound means the " +
				"replica trails the source; NULL means the estimate is unknowable because a " +
				"replication thread is stopped, and a job that gates on lag must not proceed on " +
				"an unknowable estimate.",
			Status: StatusImplemented, Phase: phaseV110,
		}
	default:
		return CheckInfo{}
	}
}
