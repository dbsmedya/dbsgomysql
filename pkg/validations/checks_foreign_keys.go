package validations

// CheckFKIndexed reports facts whose ordered child columns are not a leftmost
// prefix of an index.
//
// MySQL guarantees that every registered InnoDB foreign key has such a child
// index and refuses to drop it. A finding therefore means the supplied fact
// did not come from a conforming authoritative InnoDB source. Findings preserve
// input order. CheckFKIndexed is safe for concurrent use when fks is not
// mutated concurrently.
func CheckFKIndexed(fks []ForeignKey) []Finding {
	var findings []Finding
	for index := range fks {
		key := &fks[index]
		if key.Indexed {
			continue
		}
		findings = append(findings, foreignKeyFinding(
			IDFKIndexed,
			"foreign key lacks the supporting leftmost-prefix child index MySQL guarantees",
			key,
		))
	}

	return findings
}

// CheckFKClosure reports constraints whose parent belongs to the qualified
// target set but whose child does not. It also reports one closure finding when
// result visibility is not complete, because visible edges alone cannot prove
// closure.
//
// The result must come from ForeignKeys with IncomingTo(tables...) on an
// Inspector bound to schema. Supplying caller-authored, truncated, or
// differently selected facts invalidates the conclusion. Findings are emitted
// once per external key, in the order the result carries them; for the
// documented input that is requested parent-table order, duplicates included,
// with ties sorted by exact child schema, child table, and constraint name. An
// empty target is vacuously closed.
// CheckFKClosure is safe for concurrent use when its arguments are not mutated
// concurrently.
func CheckFKClosure(
	result ForeignKeyResult,
	schema string,
	tables []string,
) []Finding {
	if len(tables) == 0 {
		return nil
	}

	targets := make(map[string]struct{}, len(tables))
	for _, table := range tables {
		targets[table] = struct{}{}
	}

	// One finding per external key, in the order the result carries them.
	// ForeignKeys with IncomingTo(tables...) already returns keys in requested
	// parent-table order, duplicates included, with ties sorted by exact child
	// schema, child table, and constraint name; re-grouping here would either
	// multiply findings for a duplicated target (the defect in #74) or collapse
	// the duplicate positions the fact preserves.
	var findings []Finding
	for index := range result.Keys {
		key := &result.Keys[index]
		if key.ParentSchema != schema {
			continue
		}
		if _, parentInSet := targets[key.ParentTable]; !parentInSet {
			continue
		}
		_, childInSet := targets[key.ChildTable]
		if key.ChildSchema == schema && childInSet {
			continue
		}
		findings = append(findings, foreignKeyFinding(
			IDFKClosure,
			"foreign key points into the target set from an external child",
			key,
		))
	}

	if result.Visibility != VisibilityComplete {
		findings = append(findings, Finding{
			Check: IDFKClosure,
			Message: findingMessage(
				IDFKClosure,
				"foreign-key closure is unconfirmed because discovery was not complete",
			),
			Tables: append([]string(nil), tables...),
			Facts:  result.Visibility,
		})
	}

	return findings
}

// CheckFKMetadataVisibility reports every state except VisibilityComplete.
//
// The PROCESS-gated InnoDB registry is the completeness proof. A fallback,
// zero, or undeclared state means incoming-key discovery cannot safely be
// treated as complete. CheckFKMetadataVisibility is safe for concurrent use.
func CheckFKMetadataVisibility(vis MetadataVisibility) []Finding {
	if vis == VisibilityComplete {
		return nil
	}

	return []Finding{{
		Check: IDFKMetadataVisibility,
		Message: findingMessage(
			IDFKMetadataVisibility,
			"foreign-key metadata completeness is not established",
		),
		Facts: vis,
	}}
}

// CheckCascadeRules reports supplied constraints that use ON DELETE CASCADE.
//
// Cascade is legitimate design; this check identifies the edges and makes no
// policy judgment. The facts must come from ForeignKeys with Within(tables...)
// if the consumer intends an in-set answer. Findings preserve input order.
// CheckCascadeRules is safe for concurrent use when fks is not mutated
// concurrently.
func CheckCascadeRules(fks []ForeignKey) []Finding {
	var findings []Finding
	for index := range fks {
		key := &fks[index]
		if key.OnDelete != fkRuleCascade {
			continue
		}
		findings = append(findings, foreignKeyFinding(
			IDCascadeRules,
			"foreign key applies ON DELETE CASCADE",
			key,
		))
	}

	return findings
}

func foreignKeyFinding(id, summary string, key *ForeignKey) Finding {
	return Finding{
		Check:   id,
		Message: findingMessage(id, summary),
		Tables:  []string{key.ChildTable},
		Facts:   *key,
	}
}
