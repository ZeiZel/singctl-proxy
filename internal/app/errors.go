package app

import "errors"

var errNoProxy = errors.New("no link loaded; load a vless:// link first")
