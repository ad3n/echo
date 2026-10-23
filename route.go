// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package echo

import (
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
)

type (
	Route struct {
		Method string
		Path   string
		Name   string

		Handler     HandlerFunc
		Middlewares []MiddlewareFunc

		allowOverwrite bool
	}

	RouteInfo struct {
		Name       string
		Method     string
		Path       string
		Parameters []string
	}
)

func (r Route) ToRouteInfo(params []string) RouteInfo {
	name := r.Name
	if name == "" {
		name = r.Method + ":" + r.Path
	}

	return RouteInfo{
		Method:     r.Method,
		Path:       r.Path,
		Parameters: append([]string(nil), params...),
		Name:       name,
	}
}

func (r Route) WithPrefix(pathPrefix string, middlewares []MiddlewareFunc) Route {
	r.Path = pathPrefix + r.Path

	if len(middlewares) > 0 {
		m := make([]MiddlewareFunc, 0, len(middlewares)+len(r.Middlewares))
		m = append(m, middlewares...)
		m = append(m, r.Middlewares...)
		r.Middlewares = m
	}

	return r
}

func (r RouteInfo) Clone() RouteInfo {
	return RouteInfo{
		Name:       r.Name,
		Method:     r.Method,
		Path:       r.Path,
		Parameters: append([]string(nil), r.Parameters...),
	}
}

func (r RouteInfo) Reverse(pathValues ...any) string {
	if len(pathValues) == 0 {
		return strings.ReplaceAll(r.Path, `\:`, ":")
	}

	var uri strings.Builder
	uri.Grow(len(r.Path))
	nextValue := 0
	walkRoutePath(r.Path, func(part routePathPart) {
		if part.kind == staticKind {
			uri.WriteString(part.value)
			return
		}

		if nextValue < len(pathValues) {
			switch value := pathValues[nextValue].(type) {
			case string:
				uri.WriteString(value)
			case int:
				var storage [20]byte
				uri.Write(strconv.AppendInt(storage[:0], int64(value), 10))
			case int64:
				var storage [20]byte
				uri.Write(strconv.AppendInt(storage[:0], value, 10))
			case uint64:
				var storage [20]byte
				uri.Write(strconv.AppendUint(storage[:0], value, 10))
			default:
				uri.WriteString(fmt.Sprint(value))
			}

			nextValue++
			return
		}

		if part.kind == paramKind {
			uri.WriteByte(paramLabel)
		}

		uri.WriteString(strings.ReplaceAll(part.value, `\:`, ":"))
	})
	return uri.String()
}

func HandlerName(h HandlerFunc) string {
	t := reflect.ValueOf(h).Type()
	if t.Kind() == reflect.Func {
		return runtime.FuncForPC(reflect.ValueOf(h).Pointer()).Name()
	}

	return t.String()
}

func (r Routes) Clone() Routes {
	result := make(Routes, len(r))
	for i, route := range r {
		result[i] = route.Clone()
	}

	return result
}

func (r Routes) Reverse(routeName string, pathValues ...any) (string, error) {
	for _, rr := range r {
		if rr.Name == routeName {
			return rr.Reverse(pathValues...), nil
		}
	}

	return "", errors.New("route not found")
}

func (r Routes) FindByMethodPath(method string, path string) (RouteInfo, error) {
	if r == nil {
		return RouteInfo{}, errors.New("route not found by method and path")
	}

	for _, rr := range r {
		if rr.Method == method && rr.Path == path {
			return rr, nil
		}
	}

	return RouteInfo{}, errors.New("route not found by method and path")
}

func (r Routes) FilterByMethod(method string) (Routes, error) {
	if r == nil {
		return nil, errors.New("route not found by method")
	}

	result := make(Routes, 0)
	for _, rr := range r {
		if rr.Method == method {
			result = append(result, rr)
		}
	}

	if len(result) == 0 {
		return nil, errors.New("route not found by method")
	}

	return result, nil
}

func (r Routes) FilterByPath(path string) (Routes, error) {
	if r == nil {
		return nil, errors.New("route not found by path")
	}

	result := make(Routes, 0)
	for _, rr := range r {
		if rr.Path == path {
			result = append(result, rr)
		}
	}

	if len(result) == 0 {
		return nil, errors.New("route not found by path")
	}

	return result, nil
}

func (r Routes) FilterByName(name string) (Routes, error) {
	if r == nil {
		return nil, errors.New("route not found by name")
	}

	result := make(Routes, 0)
	for _, rr := range r {
		if rr.Name == name {
			result = append(result, rr)
		}
	}

	if len(result) == 0 {
		return nil, errors.New("route not found by name")
	}

	return result, nil
}
