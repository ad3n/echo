package echo

import (
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
)

type (
	fastPathValidatedString string

	fastPathValidatedInt int
)

func (v *fastPathValidatedString) UnmarshalParam(value string) error {
	if value != "allowed" {
		return errors.New("rejected")
	}

	*v = "validated:" + fastPathValidatedString(value)
	return nil
}

func (v *fastPathValidatedInt) UnmarshalText(value []byte) error {
	if string(value) != "allowed" {
		return errors.New("rejected")
	}

	*v = 42
	return nil
}

func TestBindScalarFastPathPreservesCustomValidation(t *testing.T) {
	for _, tag := range []string{"param", "query", "header", "form"} {
		t.Run(tag, func(t *testing.T) {
			var target struct {
				Validated fastPathValidatedString   `param:"value" query:"value" header:"value" form:"value"`
				Text      fastPathValidatedInt      `param:"value" query:"value" header:"value" form:"value"`
				Pointer   *fastPathValidatedString  `param:"value" query:"value" header:"value" form:"value"`
				List      []fastPathValidatedString `param:"value" query:"value" header:"value" form:"value"`
			}
			err := bindData(&target, map[string][]string{"value": {"allowed"}}, tag, nil)
			if err != nil {
				t.Fatal(err)
			}

			if target.Validated != "validated:allowed" || target.Text != 42 || target.Pointer == nil || *target.Pointer != "validated:allowed" || len(target.List) != 1 || target.List[0] != "validated:allowed" {
				t.Fatalf("custom conversion bypassed: %+v", target)
			}

			err = bindData(&target, map[string][]string{"value": {"denied"}}, tag, nil)
			if err == nil || err.Error() != "value: rejected" || target.Validated != "validated:allowed" {
				t.Fatalf("custom validation bypassed: value=%q error=%v", target.Validated, err)
			}
		})
	}
}

func FuzzBindScalarFastPath(f *testing.F) {
	for _, value := range []string{"", "true", "false", "42", "-1", "1.5", "NaN", "Inf", "18446744073709551616", "hello"} {
		f.Add(value)
	}

	types := []reflect.Type{
		reflect.TypeFor[string](), reflect.TypeFor[bool](),
		reflect.TypeFor[int](), reflect.TypeFor[int8](), reflect.TypeFor[int16](), reflect.TypeFor[int32](), reflect.TypeFor[int64](),
		reflect.TypeFor[uint](), reflect.TypeFor[uint8](), reflect.TypeFor[uint16](), reflect.TypeFor[uint32](), reflect.TypeFor[uint64](),
		reflect.TypeFor[float32](), reflect.TypeFor[float64](),
	}
	structs := make([]reflect.Type, len(types))
	for i, typ := range types {
		structs[i] = reflect.StructOf([]reflect.StructField{{Name: "Value", Type: typ, Tag: `query:"value"`}})
	}

	f.Fuzz(func(t *testing.T, value string) {
		for i, typ := range types {
			want := reflect.New(typ).Elem()
			wantErr := setWithProperType(typ.Kind(), value, want)
			target := reflect.New(structs[i])
			gotErr := bindData(target.Interface(), map[string][]string{"value": {value}}, "query", nil)
			if wantErr != nil {
				if gotErr == nil || gotErr.Error() != fmt.Sprintf("value: %v", wantErr) {
					t.Fatalf("%s error: got %v, want %v", typ, gotErr, wantErr)
				}

				continue
			}

			if gotErr != nil {
				t.Fatal(gotErr)
			}

			got := target.Elem().Field(0)
			if typ.Kind() == reflect.Float32 || typ.Kind() == reflect.Float64 {
				if math.IsNaN(want.Float()) && math.IsNaN(got.Float()) {
					continue
				}
			}

			if !reflect.DeepEqual(got.Interface(), want.Interface()) {
				t.Fatalf("%s: got %v, want %v", typ, got.Interface(), want.Interface())
			}
		}
	})
}
