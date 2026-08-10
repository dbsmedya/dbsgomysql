package sqlutil

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func BenchmarkQuoteIdentifier(b *testing.B) {
	for _, size := range []int{8, 64, 1024, 10_000} {
		name := strings.Repeat("a`", size/2)
		b.Run(benchmarkSizeName(size), func(b *testing.B) {
			b.ReportAllocs()
			var quoted string
			for range b.N {
				quoted = QuoteIdentifier(name)
			}
			runtime.KeepAlive(quoted)
		})
	}
}

func BenchmarkQuoteQualified(b *testing.B) {
	for _, size := range []int{1, 10, 100, 1000} {
		parts := make([]string, size)
		for index := range parts {
			parts[index] = "schema_part"
		}
		b.Run(benchmarkSizeName(size), func(b *testing.B) {
			b.ReportAllocs()
			var quoted string
			for range b.N {
				quoted = QuoteQualified(parts...)
			}
			runtime.KeepAlive(quoted)
		})
	}
}

func BenchmarkIdentifierClassification(b *testing.B) {
	for _, size := range []int{8, 64, 1024, 10_000} {
		name := strings.Repeat("a", size)
		b.Run("simple/"+benchmarkSizeName(size), func(b *testing.B) {
			var valid bool
			for range b.N {
				valid = IsSimpleIdentifier(name)
			}
			runtime.KeepAlive(valid)
		})
		b.Run("validate/"+benchmarkSizeName(size), func(b *testing.B) {
			var err error
			for range b.N {
				err = ValidateIdentifier(name)
			}
			runtime.KeepAlive(err)
		})
	}
}

func benchmarkSizeName(size int) string {
	return "N=" + strconv.Itoa(size)
}
