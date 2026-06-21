//go:build !linux && !darwin

package proclist

import "context"

// NewLister returns an empty lister on platforms where process enumeration is
// not implemented.
func NewLister() Lister { return emptyLister{} }

type emptyLister struct{}

func (emptyLister) List(context.Context) ([]Process, error) { return nil, nil }
