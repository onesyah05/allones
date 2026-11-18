package llm

import (
	"context"
	"fmt"

	"github.com/allones/allones/internal/config"
)

func SubscriptionModels(ctx context.Context, p *config.Provider) ([]string, error) {
	client, err := New(p)
	if err != nil {
		return nil, err
	}
	if lister, ok := client.(interface {
		listModels(context.Context) ([]string, error)
	}); ok {
		return lister.listModels(ctx)
	}
	return nil, fmt.Errorf("provider %s does not support subscription model discovery", p.Name)
}
