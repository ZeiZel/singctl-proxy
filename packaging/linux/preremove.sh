#!/bin/sh
# Runs before the package is removed. Stop any running per-user daemon instances
# (best-effort); the user's ~/.config/singctl profile is left untouched.
set -e

if command -v systemctl >/dev/null 2>&1; then
	# Disable+stop every enabled singctld@<user> instance.
	for unit in $(systemctl list-units --all --no-legend 'singctld@*' 2>/dev/null | awk '{print $1}'); do
		systemctl disable --now "$unit" || true
	done
fi

exit 0
