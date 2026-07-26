package validations

import (
	"context"
	"database/sql"
	"fmt"
)

const (
	defaultStorageEngine = "InnoDB"
	tableTypeBase        = "BASE TABLE"
)

// TableInfo describes one object that exists in the inspected schema.
//
// TableInfo is a plain value and is safe for concurrent use.
type TableInfo struct {
	// Table is the object's exact server-side spelling.
	Table string `json:"table"`
	// Type is information_schema.TABLES.TABLE_TYPE verbatim.
	Type string `json:"type"`
	// Engine is the server-reported engine, or empty when MySQL reports NULL.
	Engine string `json:"engine"`
}

// Tables returns facts for requested objects that exist in the Inspector's
// schema. Results preserve requested order and carry the server's exact
// spelling. Missing or invisible objects are absent, not errors. Views are
// included with an empty Engine when MySQL reports NULL.
//
// Tables is safe for concurrent use when the Inspector's Querier is safe for
// concurrent use and tables is not mutated concurrently.
func (i *Inspector) Tables(ctx context.Context, tables []string) ([]TableInfo, error) {
	if err := i.validate(opTables, tables); err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, nil
	}

	const query = `
		SELECT TABLE_NAME, TABLE_TYPE, ENGINE
		FROM information_schema.TABLES
		WHERE TABLE_SCHEMA = ?
		ORDER BY TABLE_NAME`

	rows, err := i.q.QueryContext(ctx, query, i.schema)
	if err != nil {
		return nil, newObjectError(opTables, i.schema, "", fmt.Errorf("query metadata: %w", err))
	}
	defer rows.Close()

	byName := make(map[string]TableInfo)
	for rows.Next() {
		var (
			info   TableInfo
			engine sql.NullString
		)
		if err := rows.Scan(&info.Table, &info.Type, &engine); err != nil {
			return nil, newObjectError(opTables, i.schema, "", fmt.Errorf("scan metadata: %w", err))
		}
		if engine.Valid {
			info.Engine = engine.String
		}
		byName[info.Table] = info
	}
	if err := rows.Err(); err != nil {
		return nil, newObjectError(opTables, i.schema, "", fmt.Errorf("iterate metadata: %w", err))
	}

	found := make([]TableInfo, 0, len(tables))
	for _, table := range tables {
		if info, ok := byName[table]; ok {
			found = append(found, info)
		}
	}

	return found, nil
}

// CheckTablesExist reports each requested table for which no fact exists.
//
// A configured table that is missing fails only when execution reaches it,
// after an operation may already have begun. Findings preserve requested
// order and spelling because an absent object has no server-side spelling.
// CheckTablesExist is safe for concurrent use when its arguments are not
// mutated concurrently.
func CheckTablesExist(requested []string, found []TableInfo) []Finding {
	foundByName := make(map[string]struct{}, len(found))
	for _, table := range found {
		foundByName[table.Table] = struct{}{}
	}

	var findings []Finding
	for _, table := range requested {
		if _, ok := foundByName[table]; ok {
			continue
		}
		findings = append(findings, Finding{
			Check: IDTablesExist,
			Message: findingMessage(
				IDTablesExist,
				"configured table does not exist in the inspected schema",
			),
			Tables: []string{table},
		})
	}

	return findings
}

// CheckStorageEngine reports each base table whose engine differs from engine.
//
// Non-transactional engines cannot provide the integrity a
// copy-verify-delete cycle depends on. An empty expected engine means InnoDB;
// engine spelling is compared with ASCII case folding. Views are ignored
// because they have no storage engine. CheckStorageEngine is safe for
// concurrent use when found is not mutated concurrently.
func CheckStorageEngine(found []TableInfo, engine string) []Finding {
	if engine == "" {
		engine = defaultStorageEngine
	}

	var findings []Finding
	for _, table := range found {
		if table.Type != tableTypeBase || asciiFoldEqual(table.Engine, engine) {
			continue
		}
		findings = append(findings, Finding{
			Check: IDStorageEngine,
			Message: findingMessage(
				IDStorageEngine,
				"table uses a different storage engine than the caller requires",
			),
			Tables: []string{table.Table},
			Facts:  table,
		})
	}

	return findings
}
