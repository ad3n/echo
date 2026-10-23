package echo

import (
	"strings"
	"testing"
)

func BenchmarkBindDelimiter(b *testing.B) {
	for _, tc := range []struct {
		name   string
		values []string
	}{
		{"single", []string{"a,b,c,d,e,f,g,h"}},
		{"multiple", []string{"a,b,c,d", "e,f,g,h", "i,j,k,l"}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			binder := &ValueBinder{ValuesFunc: func(string) []string { return tc.values }}
			b.ReportAllocs()
			for range b.N {
				var result []string
				binder.BindWithDelimiter("v", &result, ",")
				if len(result) == 0 {
					b.Fatal("empty result")
				}
			}
		})
	}
}

func FuzzBindDelimiterCompatibility(f *testing.F) {
	for _, seed := range [][3]string{{"a,b", "c,d", ","}, {"a界", "é", ""}, {"", "", ""}, {"a::b", "::", "::"}} {
		f.Add(seed[0], seed[1], seed[2])
	}

	f.Fuzz(func(t *testing.T, first, second, delimiter string) {
		values := []string{first, second}
		expected := make([]string, 0, len(values))
		for _, value := range values {
			expected = append(expected, strings.Split(value, delimiter)...)
		}

		binder := &ValueBinder{ValuesFunc: func(string) []string { return values }}
		var actual []string
		binder.BindWithDelimiter("v", &actual, delimiter)
		if len(actual) != len(expected) || (actual == nil) != (expected == nil) {
			t.Fatalf("got %#v, want %#v", actual, expected)
		}

		for i := range expected {
			if actual[i] != expected[i] {
				t.Fatalf("got %#v, want %#v", actual, expected)
			}
		}

		if len(actual) > 0 {
			actual[0] = "changed"
			if values[0] != first {
				t.Fatal("input mutated")
			}
		}
	})
}
