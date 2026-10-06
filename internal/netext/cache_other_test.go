//go:build !darwin

package netext

import "testing"

func isolateController(t *testing.T, c Controller) {}
