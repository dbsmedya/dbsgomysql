package validations

import (
	"fmt"
	"runtime"
	"testing"
)

var benchmarkSizes = []int{10, 100, 1000, 10_000}

func BenchmarkTableChecks(b *testing.B) {
	for _, size := range benchmarkSizes {
		requested := benchmarkNames("table", size)
		found := make([]TableInfo, size)
		for index, name := range requested {
			found[index] = TableInfo{Table: name, Type: tableTypeBase, Engine: defaultStorageEngine}
		}
		b.Run(fmt.Sprintf("exist/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var findings []Finding
			for range b.N {
				findings = CheckTablesExist(requested, found)
			}
			runtime.KeepAlive(findings)
		})
		b.Run(fmt.Sprintf("engine/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var findings []Finding
			for range b.N {
				findings = CheckStorageEngine(found, defaultStorageEngine)
			}
			runtime.KeepAlive(findings)
		})
	}
}

func BenchmarkPrimaryKeyChecks(b *testing.B) {
	for _, size := range benchmarkSizes {
		pks := make([]PKInfo, size)
		expected := make(map[string]string, size)
		for index := range pks {
			table := fmt.Sprintf("table_%d", index)
			pks[index] = PKInfo{
				Table: table, Kind: PKSingle, Columns: []string{"id"},
				DataType: dataTypeBigint, IsInteger: true,
			}
			expected[table] = "id"
		}
		b.Run(fmt.Sprintf("matches/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var findings []Finding
			for range b.N {
				findings = CheckPKMatchesExpected(pks, expected)
			}
			runtime.KeepAlive(findings)
		})
		b.Run(fmt.Sprintf("case/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var findings []Finding
			for range b.N {
				findings = CheckPKNameCase(pks, expected)
			}
			runtime.KeepAlive(findings)
		})
	}
}

func BenchmarkFactToFindingChecks(b *testing.B) {
	for _, size := range benchmarkSizes {
		invisible := make([]InvisibleColumns, size)
		triggers := make([]TriggerInfo, size)
		keys := make([]ForeignKey, size)
		for index := range size {
			table := fmt.Sprintf("table_%d", index)
			invisible[index] = InvisibleColumns{Table: table, Columns: []string{"hidden"}}
			triggers[index] = TriggerInfo{
				Table: table, Name: "trigger", Event: triggerEventDelete, Timing: triggerTimingBefore,
			}
			keys[index] = ForeignKey{
				ConstraintName: fmt.Sprintf("fk_%d", index), ChildSchema: "audit",
				ChildTable: table, ChildColumns: []string{"parent_id"},
				ParentSchema: "audit", ParentTable: table,
				ParentColumns: []string{"id"}, OnDelete: fkRuleCascade, Indexed: true,
			}
		}
		b.Run(fmt.Sprintf("invisible/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var findings []Finding
			for range b.N {
				findings = CheckInvisibleColumns(invisible)
			}
			runtime.KeepAlive(findings)
		})
		b.Run(fmt.Sprintf("triggers/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var findings []Finding
			for range b.N {
				findings = CheckTriggersPresent(triggers, TriggerDelete)
			}
			runtime.KeepAlive(findings)
		})
		b.Run(fmt.Sprintf("indexed/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var findings []Finding
			for range b.N {
				findings = CheckFKIndexed(keys)
			}
			runtime.KeepAlive(findings)
		})
		b.Run(fmt.Sprintf("cascade/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var findings []Finding
			for range b.N {
				findings = CheckCascadeRules(keys)
			}
			runtime.KeepAlive(findings)
		})
	}
}

func BenchmarkCheckFKClosure(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		tables := benchmarkNames("table", size)
		keys := make([]ForeignKey, size)
		for index, table := range tables {
			keys[index] = ForeignKey{
				ConstraintName: fmt.Sprintf("fk_%d", index),
				ChildSchema:    "external", ChildTable: fmt.Sprintf("child_%d", index),
				ChildColumns: []string{"parent_id"}, ParentSchema: "audit",
				ParentTable: table, ParentColumns: []string{"id"}, Indexed: true,
			}
		}
		result := ForeignKeyResult{Keys: keys, Visibility: VisibilityComplete}
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var findings []Finding
			for range b.N {
				findings = CheckFKClosure(result, "audit", tables)
			}
			runtime.KeepAlive(findings)
		})
	}
}

func BenchmarkSelectForeignKeys(b *testing.B) {
	for _, size := range []int{10, 100, 1000} {
		tables := benchmarkNames("table", size)
		keys := make([]ForeignKey, size)
		for index, table := range tables {
			keys[index] = ForeignKey{
				ConstraintName: fmt.Sprintf("fk_%d", index), ChildSchema: "audit",
				ChildTable: table, ChildColumns: []string{"parent_id"},
				ParentSchema: "other", ParentTable: "parent",
				ParentColumns: []string{"id"}, Indexed: true,
			}
		}
		sel := OutgoingFrom(tables...)
		b.Run(fmt.Sprintf("N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var selected []ForeignKey
			for range b.N {
				selected = selectForeignKeys(keys, "audit", sel)
			}
			runtime.KeepAlive(selected)
		})
	}
}

func BenchmarkDiffSpecs(b *testing.B) {
	for _, size := range benchmarkSizes {
		a := benchmarkSpec(size)
		equal := benchmarkSpec(size)
		different := benchmarkSpec(size)
		for index := range different.Columns {
			different.Columns[index].Type = "varchar(255)"
			different.Columns[index].NormalizedType = "varchar(255)"
		}
		b.Run(fmt.Sprintf("equal/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var diffs []SpecDiff
			for range b.N {
				diffs = DiffSpecs(a, equal)
			}
			runtime.KeepAlive(diffs)
		})
		b.Run(fmt.Sprintf("different/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var diffs []SpecDiff
			for range b.N {
				diffs = DiffSpecs(a, different)
			}
			runtime.KeepAlive(diffs)
		})
	}
}

func BenchmarkHotHelpers(b *testing.B) {
	for _, size := range benchmarkSizes {
		names := benchmarkNames("table", size)
		b.Run(fmt.Sprintf("narrow_names/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var params []string
			for range b.N {
				params, _ = narrowNames(names, 1)
			}
			runtime.KeepAlive(params)
		})
	}
	for _, size := range []int{10, 100, 1000, maxPointLookupTables} {
		names := benchmarkNames("table", size)
		b.Run(fmt.Sprintf("requested_objects/N=%d", size), func(b *testing.B) {
			b.ReportAllocs()
			var query string
			var args []any
			for range b.N {
				query, args, _ = requestedObjects("audit", names)
			}
			runtime.KeepAlive(query)
			runtime.KeepAlive(args)
		})
	}
	for _, size := range []int{8, 64, 1000} {
		pattern := "%" + string(make([]byte, size))
		name := string(make([]byte, size))
		b.Run(fmt.Sprintf("like_pattern/N=%d", size), func(b *testing.B) {
			var matched bool
			for range b.N {
				matched = likePatternMatches(pattern, name)
			}
			runtime.KeepAlive(matched)
		})
	}
}

func BenchmarkGrantLookup(b *testing.B) {
	for _, size := range benchmarkSizes {
		grants := Grants{
			affinity:  affinityPinned,
			populated: true,
			schema:    make(map[schemaPrivilegeKey]grantSources, size),
		}
		for index := range size {
			grants.schema[schemaPrivilegeKey{
				schema: fmt.Sprintf("other_%d", index), privilege: PrivilegeSelect,
			}] = grantSourceAccount
		}
		b.Run(fmt.Sprintf("absent_with_patterns/N=%d", size), func(b *testing.B) {
			var state GrantState
			for range b.N {
				state = grants.Schema("target", PrivilegeSelect)
			}
			runtime.KeepAlive(state)
		})
	}
}

func BenchmarkCatalog(b *testing.B) {
	b.Run("catalog", func(b *testing.B) {
		b.ReportAllocs()
		var catalog []CheckInfo
		for range b.N {
			catalog = Catalog()
		}
		runtime.KeepAlive(catalog)
	})
	b.Run("lookup", func(b *testing.B) {
		b.ReportAllocs()
		var info CheckInfo
		for range b.N {
			info, _ = LookupCheck(IDTriggersPresent)
		}
		runtime.KeepAlive(info)
	})
}

func benchmarkNames(prefix string, size int) []string {
	names := make([]string, size)
	for index := range names {
		names[index] = fmt.Sprintf("%s_%d", prefix, index)
	}
	return names
}

func benchmarkSpec(size int) TableSpec {
	columns := make([]ColumnSpec, size)
	for index := range columns {
		columns[index] = ColumnSpec{
			Name: fmt.Sprintf("column_%d", index), Ordinal: index + 1,
			Type: dataTypeBigint, NormalizedType: dataTypeBigint,
			Nullable: true, Generated: GeneratedNone,
		}
	}
	return TableSpec{
		Schema: "audit", Table: "table", Engine: defaultStorageEngine,
		Charset: "utf8mb4", Collation: "utf8mb4_0900_ai_ci", Columns: columns,
	}
}
