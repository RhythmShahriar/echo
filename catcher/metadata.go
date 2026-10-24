// Copyright (c) 2026 Rhythm Shahriar <me@rhy.io>
// SPDX-License-Identifier: MIT

package catcher

import (
	"net"
	"strings"
)

// metadataNames are the names clouds give their metadata endpoint.
var metadataNames = map[string]bool{
	"metadata.google.internal":   true,
	"metadata.goog":              true,
	"instance-data.ec2.internal": true,
}

var (
	// linkLocal is 169.254.0.0/16: AWS, GCP and Azure answer on
	// 169.254.169.254, ECS task credentials on 169.254.170.2, EKS Pod
	// Identity on 169.254.170.23 and nothing a site sends belongs there.
	linkLocal = &net.IPNet{IP: net.IPv4(169, 254, 0, 0).To4(), Mask: net.CIDRMask(16, 32)}
	// otherMetadata are metadata endpoints outside it: Alibaba Cloud's and
	// AWS's IPv6 one (a private ULA address).
	otherMetadata = []net.IP{net.IPv4(100, 100, 100, 200), net.ParseIP("fd00:ec2::254"), net.ParseIP("fd00:ec2::23")}
)

// Metadata reports whether host is a cloud metadata or credentials
// endpoint, by address (all of 169.254.0.0/16, 100.100.100.200,
// fd00:ec2::254) or by one of the clouds' names for it. A sealed site must
// never read cloud credentials, so while sealed such a host is faked
// whatever the allowlist says. Any other name can resolve there too: the
// proxy's dialer catches those.
func Metadata(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if metadataNames[host] {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	if linkLocal.Contains(ip) {
		return true
	}
	for _, m := range otherMetadata {
		if ip.Equal(m) {
			return true
		}
	}
	return false
}
