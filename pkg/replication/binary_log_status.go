package replication

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// The source status statement pair. MySQL 8.2.0 added SHOW BINARY LOG STATUS
// and deprecated the MASTER spelling, 8.4 removed the MASTER spelling, and the
// addition was never backported to 8.0.x — so each end of the supported range
// rejects the other end's statement with a parse error (docs/COMPAT.md entry
// 20).
const (
	sqlBinaryLogStatusPrimary  = "SHOW BINARY LOG STATUS"
	sqlBinaryLogStatusFallback = "SHOW MASTER STATUS"
)

// Promised columns, identical for both statements. Executed_Gtid_Set is
// spelled the same here as in SHOW REPLICA STATUS, so colExecutedGTIDSet is
// shared rather than redeclared.
const (
	colFile           = "File"
	colPosition       = "Position"
	colBinlogDoDB     = "Binlog_Do_DB"
	colBinlogIgnoreDB = "Binlog_Ignore_DB"
)

// BinaryLogStatus reports the server's current binary log coordinates.
//
// BinaryLogStatus is a value with no reference fields: copying it copies the
// whole fact, and it is safe for concurrent use.
type BinaryLogStatus struct {
	// File is the current binary log file name. It may be empty on a server
	// that previously ran with binary logging disabled; the field reports what
	// the server reports rather than normalizing it away.
	File string `json:"file"`
	// Position is the current write position within File.
	Position uint64 `json:"position"`
	// BinlogDoDB is the configured binlog-do-db filter, as the server spells
	// it. Empty means no filter.
	BinlogDoDB string `json:"binlog_do_db"`
	// BinlogIgnoreDB is the configured binlog-ignore-db filter, as the server
	// spells it. Empty means no filter.
	BinlogIgnoreDB string `json:"binlog_ignore_db"`
	// ExecutedGTIDSet is an opaque GTID set equal to the server's
	// gtid_executed; the library never parses one (docs/COMPAT.md entry 21).
	ExecutedGTIDSet string `json:"executed_gtid_set"`
}

// BinaryLogStatus reports the server's binary log coordinates, or nil when the
// server returns no row — which is provable absence: binary logging is
// disabled. A nil status with a nil error therefore means "no binary log",
// never "the question could not be answered".
//
// The returned pointer is owned by the caller and built fresh per call.
//
// BinaryLogStatus is safe for concurrent use when the Inspector's Querier is.
func (i *Inspector) BinaryLogStatus(ctx context.Context) (*BinaryLogStatus, error) {
	if err := i.validate(opBinaryLogStatus); err != nil {
		return nil, err
	}

	rows, errPrimary := i.q.QueryContext(ctx, sqlBinaryLogStatusPrimary)
	if errPrimary == nil {
		return parseBinaryLogStatus(rows)
	}

	// The fallback is this package's entire accommodation of the EOL 8.0 line,
	// where the primary spelling does not exist. Error 1064 is the documented
	// cause but not the detection mechanism: a stdlib-only library does not
	// inspect driver error numbers, so any primary failure triggers it. The
	// fallback is bound to the transitional 8.0 support window and is deleted
	// with it (docs/COMPAT.md entry 20).
	rows, errFallback := i.q.QueryContext(ctx, sqlBinaryLogStatusFallback)
	if errFallback == nil {
		return parseBinaryLogStatus(rows)
	}

	// Both causes are preserved and each is named by the statement that
	// produced it: on 8.0 the decisive error is the second, on 8.4 and later
	// the first, and the library cannot tell which server it is talking to.
	return nil, newOpError(opBinaryLogStatus, "", "", errors.Join(
		fmt.Errorf("SHOW BINARY LOG STATUS: %w", errPrimary),
		fmt.Errorf("SHOW MASTER STATUS: %w", errFallback),
	))
}

// parseBinaryLogStatus owns the row lifecycle. It reads at most one row, so it
// returns before the result set is exhausted — which is why the deferred close
// error matters here: it is the only path on which it can be observed.
func parseBinaryLogStatus(rows *sql.Rows) (status *BinaryLogStatus, err error) {
	defer func() {
		closeErr := rows.Close()
		if closeErr != nil && err == nil {
			status = nil
			err = newOpError(opBinaryLogStatus, "", "", closeErr)
		}
	}()

	index, err := indexColumns(opBinaryLogStatus, rows, promisedBinaryLogStatusColumns())
	if err != nil {
		return nil, err
	}

	if !rows.Next() {
		if iterErr := rows.Err(); iterErr != nil {
			return nil, newOpError(opBinaryLogStatus, "", "", iterErr)
		}

		// No row: binary logging is disabled. Absence, not failure.
		return nil, nil
	}

	values, targets := index.scanTargets()
	if scanErr := rows.Scan(targets...); scanErr != nil {
		return nil, newOpError(opBinaryLogStatus, "", "", scanErr)
	}

	decoded, decodeErr := decodeBinaryLogStatus(index, values)
	if decodeErr != nil {
		return nil, decodeErr
	}
	if iterErr := rows.Err(); iterErr != nil {
		return nil, newOpError(opBinaryLogStatus, "", "", iterErr)
	}

	return &decoded, nil
}

func promisedBinaryLogStatusColumns() []string {
	return []string{
		colFile,
		colPosition,
		colBinlogDoDB,
		colBinlogIgnoreDB,
		colExecutedGTIDSet,
	}
}

func decodeBinaryLogStatus(index columnIndex, values []any) (BinaryLogStatus, error) {
	row := rowDecoder{op: opBinaryLogStatus, index: index, values: values}

	var status BinaryLogStatus
	if err := decodeColumn(row, colFile, decodeString, &status.File); err != nil {
		return BinaryLogStatus{}, err
	}
	if err := decodeColumn(row, colPosition, decodeUint64, &status.Position); err != nil {
		return BinaryLogStatus{}, err
	}
	if err := decodeColumn(row, colBinlogDoDB, decodeString, &status.BinlogDoDB); err != nil {
		return BinaryLogStatus{}, err
	}
	if err := decodeColumn(row, colBinlogIgnoreDB, decodeString, &status.BinlogIgnoreDB); err != nil {
		return BinaryLogStatus{}, err
	}
	if err := decodeColumn(row, colExecutedGTIDSet, decodeString, &status.ExecutedGTIDSet); err != nil {
		return BinaryLogStatus{}, err
	}

	return status, nil
}
