package echo

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

type reverseStringer string

func (s reverseStringer) String() string {
	return "custom-" + string(s)
}

func TestRouteReverseResultOwnership(t *testing.T) {
	r := RouteInfo{Path: "/users/:id/files/*"}
	first := r.Reverse("alice", "a.txt")
	second := r.Reverse("bob", strings.Repeat("b", 1024))
	assert.Equal(t, "/users/alice/files/a.txt", first)
	assert.Equal(t, "/users/bob/files/"+strings.Repeat("b", 1024), second)

	for _, value := range []any{42, nil, reverseStringer("value"), []int{1, 2}} {
		assert.Equal(t, "/users/"+fmt.Sprintf("%v", value)+"/files/x", r.Reverse(value, "x"))
	}
}

func BenchmarkRouteReverseAllocation(b *testing.B) {
	for _, tc := range []struct {
		name string
		args []any
	}{
		{name: "strings", args: []any{"alice", "a.txt"}},
		{name: "mixed", args: []any{12345, "a.txt"}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			r := RouteInfo{Path: "/users/:id/files/*"}
			b.ReportAllocs()
			for range b.N {
				if r.Reverse(tc.args...) == "" {
					b.Fatal("empty route")
				}
			}
		})
	}
}
