// Package loopback tells if a host name is this machine.
package loopback

import (
	"net"
	"strings"
)

// Host tells if host (without port; brackets allowed) is "localhost" or a loopback IP.
func Host(host string) bool {
	host = strings.Trim(host, "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
