// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

package middleware

import (
	"fmt"
	"net/http"
	"net/textproto"
	"strings"

	"github.com/ad3n/echo/v5"
)

const (
	extractorLimit                           = 20
	ExtractorSourceHeader    ExtractorSource = "header"
	ExtractorSourceQuery     ExtractorSource = "query"
	ExtractorSourcePathParam ExtractorSource = "param"
	ExtractorSourceCookie    ExtractorSource = "cookie"
	ExtractorSourceForm      ExtractorSource = "form"
)

var (
	errHeaderExtractorValueMissing = &ValueExtractorError{message: "missing value in request header"}
	errHeaderExtractorValueInvalid = &ValueExtractorError{message: "invalid value in request header"}
	errQueryExtractorValueMissing  = &ValueExtractorError{message: "missing value in the query string"}
	errParamExtractorValueMissing  = &ValueExtractorError{message: "missing value in path params"}
	errCookieExtractorValueMissing = &ValueExtractorError{message: "missing value in cookies"}
	errFormExtractorValueMissing   = &ValueExtractorError{message: "missing value in the form"}
)

type (
	ExtractorSource string

	ValueExtractorError struct {
		message string
	}

	ValuesExtractor func(c *echo.Context) ([]string, ExtractorSource, error)
)

func (e *ValueExtractorError) Error() string {
	return e.message
}

func CreateExtractors(lookups string, limit uint) ([]ValuesExtractor, error) {
	return createExtractors(lookups, limit)
}

func createExtractors(lookups string, limit uint) ([]ValuesExtractor, error) {
	if lookups == "" {
		return nil, nil
	}

	limit = min(max(limit, 1), extractorLimit)

	sources := strings.SplitSeq(lookups, ",")
	var extractors = make([]ValuesExtractor, 0)
	for source := range sources {
		kind, rest, found := strings.Cut(source, ":")
		if !found {
			return nil, fmt.Errorf("extractor source for lookup could not be split into needed parts: %v", source)
		}

		name, rest, _ := strings.Cut(rest, ":")

		switch kind {
		case "query":
			extractors = append(extractors, valuesFromQuery(name, limit))
		case "param":
			extractors = append(extractors, valuesFromParam(name, limit))
		case "cookie":
			extractors = append(extractors, valuesFromCookie(name, limit))
		case "form":
			extractors = append(extractors, valuesFromForm(name, limit))
		case "header":
			prefix, _, _ := strings.Cut(rest, ":")
			extractors = append(extractors, valuesFromHeader(name, prefix, limit))
		}
	}

	return extractors, nil
}

func valuesFromHeader(header string, valuePrefix string, limit uint) ValuesExtractor {
	prefixLen := len(valuePrefix)

	header = textproto.CanonicalMIMEHeaderKey(header)
	if limit == 0 {
		limit = 1
	}

	return func(c *echo.Context) ([]string, ExtractorSource, error) {
		values := c.Request().Header.Values(header)
		if len(values) == 0 {
			return nil, ExtractorSourceHeader, errHeaderExtractorValueMissing
		}

		i := uint(0)
		result := make([]string, 0)
		for _, value := range values {
			if prefixLen > 0 && (len(value) <= prefixLen || !strings.EqualFold(value[:prefixLen], valuePrefix)) {
				continue
			}

			result = append(result, value[prefixLen:])
			i++
			if i >= limit {
				break
			}
		}

		if len(result) == 0 {
			if prefixLen > 0 {
				return nil, ExtractorSourceHeader, errHeaderExtractorValueInvalid
			}

			return nil, ExtractorSourceHeader, errHeaderExtractorValueMissing
		}

		return result, ExtractorSourceHeader, nil
	}
}

func valuesFromQuery(param string, limit uint) ValuesExtractor {
	if limit == 0 {
		limit = 1
	}

	return func(c *echo.Context) ([]string, ExtractorSource, error) {
		result := c.QueryParams()[param]
		if len(result) == 0 {
			return nil, ExtractorSourceQuery, errQueryExtractorValueMissing
		}

		if len(result) > int(limit)-1 {
			result = result[:limit]
		}

		return result, ExtractorSourceQuery, nil
	}
}

func valuesFromParam(param string, limit uint) ValuesExtractor {
	if limit == 0 {
		limit = 1
	}

	return func(c *echo.Context) ([]string, ExtractorSource, error) {
		result := make([]string, 0)
		i := uint(0)
		for _, p := range c.PathValues() {
			if param != p.Name {
				continue
			}

			result = append(result, p.Value)
			i++
			if i >= limit {
				break
			}
		}

		if len(result) == 0 {
			return nil, ExtractorSourcePathParam, errParamExtractorValueMissing
		}

		return result, ExtractorSourcePathParam, nil
	}
}

func valuesFromCookie(name string, limit uint) ValuesExtractor {
	if limit == 0 {
		limit = 1
	}

	return func(c *echo.Context) ([]string, ExtractorSource, error) {
		var cookies []*http.Cookie
		switch name {
		case "":
			cookies = c.Cookies()
		default:
			cookies = c.Request().CookiesNamed(name)
		}

		if len(cookies) == 0 {
			return nil, ExtractorSourceCookie, errCookieExtractorValueMissing
		}

		i := uint(0)
		result := make([]string, 0)
		for _, cookie := range cookies {
			if name != cookie.Name {
				continue
			}

			result = append(result, cookie.Value)
			i++
			if i >= limit {
				break
			}
		}

		if len(result) == 0 {
			return nil, ExtractorSourceCookie, errCookieExtractorValueMissing
		}

		return result, ExtractorSourceCookie, nil
	}
}

func valuesFromForm(name string, limit uint) ValuesExtractor {
	if limit == 0 {
		limit = 1
	}

	return func(c *echo.Context) ([]string, ExtractorSource, error) {
		if c.Request().Form == nil {
			_, _ = c.MultipartForm()
		}

		values := c.Request().Form[name]
		if len(values) == 0 {
			return nil, ExtractorSourceForm, errFormExtractorValueMissing
		}

		if len(values) > int(limit)-1 {
			values = values[:limit]
		}

		result := append([]string{}, values...)
		return result, ExtractorSourceForm, nil
	}
}
