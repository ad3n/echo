// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"encoding"
	"encoding/xml"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Binder is the interface that wraps the Bind method.
type Binder interface {
	Bind(c *Context, target any) error
}

// DefaultBinder is the default implementation of the Binder interface.
// For path, query, header, and form binding, time.Time and *time.Time fields (not slices)
// may use a format tag: "date-time" uses the standard RFC3339 decoding, "date-time-local"
// accepts "2006-01-02T15:04:05" with optional fractional seconds, "date" accepts
// "2006-01-02", and other values specify Go time layouts. "date", "date-time-local", and
// layouts without a timezone give UTC times. JSON and XML decoding do not use this tag.
// HTML datetime-local inputs omit seconds by default, so bind them with a Go layout
// such as "2006-01-02T15:04" instead of "date-time-local".
type DefaultBinder struct{}

// BindUnmarshaler is the interface used to wrap the UnmarshalParam method.
// Types that don't implement this, but do implement encoding.TextUnmarshaler
// will use that interface instead.
type BindUnmarshaler interface {
	// UnmarshalParam decodes and assigns a value from an form or query param.
	UnmarshalParam(param string) error
}

// bindMultipleUnmarshaler is used by binder to unmarshal multiple values from request at once to
// type implementing this interface. For example request could have multiple query fields `?a=1&a=2&b=test` in that case
// for `a` following slice `["1", "2"] will be passed to unmarshaller.
type bindMultipleUnmarshaler interface {
	UnmarshalParams(params []string) error
}

// BindPathValues binds path parameter values to bindable object
func BindPathValues(c *Context, target any) error {
	params := map[string][]string{}
	for _, param := range c.PathValues() {
		params[param.Name] = []string{param.Value}
	}
	if err := bindData(target, params, "param", nil); err != nil {
		return ErrBadRequest.Wrap(err)
	}
	return nil
}

// BindQueryParams binds query params to bindable object
func BindQueryParams(c *Context, target any) error {
	if err := bindData(target, c.QueryParams(), "query", nil); err != nil {
		return ErrBadRequest.Wrap(err)
	}
	return nil
}

// BindBody binds request body contents to bindable object
// NB: then binding forms take note that this implementation uses standard library form parsing
// which parses form data from BOTH URL and BODY if content type is not MIMEMultipartForm
// See non-MIMEMultipartForm: https://golang.org/pkg/net/http/#Request.ParseForm
// See MIMEMultipartForm: https://golang.org/pkg/net/http/#Request.ParseMultipartForm
func BindBody(c *Context, target any) (err error) {
	req := c.Request()
	if req.ContentLength == 0 {
		return
	}

	// mediatype is found like `mime.ParseMediaType()` does it
	base, _, _ := strings.Cut(req.Header.Get(HeaderContentType), ";")
	mediatype := strings.TrimSpace(base)

	switch mediatype {
	case MIMEApplicationJSON:
		if err = c.Echo().JSONSerializer.Deserialize(c, target); err != nil {
			return wrapBindBodyError(err)
		}
	case MIMEApplicationXML, MIMETextXML:
		if err = xml.NewDecoder(req.Body).Decode(target); err != nil {
			return wrapBindBodyError(err)
		}
	case MIMEApplicationForm:
		params, err := c.FormValues()
		if err != nil {
			return wrapBindBodyError(err)
		}
		if err = bindData(target, params, "form", nil); err != nil {
			return ErrBadRequest.Wrap(err)
		}
	case MIMEMultipartForm:
		params, err := c.MultipartForm()
		if err != nil {
			return wrapBindBodyError(err)
		}
		if err = bindData(target, params.Value, "form", params.File); err != nil {
			return ErrBadRequest.Wrap(err)
		}
	default:
		return &HTTPError{Code: http.StatusUnsupportedMediaType}
	}
	return nil
}

func wrapBindBodyError(err error) error {
	if StatusCode(err) != 0 {
		return err
	}
	return ErrBadRequest.Wrap(err)
}

// BindHeaders binds HTTP headers to a bindable object
func BindHeaders(c *Context, target any) error {
	if err := bindData(target, c.Request().Header, "header", nil); err != nil {
		return ErrBadRequest.Wrap(err)
	}
	return nil
}

// Bind implements the `Binder#Bind` function.
// Binding is done in following order: 1) path params; 2) query params; 3) request body. Each step COULD override previous
// step bound values. For single source binding use their own methods BindBody, BindQueryParams, BindPathValues.
func (b *DefaultBinder) Bind(c *Context, target any) error {
	if err := BindPathValues(c, target); err != nil {
		return err
	}
	// Only bind query parameters for GET/DELETE/HEAD/QUERY to avoid unexpected behavior with destination struct binding from body.
	// For example a request URL `&id=1&lang=en` with body `{"id":100,"lang":"de"}` would lead to precedence issues.
	// The HTTP method check restores pre-v4.1.11 behavior to avoid these problems (see issue #1670)
	method := c.Request().Method
	if method == http.MethodGet || method == http.MethodDelete || method == http.MethodHead || method == QUERY {
		if err := BindQueryParams(c, target); err != nil {
			return err
		}
	}
	return BindBody(c, target)
}

type bindFieldMeta struct {
	formatTag                  string
	param, query, form, header string
	index                      int
	fieldKind                  reflect.Kind
	anonymous                  bool
	plainScalar                bool
}

// tagName returns the field's tag value for the given binding source tag.
// Keep in sync with the tag fields above and the f.Tag.Get calls in bindMetaFor.
func (m *bindFieldMeta) tagName(tag string) string {
	switch tag {
	case "param":
		return m.param
	case "query":
		return m.query
	case "form":
		return m.form
	case "header":
		return m.header
	default:
		return ""
	}
}

// bindStructMeta is the cached field metadata for a whole struct type, in declaration order.
type bindStructMeta struct {
	fields []bindFieldMeta
}

// bindStructCache memoizes bindStructMeta keyed by struct reflect.Type. Concurrent double-computation is
// harmless because the result is deterministic and idempotent.
var bindStructCache sync.Map // map[reflect.Type]*bindStructMeta

func bindMetaFor(typ reflect.Type) *bindStructMeta {
	if cached, ok := bindStructCache.Load(typ); ok {
		return cached.(*bindStructMeta)
	}
	n := typ.NumField()
	meta := &bindStructMeta{fields: make([]bindFieldMeta, n)}
	for i := range n {
		f := typ.Field(i)
		kind := f.Type.Kind()
		meta.fields[i] = bindFieldMeta{
			index:       i,
			plainScalar: f.Type.PkgPath() == "" && (kind == reflect.String || kind >= reflect.Bool && kind <= reflect.Float64),
			anonymous:   f.Anonymous,
			fieldKind:   kind,
			formatTag:   f.Tag.Get("format"),
			param:       f.Tag.Get("param"),
			query:       f.Tag.Get("query"),
			form:        f.Tag.Get("form"),
			header:      f.Tag.Get("header"),
		}
	}
	bindStructCache.Store(typ, meta)
	return meta
}

func bindData(destination any, data map[string][]string, tag string, dataFiles map[string][]*multipart.FileHeader) error {
	if destination == nil || (len(data) == 0 && len(dataFiles) == 0) {
		return nil
	}
	hasFiles := len(dataFiles) > 0
	typ := reflect.TypeOf(destination).Elem()
	val := reflect.ValueOf(destination).Elem()

	if typ.Kind() == reflect.Map && typ.Key().Kind() == reflect.String {
		k := typ.Elem().Kind()
		isElemInterface := k == reflect.Interface
		isElemString := k == reflect.String
		isElemSliceOfStrings := k == reflect.Slice && typ.Elem().Elem().Kind() == reflect.String
		if !isElemSliceOfStrings && !isElemString && !isElemInterface {
			return nil
		}
		if val.IsNil() {
			val.Set(reflect.MakeMap(typ))
		}
		for k, v := range data {
			if len(v) == 0 && !isElemSliceOfStrings {
				continue
			}
			if isElemString {
				val.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(v[0]))
			} else if isElemInterface {
				val.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(v[0]))
			} else {
				val.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(v))
			}
		}
		return nil
	}

	if typ.Kind() != reflect.Struct {
		if tag == "param" || tag == "query" || tag == "header" {
			return nil
		}
		return errors.New("binding element must be a struct")
	}

	meta := bindMetaFor(typ)
	for fi := range meta.fields {
		fm := &meta.fields[fi]
		inputFieldName := fm.tagName(tag)
		if inputFieldName == "" && !fm.anonymous && fm.fieldKind != reflect.Struct {
			continue
		}

		structField := val.Field(fm.index)
		if fm.anonymous {
			if structField.Kind() == reflect.Pointer {
				structField = structField.Elem()
			}
		}
		if !structField.CanSet() {
			continue
		}
		structFieldKind := structField.Kind()
		if fm.anonymous && structFieldKind == reflect.Struct && inputFieldName != "" {
			return errors.New("query/param/form tags are not allowed with anonymous struct field")
		}

		if inputFieldName == "" {
			if _, ok := structField.Addr().Interface().(BindUnmarshaler); !ok && structFieldKind == reflect.Struct {
				if err := bindData(structField.Addr().Interface(), data, tag, dataFiles); err != nil {
					return err
				}
			}

			continue
		}

		if hasFiles {
			if ok, err := isFieldMultipartFile(structField.Type()); err != nil {
				return err
			} else if ok {
				if ok := setMultipartFileHeaderTypes(structField, inputFieldName, dataFiles); ok {
					continue
				}
			}
		}

		inputValue, exists := data[inputFieldName]
		if !exists {
			for k, v := range data {
				if strings.EqualFold(k, inputFieldName) {
					inputValue = v
					exists = true
					break
				}
			}
		}

		if !exists {
			continue
		}
		if len(inputValue) == 0 {
			value := structField
			if structFieldKind != reflect.Pointer {
				value = structField.Addr()
			}
			if _, multiple := value.Interface().(bindMultipleUnmarshaler); !multiple {
				continue
			}
		}

		if fm.plainScalar {
			if err := setScalarField(structFieldKind, inputValue[0], structField); err != nil {
				return fmt.Errorf("%s: %w", inputFieldName, err)
			}

			continue
		}

		if ok, err := unmarshalInputsToField(fm.fieldKind, inputValue, structField); ok {
			if err != nil {
				return fmt.Errorf("%s: %w", inputFieldName, err)
			}
			continue
		}

		if ok, err := unmarshalInputToField(fm.fieldKind, inputValue[0], structField, fm.formatTag); ok {
			if err != nil {
				return fmt.Errorf("%s: %w", inputFieldName, err)
			}
			continue
		}

		if structFieldKind == reflect.Pointer {
			structFieldKind = structField.Elem().Kind()
			structField = structField.Elem()
		}

		if structFieldKind == reflect.Slice {
			sliceOf := structField.Type().Elem().Kind()
			numElems := len(inputValue)
			slice := reflect.MakeSlice(structField.Type(), numElems, numElems)
			for j := range numElems {
				if err := setWithProperType(sliceOf, inputValue[j], slice.Index(j)); err != nil {
					return fmt.Errorf("%s: %w", inputFieldName, err)
				}
			}
			structField.Set(slice)
			continue
		}

		if err := setWithProperType(structFieldKind, inputValue[0], structField); err != nil {
			return fmt.Errorf("%s: %w", inputFieldName, err)
		}
	}
	return nil
}

func setWithProperType(valueKind reflect.Kind, val string, structField reflect.Value) error {
	if ok, err := unmarshalInputToField(valueKind, val, structField, ""); ok {
		return err
	}

	return setScalarField(valueKind, val, structField)
}

func setScalarField(valueKind reflect.Kind, val string, structField reflect.Value) error {
	switch valueKind {
	case reflect.Pointer:
		return setWithProperType(structField.Elem().Kind(), val, structField.Elem())
	case reflect.Int:
		return setIntField(val, 0, structField)
	case reflect.Int8:
		return setIntField(val, 8, structField)
	case reflect.Int16:
		return setIntField(val, 16, structField)
	case reflect.Int32:
		return setIntField(val, 32, structField)
	case reflect.Int64:
		return setIntField(val, 64, structField)
	case reflect.Uint:
		return setUintField(val, 0, structField)
	case reflect.Uint8:
		return setUintField(val, 8, structField)
	case reflect.Uint16:
		return setUintField(val, 16, structField)
	case reflect.Uint32:
		return setUintField(val, 32, structField)
	case reflect.Uint64:
		return setUintField(val, 64, structField)
	case reflect.Bool:
		return setBoolField(val, structField)
	case reflect.Float32:
		return setFloatField(val, 32, structField)
	case reflect.Float64:
		return setFloatField(val, 64, structField)
	case reflect.String:
		structField.SetString(val)
	default:
		return errors.New("unknown type")
	}
	return nil
}

func unmarshalInputsToField(valueKind reflect.Kind, values []string, field reflect.Value) (bool, error) {
	if valueKind == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		field = field.Elem()
	}

	fieldIValue := field.Addr().Interface()
	unmarshaler, ok := fieldIValue.(bindMultipleUnmarshaler)
	if !ok {
		return false, nil
	}
	return true, unmarshaler.UnmarshalParams(values)
}

func unmarshalInputToField(valueKind reflect.Kind, val string, field reflect.Value, formatTag string) (bool, error) {
	if valueKind == reflect.Pointer {
		if field.IsNil() {
			field.Set(reflect.New(field.Type().Elem()))
		}
		field = field.Elem()
	}

	fieldIValue := field.Addr().Interface()
	// date-time uses the same TextUnmarshaler as an untagged time.Time.
	if formatTag != "" && formatTag != "date-time" {
		if _, isTime := fieldIValue.(*time.Time); isTime {
			// OpenAPI dates and local date-times have no timezone. time.Parse assigns UTC,
			// as it does for custom layouts without timezone information.
			layout := formatTag
			switch formatTag {
			case "date":
				layout = "2006-01-02"
			case "date-time-local":
				layout = "2006-01-02T15:04:05"
			}
			t, err := time.Parse(layout, val)
			if err != nil {
				return true, err
			}
			field.Set(reflect.ValueOf(t))
			return true, nil
		}
	}

	switch unmarshaler := fieldIValue.(type) {
	case BindUnmarshaler:
		return true, unmarshaler.UnmarshalParam(val)
	case encoding.TextUnmarshaler:
		return true, unmarshaler.UnmarshalText([]byte(val))
	}

	return false, nil
}

func setIntField(value string, bitSize int, field reflect.Value) error {
	if value == "" {
		value = "0"
	}
	intVal, err := strconv.ParseInt(value, 10, bitSize)
	if err == nil {
		field.SetInt(intVal)
	}
	return err
}

func setUintField(value string, bitSize int, field reflect.Value) error {
	if value == "" {
		value = "0"
	}
	uintVal, err := strconv.ParseUint(value, 10, bitSize)
	if err == nil {
		field.SetUint(uintVal)
	}
	return err
}

func setBoolField(value string, field reflect.Value) error {
	if value == "" {
		value = "false"
	}
	boolVal, err := strconv.ParseBool(value)
	if err == nil {
		field.SetBool(boolVal)
	}
	return err
}

func setFloatField(value string, bitSize int, field reflect.Value) error {
	if value == "" {
		value = "0.0"
	}
	floatVal, err := strconv.ParseFloat(value, bitSize)
	if err == nil {
		field.SetFloat(floatVal)
	}
	return err
}

var (
	// NOT supported by bind as you can NOT check easily empty struct being actual file or not
	multipartFileHeaderType = reflect.TypeFor[multipart.FileHeader]()
	// supported by bind as you can check by nil value if file existed or not
	multipartFileHeaderPointerType      = reflect.TypeFor[*multipart.FileHeader]()
	multipartFileHeaderSliceType        = reflect.TypeFor[[]multipart.FileHeader]()
	multipartFileHeaderPointerSliceType = reflect.TypeFor[[]*multipart.FileHeader]()
)

func isFieldMultipartFile(field reflect.Type) (bool, error) {
	switch field {
	case multipartFileHeaderPointerType,
		multipartFileHeaderSliceType,
		multipartFileHeaderPointerSliceType:
		return true, nil
	case multipartFileHeaderType:
		return true, errors.New("binding to multipart.FileHeader struct is not supported, use pointer to struct")
	default:
		return false, nil
	}
}

func setMultipartFileHeaderTypes(structField reflect.Value, inputFieldName string, files map[string][]*multipart.FileHeader) bool {
	fileHeaders := files[inputFieldName]
	if len(fileHeaders) == 0 {
		return false
	}

	result := true
	switch structField.Type() {
	case multipartFileHeaderPointerSliceType:
		structField.Set(reflect.ValueOf(fileHeaders))
	case multipartFileHeaderSliceType:
		headers := make([]multipart.FileHeader, len(fileHeaders))
		for i, fileHeader := range fileHeaders {
			headers[i] = *fileHeader
		}
		structField.Set(reflect.ValueOf(headers))
	case multipartFileHeaderPointerType:
		structField.Set(reflect.ValueOf(fileHeaders[0]))
	default:
		result = false
	}

	return result
}
