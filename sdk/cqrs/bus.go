package cqrs

import (
	"context"
	"fmt"

	"github.com/dudangphuoc/AegisKernel/sdk/multitenant"
)

type HandlerFunc[Req any, Res any] func(ctx context.Context, req Req) (Res, error)

type featureOverride[Req any, Res any] struct {
	FeatureFlag string
	Handler     HandlerFunc[Req, Res]
}

// FeatureDispatcher gắn kết luồng Base Business và Feature Overrides theo Tenant
type FeatureDispatcher[Req any, Res any] struct {
	name        string
	baseHandler HandlerFunc[Req, Res]
	overrides   []featureOverride[Req, Res]
}

func NewFeatureDispatcher[Req any, Res any](name string, baseHandler HandlerFunc[Req, Res]) *FeatureDispatcher[Req, Res] {
	return &FeatureDispatcher[Req, Res]{
		name:        name,
		baseHandler: baseHandler,
		overrides:   make([]featureOverride[Req, Res], 0),
	}
}

// RegisterOverride đăng ký một Feature Handler để ghi đè Base Business khi FeatureFlag bật
func (d *FeatureDispatcher[Req, Res]) RegisterOverride(featureFlag string, handler HandlerFunc[Req, Res]) *FeatureDispatcher[Req, Res] {
	d.overrides = append(d.overrides, featureOverride[Req, Res]{
		FeatureFlag: featureFlag,
		Handler:     handler,
	})
	return d
}

// Dispatch thực thi luồng phù hợp dựa vào Tenant Context
func (d *FeatureDispatcher[Req, Res]) Dispatch(ctx context.Context, req Req) (Res, error) {
	// Kiểm tra xem Tenant có bật cờ Feature Override nào không
	for _, ov := range d.overrides {
		if multitenant.IsFeatureEnabled(ctx, ov.FeatureFlag) {
			return ov.Handler(ctx, req)
		}
	}

	if d.baseHandler == nil {
		var zero Res
		return zero, fmt.Errorf("dispatcher '%s': no handler configured", d.name)
	}

	return d.baseHandler(ctx, req)
}

