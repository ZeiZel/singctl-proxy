//go:build singbox

// This file is the ONLY place the real sing-box library is imported. It is
// excluded from the default build and the hermetic unit suite; build the
// shipping binary with `-tags singbox` (after `go get github.com/sagernet/
// sing-box@v1.12.x`). It is verified at integration time (PLAN §8), not by the
// unit suite.
package core

import (
	"context"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"
)

type boxCore struct {
	instance *box.Box
}

// newBoxCore parses a sing-box JSON config and constructs a (not-yet-started)
// instance. The 6-arg box.Context registers all protocol registries exactly
// once; box.Options embeds option.Options.
func newBoxCore(ctx context.Context, label string, configJSON []byte) (Core, error) {
	bctx := box.Context(ctx,
		include.InboundRegistry(),
		include.OutboundRegistry(),
		include.EndpointRegistry(),
		include.DNSTransportRegistry(),
		include.ServiceRegistry(),
	)
	options, err := json.UnmarshalExtendedContext[option.Options](bctx, configJSON)
	if err != nil {
		return nil, err
	}
	instance, err := box.New(box.Options{Context: bctx, Options: options})
	if err != nil {
		return nil, err
	}
	return &boxCore{instance: instance}, nil
}

func (c *boxCore) Start(ctx context.Context) error { return c.instance.Start() }

func (c *boxCore) Close() error { return c.instance.Close() }

// NewFactory returns the real sing-box-backed Factory.
func NewFactory() Factory { return newBoxCore }
