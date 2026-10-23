package echo

import (
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
)

type reverseFormatter int

func (v reverseFormatter) Format(s fmt.State, verb rune) {
	fmt.Fprintf(s, "formatted-%d-%c", int(v), verb)
}

func referenceRouteReverse(r RouteInfo, values ...any) string {
	var uri strings.Builder
	uri.Grow(len(r.Path))
	next := 0
	walkRoutePath(r.Path, func(part routePathPart) {
		if part.kind == staticKind {
			uri.WriteString(part.value)
			return
		}

		if next < len(values) {
			uri.WriteString(fmt.Sprint(values[next]))
			next++
			return
		}

		if part.kind == paramKind {
			uri.WriteByte(paramLabel)
		}

		uri.WriteString(strings.ReplaceAll(part.value, `\:`, ":"))
	})
	return uri.String()
}

func TestRouteReverseFormattingCompatibility(t *testing.T) {
	values := []any{int(0), int(-12345), int64(math.MinInt64), int64(math.MaxInt64), uint64(math.MaxUint64), uint64(0), int8(-12), uint32(123), true, 1.25, nil, "alice", reverseStringer("name"), reverseFormatter(42), []int{1, 2}}
	for _, path := range []string{"", "/static", `/literal\path`, "/:id", "/:id/*", `/r/:name\:cancel/:action`, `/literal\:colon`, "/:id/:other/:third"} {
		r := RouteInfo{Path: path}
		for _, value := range values {
			args := []any{value, "tail"}
			if got, want := r.Reverse(args...), referenceRouteReverse(r, args...); got != want {
				t.Fatalf("path=%q value=%#v: got %q, want %q", path, value, got, want)
			}
		}
	}
}

func FuzzRouteReverseFormattingCompatibility(f *testing.F) {
	for _, path := range []string{"", "/static", "/:id", "/:id/*", `/r/:name\:cancel`, `/literal\:colon`, `/r/:a\:b:c/*`} {
		f.Add(path, "value", int64(-12345))
	}

	f.Fuzz(func(t *testing.T, path, value string, number int64) {
		r := RouteInfo{Path: path}
		for _, args := range [][]any{nil, {value}, {number}, {value, number}, {uint64(number), value}} {
			if got, want := r.Reverse(args...), referenceRouteReverse(r, args...); got != want {
				t.Fatalf("path=%q args=%#v: got %q, want %q", path, args, got, want)
			}
		}
	})
}

func TestXFFCanonicalFormattingCompatibility(t *testing.T) {
	extractor := ExtractIPFromXFFHeader()
	checker := newIPChecker(nil)
	for _, address := range []string{"203.0.113.1", "2001:db8::1", "2001:DB8:0:0:0:0:0:1", "::ffff:203.0.113.1", "::FFFF:cb00:7101", "2001:0:0:1:0:0:1:1", "2001:db8::192.0.2.1", "[2001:DB8::1]", "203.000.113.1", "invalid"} {
		for _, remote := range []string{"10.0.0.1:80", "[" + address + "]:80"} {
			req := &http.Request{RemoteAddr: remote, Header: http.Header{HeaderXForwardedFor: {address}}}
			if got, want := extractor(req), referenceExtractXFF(req, checker); got != want {
				t.Fatalf("remote=%q address=%q: got %q, want %q", remote, address, got, want)
			}
		}
	}
}

func BenchmarkRouteReverseNoValues(b *testing.B) {
	for _, path := range []string{"/static/path", "/users/:id/files/*", `/literal\:colon`} {
		b.Run(path, func(b *testing.B) {
			r := RouteInfo{Path: path}
			b.ReportAllocs()
			for range b.N {
				if r.Reverse() == "" {
					b.Fatal("empty route")
				}
			}
		})
	}
}
