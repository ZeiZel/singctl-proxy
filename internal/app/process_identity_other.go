//go:build !darwin

package app

import "strconv"

func processIdentity(pid int) (string, bool) { return strconv.Itoa(pid), pid > 0 }
