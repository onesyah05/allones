package agent

import (
	"context"

	"github.com/allones/allones/internal/llm"
	"github.com/allones/allones/internal/subscription"
)

func (e *Engine) resolveClient(ctx context.Context, ref string) (llm.Client, string, error) {
	p, model, err := e.cfg.Get().FindProvider(ref)
	if err != nil {
		return nil, "", err
	}
	if p.OAuth {
		p, err = subscription.Ready(ctx, e.cfg, p.Name)
		if err != nil {
			return nil, "", err
		}
	}
	client, err := llm.New(p)
	return client, model, err
}
