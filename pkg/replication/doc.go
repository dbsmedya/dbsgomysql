// Package replication answers factual questions about one MySQL server's
// replication state and runs named checks over the answers.
//
// The organizing distinction is facts versus policy:
//
//   - a fact describes the server's replication state;
//   - a check returns findings when its predicate is not satisfied;
//   - no findings means the check passed for the state inspected;
//   - an error means the inspection could not be completed.
//
// Whether a finding matters — informational, a warning, fatal, or ignorable —
// is the consumer's decision. Findings therefore carry no severity, not even a
// default to remap, because a default is a decision. What each check ships
// instead is a rationale: the failure mode it protects against.
//
// This package is a sibling of the schema-oriented validations package, not a
// layer on top of it: it declares its own Querier and its own Inspector, so a
// consumer needs neither package to use the other.
//
// # Ownership of returned values
//
// Every fact owns the slices and pointers it returns. They are built fresh per
// call, so mutating one never affects another and never affects a later call.
// Callers are free to sort or truncate a returned slice in place.
//
// # Concurrency
//
// Inspector is immutable and safe for concurrent use when its Querier is safe
// for concurrent use. The package holds no mutable state of its own, and each
// exported identifier documents its own concurrency behavior.
//
// # Design record
//
// The design this package implements is recorded internally as the replication
// facts design spec, 2026-08-19-replication-facts-design-r5. Consumer-facing
// version behavior lives in docs/COMPAT.md.
package replication
