#!/bin/sh
# When cjx detects a reachable host-side http(s)_proxy, it passes
# $CJX_UPSTREAM_PROXY=host:port. We then build a runtime config in the /run
# tmpfs that routes everything through that parent and skips Squid's own DNS
# (corporate networks usually block 1.1.1.1 / 8.8.8.8).
set -e

if [ -n "${CJX_UPSTREAM_PROXY:-}" ]; then
	host="${CJX_UPSTREAM_PROXY%:*}"
	port="${CJX_UPSTREAM_PROXY##*:}"
	grep -v '^dns_nameservers' /etc/squid/squid.conf > /run/squid.conf
	printf '\ncache_peer %s parent %s 0 no-query default\nnever_direct allow all\n' \
		"$host" "$port" >> /run/squid.conf
	exec squid -N -f /run/squid.conf
fi

exec squid -N -f /etc/squid/squid.conf
