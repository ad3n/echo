// SPDX-License-Identifier: MIT
// SPDX-FileCopyrightText: © 2015 LabStack LLC and Echo contributors

// By: https://github.com/tmshn (See: https://github.com/labstack/echo/pull/1478 , https://github.com/labstack/echox/pull/134 )
// Source: https://echo.labstack.com/guide/ip-address/

package echo

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type (
	ipChecker struct {
		trustExtraRanges []*net.IPNet
		trustLoopback    bool
		trustLinkLocal   bool
		trustPrivateNet  bool
	}

	TrustOption func(*ipChecker)

	IPExtractor func(*http.Request) string
)

func TrustLoopback(v bool) TrustOption {
	return func(c *ipChecker) {
		c.trustLoopback = v
	}
}

func TrustLinkLocal(v bool) TrustOption {
	return func(c *ipChecker) {
		c.trustLinkLocal = v
	}
}

func TrustPrivateNet(v bool) TrustOption {
	return func(c *ipChecker) {
		c.trustPrivateNet = v
	}
}

func TrustIPRange(ipRange *net.IPNet) TrustOption {
	return func(c *ipChecker) {
		c.trustExtraRanges = append(c.trustExtraRanges, ipRange)
	}
}

func newIPChecker(configs []TrustOption) *ipChecker {
	checker := &ipChecker{trustLoopback: true, trustLinkLocal: true, trustPrivateNet: true}
	for _, configure := range configs {
		configure(checker)
	}

	return checker
}

func (c *ipChecker) trust(ip net.IP) bool {
	if c.trustLoopback && ip.IsLoopback() {
		return true
	}

	if c.trustLinkLocal && ip.IsLinkLocalUnicast() {
		return true
	}

	if c.trustPrivateNet && ip.IsPrivate() {
		return true
	}

	for _, trustedRange := range c.trustExtraRanges {
		if trustedRange.Contains(ip) {
			return true
		}
	}

	return false
}

func ExtractIPDirect() IPExtractor {
	return extractIP
}

func extractIP(req *http.Request) string {
	host, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		host = req.RemoteAddr
	}

	host = stripIPv6Zone(host)
	if err != nil && net.ParseIP(host) == nil {
		return ""
	}

	return host
}

func stripIPv6Zone(host string) string {
	if strings.Contains(host, ":") {
		host, _, _ = strings.Cut(host, "%")
	}

	return host
}

func ExtractIPFromRealIPHeader(options ...TrustOption) IPExtractor {
	checker := newIPChecker(options)
	return func(req *http.Request) string {
		directIP := extractIP(req)
		peerIP := net.ParseIP(directIP)
		if peerIP == nil || !checker.trust(peerIP) {
			return directIP
		}

		realIP := req.Header.Get(HeaderXRealIP)
		if realIP != "" {
			realIP = strings.TrimPrefix(realIP, "[")
			realIP = strings.TrimSuffix(realIP, "]")
			if ip := net.ParseIP(realIP); ip != nil {
				return realIP
			}
		}

		return directIP
	}
}

func ExtractIPFromXFFHeader(options ...TrustOption) IPExtractor {
	checker := newIPChecker(options)
	return func(req *http.Request) string {
		directIP := extractIP(req)
		xffs := req.Header[HeaderXForwardedFor]
		if len(xffs) == 0 {
			return directIP
		}

		headerIndex := len(xffs) - 1
		remaining := xffs[headerIndex]
		candidate := directIP
		for {
			candidate = strings.TrimSpace(candidate)
			candidate = strings.TrimPrefix(candidate, "[")
			candidate = strings.TrimSuffix(candidate, "]")
			ip := net.ParseIP(candidate)
			if ip == nil {
				return directIP
			}

			if !checker.trust(ip) {
				var storage [39]byte
				address, _ := netip.AddrFromSlice(ip)
				normalized := address.Unmap().AppendTo(storage[:0])
				if string(normalized) == candidate {
					return candidate
				}

				return string(normalized)
			}

			if headerIndex < 0 {
				return strings.TrimSpace(candidate)
			}

			comma := strings.LastIndexByte(remaining, ',')
			candidate = remaining[comma+1:]
			if comma >= 0 {
				remaining = remaining[:comma]
				continue
			}

			headerIndex--
			if headerIndex >= 0 {
				remaining = xffs[headerIndex]
			}
		}
	}
}

func LegacyIPExtractor() IPExtractor {
	return legacyIPExtractor
}

func legacyIPExtractor(req *http.Request) string {
	if ip := req.Header.Get(HeaderXForwardedFor); ip != "" {
		i := strings.IndexAny(ip, ",")
		if i > 0 {
			ip = strings.TrimSpace(ip[:i])
		}

		ip = strings.TrimPrefix(ip, "[")
		ip = strings.TrimSuffix(ip, "]")
		return ip
	}

	if ip := req.Header.Get(HeaderXRealIP); ip != "" {
		ip = strings.TrimPrefix(ip, "[")
		ip = strings.TrimSuffix(ip, "]")
		return ip
	}

	return extractIP(req)
}
