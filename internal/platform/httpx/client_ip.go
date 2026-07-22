package httpx

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type clientIPContextKey struct{}

func ClientIP(trusted []netip.Prefix, next http.Handler) http.Handler {
	trusted = append([]netip.Prefix(nil), trusted...)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := directIP(r.RemoteAddr)
		if ip.IsValid() && containsPrefix(trusted, ip) {
			if forwarded, ok := forwardedIP(r, trusted); ok {
				ip = forwarded
			}
		}
		value := ""
		if ip.IsValid() {
			value = ip.String()
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPContextKey{}, value)))
	})
}

func ClientIPFromRequest(r *http.Request) string {
	if value, ok := r.Context().Value(clientIPContextKey{}).(string); ok {
		return value
	}
	ip := directIP(r.RemoteAddr)
	if ip.IsValid() {
		return ip.String()
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func forwardedIP(r *http.Request, trusted []netip.Prefix) (netip.Addr, bool) {
	if values := r.Header.Values("X-Forwarded-For"); len(values) > 0 {
		forwarded := strings.TrimSpace(strings.Join(values, ","))
		if forwarded == "" {
			return netip.Addr{}, false
		}
		parts := strings.Split(forwarded, ",")
		addresses := make([]netip.Addr, len(parts))
		for i, part := range parts {
			address, err := netip.ParseAddr(strings.TrimSpace(part))
			if err != nil || address.Zone() != "" {
				return netip.Addr{}, false
			}
			addresses[i] = address.Unmap()
		}
		for i := len(addresses) - 1; i >= 0; i-- {
			if !containsPrefix(trusted, addresses[i]) {
				return addresses[i], true
			}
		}
		return addresses[0], true
	}
	return realIP(r)
}

func realIP(r *http.Request) (netip.Addr, bool) {
	values := r.Header.Values("X-Real-IP")
	if len(values) != 1 {
		return netip.Addr{}, false
	}
	value := strings.TrimSpace(values[0])
	if value == "" || strings.Contains(value, ",") {
		return netip.Addr{}, false
	}
	address, err := netip.ParseAddr(value)
	return address.Unmap(), err == nil && address.Zone() == ""
}

func directIP(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remoteAddr))
	if err != nil {
		host = strings.TrimSpace(remoteAddr)
	}
	address, err := netip.ParseAddr(host)
	if err != nil || address.Zone() != "" {
		return netip.Addr{}
	}
	return address.Unmap()
}

func containsPrefix(prefixes []netip.Prefix, address netip.Addr) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
