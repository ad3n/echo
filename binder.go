// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"encoding"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type (
	BindingError struct {
		Field string `json:"field"`
		*HTTPError

		Values []string `json:"-"`
	}

	ValueBinder struct {
		ValueFunc func(sourceParam string) string

		ValuesFunc func(sourceParam string) []string

		ErrorFunc func(sourceParam string, values []string, message string, internalError error) error
		errors    []error

		failFast bool
	}
)

func NewBindingError(sourceParam string, values []string, message string, err error) error {
	return &BindingError{
		Field:     sourceParam,
		Values:    values,
		HTTPError: &HTTPError{Code: http.StatusBadRequest, Message: message, err: err},
	}
}

func (be *BindingError) Error() string {
	return fmt.Sprintf("%s, field=%s", be.HTTPError.Error(), be.Field)
}

func (be *BindingError) MarshalJSON() ([]byte, error) {
	message := be.Message
	if message == "" {
		message = http.StatusText(be.Code)
	}

	return json.Marshal(struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	}{
		Field:   be.Field,
		Message: message,
	})
}

func QueryParamsBinder(c *Context) *ValueBinder {
	return &ValueBinder{
		failFast:  true,
		ValueFunc: c.QueryParam,
		ValuesFunc: func(sourceParam string) []string {
			values, ok := c.QueryParams()[sourceParam]
			if !ok {
				return nil
			}

			return values
		},
		ErrorFunc: NewBindingError,
	}
}

func PathValuesBinder(c *Context) *ValueBinder {
	return &ValueBinder{
		failFast:  true,
		ValueFunc: c.Param,
		ValuesFunc: func(sourceParam string) []string {

			value := c.Param(sourceParam)
			if value == "" {
				return nil
			}

			return []string{value}
		},
		ErrorFunc: NewBindingError,
	}
}

func FormFieldBinder(c *Context) *ValueBinder {
	vb := &ValueBinder{
		failFast: true,
		ValueFunc: func(sourceParam string) string {
			return c.Request().FormValue(sourceParam)
		},
		ErrorFunc: NewBindingError,
	}
	vb.ValuesFunc = func(sourceParam string) []string {
		if c.Request().Form == nil {

			_, _ = c.MultipartForm()
		}

		values, ok := c.Request().Form[sourceParam]
		if !ok {
			return nil
		}

		return values
	}

	return vb
}

func (b *ValueBinder) FailFast(value bool) *ValueBinder {
	b.failFast = value
	return b
}

func (b *ValueBinder) setError(err error) {
	if b.errors == nil {
		b.errors = []error{err}
		return
	}

	b.errors = append(b.errors, err)
}

func (b *ValueBinder) BindError() error {
	if b.errors == nil {
		return nil
	}

	err := b.errors[0]
	b.errors = nil
	return err
}

func (b *ValueBinder) BindErrors() []error {
	if b.errors == nil {
		return nil
	}

	errors := b.errors
	b.errors = nil
	return errors
}

func (b *ValueBinder) CustomFunc(sourceParam string, customFunc func(values []string) []error) *ValueBinder {
	return b.customFunc(sourceParam, customFunc, false)
}

func (b *ValueBinder) MustCustomFunc(sourceParam string, customFunc func(values []string) []error) *ValueBinder {
	return b.customFunc(sourceParam, customFunc, true)
}

func (b *ValueBinder) customFunc(sourceParam string, customFunc func(values []string) []error, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	values := b.ValuesFunc(sourceParam)
	if len(values) == 0 {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		}

		return b
	}

	if errs := customFunc(values); errs != nil {
		b.errors = append(b.errors, errs...)
	}

	return b
}

func (b *ValueBinder) String(sourceParam string, dest *string) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValueFunc(sourceParam)
	if value == "" {
		return b
	}

	*dest = value
	return b
}

func (b *ValueBinder) MustString(sourceParam string, dest *string) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValueFunc(sourceParam)
	if value == "" {
		b.setError(b.ErrorFunc(sourceParam, []string{value}, "required field value is empty", nil))
		return b
	}

	*dest = value
	return b
}

func (b *ValueBinder) Strings(sourceParam string, dest *[]string) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValuesFunc(sourceParam)
	if value == nil {
		return b
	}

	*dest = value
	return b
}

func (b *ValueBinder) MustStrings(sourceParam string, dest *[]string) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValuesFunc(sourceParam)
	if value == nil {
		b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		return b
	}

	*dest = value
	return b
}

func (b *ValueBinder) BindUnmarshaler(sourceParam string, dest BindUnmarshaler) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	tmp := b.ValueFunc(sourceParam)
	if tmp == "" {
		return b
	}

	if err := dest.UnmarshalParam(tmp); err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{tmp}, "failed to bind field value to BindUnmarshaler interface", err))
	}

	return b
}

func (b *ValueBinder) MustBindUnmarshaler(sourceParam string, dest BindUnmarshaler) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValueFunc(sourceParam)
	if value == "" {
		b.setError(b.ErrorFunc(sourceParam, []string{value}, "required field value is empty", nil))
		return b
	}

	if err := dest.UnmarshalParam(value); err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{value}, "failed to bind field value to BindUnmarshaler interface", err))
	}

	return b
}

func (b *ValueBinder) JSONUnmarshaler(sourceParam string, dest json.Unmarshaler) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	tmp := b.ValueFunc(sourceParam)
	if tmp == "" {
		return b
	}

	if err := dest.UnmarshalJSON([]byte(tmp)); err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{tmp}, "failed to bind field value to json.Unmarshaler interface", err))
	}

	return b
}

func (b *ValueBinder) MustJSONUnmarshaler(sourceParam string, dest json.Unmarshaler) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	tmp := b.ValueFunc(sourceParam)
	if tmp == "" {
		b.setError(b.ErrorFunc(sourceParam, []string{tmp}, "required field value is empty", nil))
		return b
	}

	if err := dest.UnmarshalJSON([]byte(tmp)); err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{tmp}, "failed to bind field value to json.Unmarshaler interface", err))
	}

	return b
}

func (b *ValueBinder) TextUnmarshaler(sourceParam string, dest encoding.TextUnmarshaler) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	tmp := b.ValueFunc(sourceParam)
	if tmp == "" {
		return b
	}

	if err := dest.UnmarshalText([]byte(tmp)); err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{tmp}, "failed to bind field value to encoding.TextUnmarshaler interface", err))
	}

	return b
}

func (b *ValueBinder) MustTextUnmarshaler(sourceParam string, dest encoding.TextUnmarshaler) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	tmp := b.ValueFunc(sourceParam)
	if tmp == "" {
		b.setError(b.ErrorFunc(sourceParam, []string{tmp}, "required field value is empty", nil))
		return b
	}

	if err := dest.UnmarshalText([]byte(tmp)); err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{tmp}, "failed to bind field value to encoding.TextUnmarshaler interface", err))
	}

	return b
}

func (b *ValueBinder) BindWithDelimiter(sourceParam string, dest any, delimiter string) *ValueBinder {
	return b.bindWithDelimiter(sourceParam, dest, delimiter, false)
}

func (b *ValueBinder) MustBindWithDelimiter(sourceParam string, dest any, delimiter string) *ValueBinder {
	return b.bindWithDelimiter(sourceParam, dest, delimiter, true)
}

func (b *ValueBinder) bindWithDelimiter(sourceParam string, dest any, delimiter string, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	values := b.ValuesFunc(sourceParam)
	if len(values) == 0 {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		}

		return b
	}

	count := 0
	for _, value := range values {
		if delimiter == "" {
			count += strings.Count(value, "") - 1
			continue
		}

		count += strings.Count(value, delimiter) + 1
	}

	tmpValues := make([]string, 0, max(len(values), count))
	for _, value := range values {
		for part := range strings.SplitSeq(value, delimiter) {
			tmpValues = append(tmpValues, part)
		}
	}

	switch d := dest.(type) {
	case *[]string:
		*d = tmpValues
		return b
	case *[]bool:
		return b.bools(sourceParam, tmpValues, d)
	case *[]int64, *[]int32, *[]int16, *[]int8, *[]int:
		return b.ints(sourceParam, tmpValues, d)
	case *[]uint64, *[]uint32, *[]uint16, *[]uint8, *[]uint:
		return b.uints(sourceParam, tmpValues, d)
	case *[]float64, *[]float32:
		return b.floats(sourceParam, tmpValues, d)
	case *[]time.Duration:
		return b.durations(sourceParam, tmpValues, d)
	default:
		b.setError(b.ErrorFunc(sourceParam, []string{}, "unsupported bind type", nil))
		return b
	}
}

func (b *ValueBinder) Int64(sourceParam string, dest *int64) *ValueBinder {
	return b.intValue(sourceParam, dest, 64, false)
}

func (b *ValueBinder) MustInt64(sourceParam string, dest *int64) *ValueBinder {
	return b.intValue(sourceParam, dest, 64, true)
}

func (b *ValueBinder) Int32(sourceParam string, dest *int32) *ValueBinder {
	return b.intValue(sourceParam, dest, 32, false)
}

func (b *ValueBinder) MustInt32(sourceParam string, dest *int32) *ValueBinder {
	return b.intValue(sourceParam, dest, 32, true)
}

func (b *ValueBinder) Int16(sourceParam string, dest *int16) *ValueBinder {
	return b.intValue(sourceParam, dest, 16, false)
}

func (b *ValueBinder) MustInt16(sourceParam string, dest *int16) *ValueBinder {
	return b.intValue(sourceParam, dest, 16, true)
}

func (b *ValueBinder) Int8(sourceParam string, dest *int8) *ValueBinder {
	return b.intValue(sourceParam, dest, 8, false)
}

func (b *ValueBinder) MustInt8(sourceParam string, dest *int8) *ValueBinder {
	return b.intValue(sourceParam, dest, 8, true)
}

func (b *ValueBinder) Int(sourceParam string, dest *int) *ValueBinder {
	return b.intValue(sourceParam, dest, 0, false)
}

func (b *ValueBinder) MustInt(sourceParam string, dest *int) *ValueBinder {
	return b.intValue(sourceParam, dest, 0, true)
}

func (b *ValueBinder) intValue(sourceParam string, dest any, bitSize int, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValueFunc(sourceParam)
	if value == "" {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		}

		return b
	}

	return b.int(sourceParam, value, dest, bitSize)
}

func (b *ValueBinder) int(sourceParam string, value string, dest any, bitSize int) *ValueBinder {
	n, err := strconv.ParseInt(value, 10, bitSize)
	if err != nil {
		if bitSize == 0 {
			b.setError(b.ErrorFunc(sourceParam, []string{value}, "failed to bind field value to int", err))
			return b
		}

		b.setError(b.ErrorFunc(sourceParam, []string{value}, fmt.Sprintf("failed to bind field value to int%v", bitSize), err))
		return b
	}

	switch d := dest.(type) {
	case *int64:
		*d = n
	case *int32:
		*d = int32(n) // #nosec G115
	case *int16:
		*d = int16(n) // #nosec G115
	case *int8:
		*d = int8(n) // #nosec G115
	case *int:
		*d = int(n)
	}

	return b
}

func (b *ValueBinder) intsValue(sourceParam string, dest any, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	values := b.ValuesFunc(sourceParam)
	if len(values) == 0 {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, values, "required field value is empty", nil))
		}

		return b
	}

	return b.ints(sourceParam, values, dest)
}

func (b *ValueBinder) ints(sourceParam string, values []string, dest any) *ValueBinder {
	switch d := dest.(type) {
	case *[]int64:
		tmp := make([]int64, len(values))
		for i, v := range values {
			b.int(sourceParam, v, &tmp[i], 64)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	case *[]int32:
		tmp := make([]int32, len(values))
		for i, v := range values {
			b.int(sourceParam, v, &tmp[i], 32)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	case *[]int16:
		tmp := make([]int16, len(values))
		for i, v := range values {
			b.int(sourceParam, v, &tmp[i], 16)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	case *[]int8:
		tmp := make([]int8, len(values))
		for i, v := range values {
			b.int(sourceParam, v, &tmp[i], 8)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	case *[]int:
		tmp := make([]int, len(values))
		for i, v := range values {
			b.int(sourceParam, v, &tmp[i], 0)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	}

	return b
}

func (b *ValueBinder) Int64s(sourceParam string, dest *[]int64) *ValueBinder {
	return b.intsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustInt64s(sourceParam string, dest *[]int64) *ValueBinder {
	return b.intsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Int32s(sourceParam string, dest *[]int32) *ValueBinder {
	return b.intsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustInt32s(sourceParam string, dest *[]int32) *ValueBinder {
	return b.intsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Int16s(sourceParam string, dest *[]int16) *ValueBinder {
	return b.intsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustInt16s(sourceParam string, dest *[]int16) *ValueBinder {
	return b.intsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Int8s(sourceParam string, dest *[]int8) *ValueBinder {
	return b.intsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustInt8s(sourceParam string, dest *[]int8) *ValueBinder {
	return b.intsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Ints(sourceParam string, dest *[]int) *ValueBinder {
	return b.intsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustInts(sourceParam string, dest *[]int) *ValueBinder {
	return b.intsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Uint64(sourceParam string, dest *uint64) *ValueBinder {
	return b.uintValue(sourceParam, dest, 64, false)
}

func (b *ValueBinder) MustUint64(sourceParam string, dest *uint64) *ValueBinder {
	return b.uintValue(sourceParam, dest, 64, true)
}

func (b *ValueBinder) Uint32(sourceParam string, dest *uint32) *ValueBinder {
	return b.uintValue(sourceParam, dest, 32, false)
}

func (b *ValueBinder) MustUint32(sourceParam string, dest *uint32) *ValueBinder {
	return b.uintValue(sourceParam, dest, 32, true)
}

func (b *ValueBinder) Uint16(sourceParam string, dest *uint16) *ValueBinder {
	return b.uintValue(sourceParam, dest, 16, false)
}

func (b *ValueBinder) MustUint16(sourceParam string, dest *uint16) *ValueBinder {
	return b.uintValue(sourceParam, dest, 16, true)
}

func (b *ValueBinder) Uint8(sourceParam string, dest *uint8) *ValueBinder {
	return b.uintValue(sourceParam, dest, 8, false)
}

func (b *ValueBinder) MustUint8(sourceParam string, dest *uint8) *ValueBinder {
	return b.uintValue(sourceParam, dest, 8, true)
}

func (b *ValueBinder) Byte(sourceParam string, dest *byte) *ValueBinder {
	return b.uintValue(sourceParam, dest, 8, false)
}

func (b *ValueBinder) MustByte(sourceParam string, dest *byte) *ValueBinder {
	return b.uintValue(sourceParam, dest, 8, true)
}

func (b *ValueBinder) Uint(sourceParam string, dest *uint) *ValueBinder {
	return b.uintValue(sourceParam, dest, 0, false)
}

func (b *ValueBinder) MustUint(sourceParam string, dest *uint) *ValueBinder {
	return b.uintValue(sourceParam, dest, 0, true)
}

func (b *ValueBinder) uintValue(sourceParam string, dest any, bitSize int, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValueFunc(sourceParam)
	if value == "" {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		}

		return b
	}

	return b.uint(sourceParam, value, dest, bitSize)
}

func (b *ValueBinder) uint(sourceParam string, value string, dest any, bitSize int) *ValueBinder {
	n, err := strconv.ParseUint(value, 10, bitSize)
	if err != nil {
		if bitSize == 0 {
			b.setError(b.ErrorFunc(sourceParam, []string{value}, "failed to bind field value to uint", err))
			return b
		}

		b.setError(b.ErrorFunc(sourceParam, []string{value}, fmt.Sprintf("failed to bind field value to uint%v", bitSize), err))
		return b
	}

	switch d := dest.(type) {
	case *uint64:
		*d = n
	case *uint32:
		*d = uint32(n) // #nosec G115
	case *uint16:
		*d = uint16(n) // #nosec G115
	case *uint8:
		*d = uint8(n) // #nosec G115
	case *uint:
		*d = uint(n) // #nosec G115
	}

	return b
}

func (b *ValueBinder) uintsValue(sourceParam string, dest any, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	values := b.ValuesFunc(sourceParam)
	if len(values) == 0 {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, values, "required field value is empty", nil))
		}

		return b
	}

	return b.uints(sourceParam, values, dest)
}

func (b *ValueBinder) uints(sourceParam string, values []string, dest any) *ValueBinder {
	switch d := dest.(type) {
	case *[]uint64:
		tmp := make([]uint64, len(values))
		for i, v := range values {
			b.uint(sourceParam, v, &tmp[i], 64)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	case *[]uint32:
		tmp := make([]uint32, len(values))
		for i, v := range values {
			b.uint(sourceParam, v, &tmp[i], 32)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	case *[]uint16:
		tmp := make([]uint16, len(values))
		for i, v := range values {
			b.uint(sourceParam, v, &tmp[i], 16)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	case *[]uint8:
		tmp := make([]uint8, len(values))
		for i, v := range values {
			b.uint(sourceParam, v, &tmp[i], 8)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	case *[]uint:
		tmp := make([]uint, len(values))
		for i, v := range values {
			b.uint(sourceParam, v, &tmp[i], 0)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	}

	return b
}

func (b *ValueBinder) Uint64s(sourceParam string, dest *[]uint64) *ValueBinder {
	return b.uintsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustUint64s(sourceParam string, dest *[]uint64) *ValueBinder {
	return b.uintsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Uint32s(sourceParam string, dest *[]uint32) *ValueBinder {
	return b.uintsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustUint32s(sourceParam string, dest *[]uint32) *ValueBinder {
	return b.uintsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Uint16s(sourceParam string, dest *[]uint16) *ValueBinder {
	return b.uintsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustUint16s(sourceParam string, dest *[]uint16) *ValueBinder {
	return b.uintsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Uint8s(sourceParam string, dest *[]uint8) *ValueBinder {
	return b.uintsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustUint8s(sourceParam string, dest *[]uint8) *ValueBinder {
	return b.uintsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Uints(sourceParam string, dest *[]uint) *ValueBinder {
	return b.uintsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustUints(sourceParam string, dest *[]uint) *ValueBinder {
	return b.uintsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Bool(sourceParam string, dest *bool) *ValueBinder {
	return b.boolValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustBool(sourceParam string, dest *bool) *ValueBinder {
	return b.boolValue(sourceParam, dest, true)
}

func (b *ValueBinder) boolValue(sourceParam string, dest *bool, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValueFunc(sourceParam)
	if value == "" {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		}

		return b
	}

	return b.bool(sourceParam, value, dest)
}

func (b *ValueBinder) bool(sourceParam string, value string, dest *bool) *ValueBinder {
	n, err := strconv.ParseBool(value)
	if err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{value}, "failed to bind field value to bool", err))
		return b
	}

	*dest = n
	return b
}

func (b *ValueBinder) boolsValue(sourceParam string, dest *[]bool, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	values := b.ValuesFunc(sourceParam)
	if len(values) == 0 {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		}

		return b
	}

	return b.bools(sourceParam, values, dest)
}

func (b *ValueBinder) bools(sourceParam string, values []string, dest *[]bool) *ValueBinder {
	tmp := make([]bool, len(values))
	for i, v := range values {
		b.bool(sourceParam, v, &tmp[i])
		if b.failFast && b.errors != nil {
			return b
		}
	}

	if b.errors == nil {
		*dest = tmp
	}

	return b
}

func (b *ValueBinder) Bools(sourceParam string, dest *[]bool) *ValueBinder {
	return b.boolsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustBools(sourceParam string, dest *[]bool) *ValueBinder {
	return b.boolsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Float64(sourceParam string, dest *float64) *ValueBinder {
	return b.floatValue(sourceParam, dest, 64, false)
}

func (b *ValueBinder) MustFloat64(sourceParam string, dest *float64) *ValueBinder {
	return b.floatValue(sourceParam, dest, 64, true)
}

func (b *ValueBinder) Float32(sourceParam string, dest *float32) *ValueBinder {
	return b.floatValue(sourceParam, dest, 32, false)
}

func (b *ValueBinder) MustFloat32(sourceParam string, dest *float32) *ValueBinder {
	return b.floatValue(sourceParam, dest, 32, true)
}

func (b *ValueBinder) floatValue(sourceParam string, dest any, bitSize int, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValueFunc(sourceParam)
	if value == "" {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		}

		return b
	}

	return b.float(sourceParam, value, dest, bitSize)
}

func (b *ValueBinder) float(sourceParam string, value string, dest any, bitSize int) *ValueBinder {
	n, err := strconv.ParseFloat(value, bitSize)
	if err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{value}, fmt.Sprintf("failed to bind field value to float%v", bitSize), err))
		return b
	}

	switch d := dest.(type) {
	case *float64:
		*d = n
	case *float32:
		*d = float32(n)
	}

	return b
}

func (b *ValueBinder) floatsValue(sourceParam string, dest any, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	values := b.ValuesFunc(sourceParam)
	if len(values) == 0 {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		}

		return b
	}

	return b.floats(sourceParam, values, dest)
}

func (b *ValueBinder) floats(sourceParam string, values []string, dest any) *ValueBinder {
	switch d := dest.(type) {
	case *[]float64:
		tmp := make([]float64, len(values))
		for i, v := range values {
			b.float(sourceParam, v, &tmp[i], 64)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	case *[]float32:
		tmp := make([]float32, len(values))
		for i, v := range values {
			b.float(sourceParam, v, &tmp[i], 32)
			if b.failFast && b.errors != nil {
				return b
			}
		}

		if b.errors == nil {
			*d = tmp
		}
	}

	return b
}

func (b *ValueBinder) Float64s(sourceParam string, dest *[]float64) *ValueBinder {
	return b.floatsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustFloat64s(sourceParam string, dest *[]float64) *ValueBinder {
	return b.floatsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Float32s(sourceParam string, dest *[]float32) *ValueBinder {
	return b.floatsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustFloat32s(sourceParam string, dest *[]float32) *ValueBinder {
	return b.floatsValue(sourceParam, dest, true)
}

func (b *ValueBinder) Time(sourceParam string, dest *time.Time, layout string) *ValueBinder {
	return b.time(sourceParam, dest, layout, false)
}

func (b *ValueBinder) MustTime(sourceParam string, dest *time.Time, layout string) *ValueBinder {
	return b.time(sourceParam, dest, layout, true)
}

func (b *ValueBinder) time(sourceParam string, dest *time.Time, layout string, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValueFunc(sourceParam)
	if value == "" {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{value}, "required field value is empty", nil))
		}

		return b
	}

	t, err := time.Parse(layout, value)
	if err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{value}, "failed to bind field value to Time", err))
		return b
	}

	*dest = t
	return b
}

func (b *ValueBinder) Times(sourceParam string, dest *[]time.Time, layout string) *ValueBinder {
	return b.times(sourceParam, dest, layout, false)
}

func (b *ValueBinder) MustTimes(sourceParam string, dest *[]time.Time, layout string) *ValueBinder {
	return b.times(sourceParam, dest, layout, true)
}

func (b *ValueBinder) times(sourceParam string, dest *[]time.Time, layout string, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	values := b.ValuesFunc(sourceParam)
	if len(values) == 0 {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		}

		return b
	}

	tmp := make([]time.Time, len(values))
	for i, v := range values {
		t, err := time.Parse(layout, v)
		if err != nil {
			b.setError(b.ErrorFunc(sourceParam, []string{v}, "failed to bind field value to Time", err))
			if b.failFast {
				return b
			}

			continue
		}

		tmp[i] = t
	}

	if b.errors == nil {
		*dest = tmp
	}

	return b
}

func (b *ValueBinder) Duration(sourceParam string, dest *time.Duration) *ValueBinder {
	return b.duration(sourceParam, dest, false)
}

func (b *ValueBinder) MustDuration(sourceParam string, dest *time.Duration) *ValueBinder {
	return b.duration(sourceParam, dest, true)
}

func (b *ValueBinder) duration(sourceParam string, dest *time.Duration, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValueFunc(sourceParam)
	if value == "" {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{value}, "required field value is empty", nil))
		}

		return b
	}

	t, err := time.ParseDuration(value)
	if err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{value}, "failed to bind field value to Duration", err))
		return b
	}

	*dest = t
	return b
}

func (b *ValueBinder) Durations(sourceParam string, dest *[]time.Duration) *ValueBinder {
	return b.durationsValue(sourceParam, dest, false)
}

func (b *ValueBinder) MustDurations(sourceParam string, dest *[]time.Duration) *ValueBinder {
	return b.durationsValue(sourceParam, dest, true)
}

func (b *ValueBinder) durationsValue(sourceParam string, dest *[]time.Duration, valueMustExist bool) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	values := b.ValuesFunc(sourceParam)
	if len(values) == 0 {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{}, "required field value is empty", nil))
		}

		return b
	}

	return b.durations(sourceParam, values, dest)
}

func (b *ValueBinder) durations(sourceParam string, values []string, dest *[]time.Duration) *ValueBinder {
	tmp := make([]time.Duration, len(values))
	for i, v := range values {
		t, err := time.ParseDuration(v)
		if err != nil {
			b.setError(b.ErrorFunc(sourceParam, []string{v}, "failed to bind field value to Duration", err))
			if b.failFast {
				return b
			}

			continue
		}

		tmp[i] = t
	}

	if b.errors == nil {
		*dest = tmp
	}

	return b
}

func (b *ValueBinder) UnixTime(sourceParam string, dest *time.Time) *ValueBinder {
	return b.unixTime(sourceParam, dest, false, time.Second)
}

func (b *ValueBinder) MustUnixTime(sourceParam string, dest *time.Time) *ValueBinder {
	return b.unixTime(sourceParam, dest, true, time.Second)
}

func (b *ValueBinder) UnixTimeMilli(sourceParam string, dest *time.Time) *ValueBinder {
	return b.unixTime(sourceParam, dest, false, time.Millisecond)
}

func (b *ValueBinder) MustUnixTimeMilli(sourceParam string, dest *time.Time) *ValueBinder {
	return b.unixTime(sourceParam, dest, true, time.Millisecond)
}

func (b *ValueBinder) UnixTimeNano(sourceParam string, dest *time.Time) *ValueBinder {
	return b.unixTime(sourceParam, dest, false, time.Nanosecond)
}

func (b *ValueBinder) MustUnixTimeNano(sourceParam string, dest *time.Time) *ValueBinder {
	return b.unixTime(sourceParam, dest, true, time.Nanosecond)
}

func (b *ValueBinder) unixTime(sourceParam string, dest *time.Time, valueMustExist bool, precision time.Duration) *ValueBinder {
	if b.failFast && b.errors != nil {
		return b
	}

	value := b.ValueFunc(sourceParam)
	if value == "" {
		if valueMustExist {
			b.setError(b.ErrorFunc(sourceParam, []string{value}, "required field value is empty", nil))
		}

		return b
	}

	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		b.setError(b.ErrorFunc(sourceParam, []string{value}, "failed to bind field value to Time", err))
		return b
	}

	switch precision {
	case time.Second:
		*dest = time.Unix(n, 0)
	case time.Millisecond:
		*dest = time.UnixMilli(n)
	case time.Nanosecond:
		*dest = time.Unix(0, n)
	}

	return b
}
