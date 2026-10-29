//go:build !linux

package srv

import (
	"context"
	"time"

	"github.com/Marty-G1tHub/tracewing/internal/model"
)

func SampleResources(ctx context.Context, d time.Duration) model.Resources {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
	return model.Resources{Note: "服务端不是 Linux，无法读取 CPU steal / 内存"}
}
