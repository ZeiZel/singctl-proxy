package app

import "errors"

var (
	errNoProxy         = errors.New("no link loaded; load a vless:// link first")
	errProxyNotRunning = errors.New("start PROXY or VPN mode first, then route a process")
)
