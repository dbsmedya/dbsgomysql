package validations

import "strconv"

const (
	grantStatePresentName     = "present"
	grantStateUnconfirmedName = "unconfirmed"
	privilegeCreateName       = "CREATE"
	privilegeSelectName       = "SELECT"
)

// MetadataVisibility reports whether foreign-key discovery is complete for
// registered InnoDB constraints.
//
// The zero value means the fact was not populated. MetadataVisibility is a
// plain value and is safe for concurrent use.
type MetadataVisibility uint8

const (
	// VisibilityUnknown means no metadata-completeness proof was populated.
	VisibilityUnknown MetadataVisibility = iota
	// VisibilityComplete means the PROCESS-gated InnoDB query succeeded.
	VisibilityComplete
	// VisibilityUnconfirmed means the visibility-filtered fallback was used.
	VisibilityUnconfirmed
)

// String returns a stable human spelling for the visibility state.
//
// String is safe for concurrent use.
func (v MetadataVisibility) String() string {
	switch v {
	case VisibilityUnknown:
		return unknownEnum
	case VisibilityComplete:
		return "complete"
	case VisibilityUnconfirmed:
		return grantStateUnconfirmedName
	default:
		return "MetadataVisibility(" + strconv.Itoa(int(v)) + ")"
	}
}

// GrantState reports whether a requested privilege is established.
//
// The zero value means the fact was not populated. GrantState is a plain value
// and is safe for concurrent use.
type GrantState uint8

const (
	// GrantUnknown means the fact or requested privilege was not populated.
	GrantUnknown GrantState = iota
	// GrantPresent means a qualifying grant is established for this session.
	GrantPresent
	// GrantAbsent means a pinned, role-free session has no qualifying grant.
	GrantAbsent
	// GrantUnconfirmed means session, role, or partial-revoke uncertainty
	// prevents either a present or absent conclusion.
	GrantUnconfirmed
)

// String returns a stable human spelling for the grant state.
//
// String is safe for concurrent use.
func (s GrantState) String() string {
	switch s {
	case GrantUnknown:
		return unknownEnum
	case GrantPresent:
		return grantStatePresentName
	case GrantAbsent:
		return "absent"
	case GrantUnconfirmed:
		return grantStateUnconfirmedName
	default:
		return "GrantState(" + strconv.Itoa(int(s)) + ")"
	}
}

// Privilege is one static MySQL privilege supported by the validation checks.
//
// The zero value names no privilege. Privilege is a plain value and is safe
// for concurrent use.
type Privilege uint8

const (
	// PrivilegeUnknown names no privilege.
	PrivilegeUnknown Privilege = iota
	// PrivilegeSelect is MySQL's SELECT privilege.
	PrivilegeSelect
	// PrivilegeInsert is MySQL's INSERT privilege.
	PrivilegeInsert
	// PrivilegeUpdate is MySQL's UPDATE privilege.
	PrivilegeUpdate
	// PrivilegeDelete is MySQL's DELETE privilege.
	PrivilegeDelete
	// PrivilegeCreate is MySQL's CREATE privilege.
	PrivilegeCreate
)

// String returns the upper-case MySQL spelling of the privilege.
//
// String is safe for concurrent use.
func (p Privilege) String() string {
	switch p {
	case PrivilegeUnknown:
		return unknownEnum
	case PrivilegeSelect:
		return privilegeSelectName
	case PrivilegeInsert:
		return triggerEventInsert
	case PrivilegeUpdate:
		return triggerEventUpdate
	case PrivilegeDelete:
		return triggerEventDelete
	case PrivilegeCreate:
		return privilegeCreateName
	default:
		return "Privilege(" + strconv.Itoa(int(p)) + ")"
	}
}

func (p Privilege) valid() bool {
	return p >= PrivilegeSelect && p <= PrivilegeCreate
}

func privilegeFromString(value string) (Privilege, bool) {
	switch value {
	case privilegeSelectName:
		return PrivilegeSelect, true
	case triggerEventInsert:
		return PrivilegeInsert, true
	case triggerEventUpdate:
		return PrivilegeUpdate, true
	case triggerEventDelete:
		return PrivilegeDelete, true
	case privilegeCreateName:
		return PrivilegeCreate, true
	default:
		return PrivilegeUnknown, false
	}
}

// PrivilegeFact is one privilege answer used as a finding payload. Table is
// empty for a schema-scoped answer.
//
// PrivilegeFact is a plain value and is safe for concurrent use.
type PrivilegeFact struct {
	// Schema is the schema whose privilege was requested.
	Schema string `json:"schema"`
	// Table is the requested table, or empty for a schema-scoped answer.
	Table string `json:"table"`
	// Privilege is the requested privilege.
	Privilege Privilege `json:"privilege"`
	// State is the resolved grant state.
	State GrantState `json:"state"`
}
