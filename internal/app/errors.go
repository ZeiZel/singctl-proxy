package app

import "errors"

var (
	errNoProxy         = errors.New("no link loaded; load a share link first (vless://, ss://, trojan://, hysteria2://, …)")
	errProxyNotRunning = errors.New("start PROXY or VPN mode first, then route a process")
)
