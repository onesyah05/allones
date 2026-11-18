package subscription

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/allones/allones/internal/config"
)

// Serialize rotating refresh credentials across chats, tests and model listing.
var refreshMu sync.Mutex

func Ready(ctx context.Context, manager *config.Manager, name string) (*config.Provider, error) {
	refreshMu.Lock()
	defer refreshMu.Unlock()
	c := manager.Get()
	var p *config.Provider
	for i := range c.Provider {
		if c.Provider[i].Name == name {
			p = &c.Provider[i]
			break
		}
	}
	if p == nil {
		return nil, fmt.Errorf("provider %q is not connected", name)
	}
	if !p.OAuth || p.ExpiresAt == 0 || p.ExpiresAt > time.Now().Unix()+90 {
		return p, nil
	}
	fresh, err := Refresh(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("refresh %s: %w; sign in again in Providers", name, err)
	}
	err = manager.Update(func(cfg *config.Config) error {
		for i := range cfg.Provider {
			if cfg.Provider[i].Name == name {
				if cfg.Provider[i].APIKey != p.APIKey {
					return fmt.Errorf("provider changed during refresh; retry")
				}
				cfg.Provider[i].APIKey = fresh.APIKey
				cfg.Provider[i].RefreshToken = fresh.RefreshToken
				cfg.Provider[i].ExpiresAt = fresh.ExpiresAt
				cfg.Provider[i].Headers = fresh.Headers
				return nil
			}
		}
		return fmt.Errorf("provider disconnected during refresh")
	})
	return fresh, err
}
