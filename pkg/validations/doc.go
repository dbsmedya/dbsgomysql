// Package validations answers factual questions about a MySQL schema and runs
// named checks over the answers.
//
// The organizing distinction is facts versus policy:
//
//   - a fact describes the schema;
//   - a check returns findings when its predicate is not satisfied;
//   - no findings means the check passed for the objects inspected;
//   - an error means the inspection could not be completed.
//
// Whether a finding matters — informational, a warning, fatal, or ignorable —
// is the consumer's decision. Findings therefore carry no severity, not even a
// default to remap, because a default is a decision. What each check ships
// instead is a rationale: the failure mode it protects against.
package validations
