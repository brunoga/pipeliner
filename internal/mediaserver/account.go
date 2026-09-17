// Plex account mode: one Sign-in-with-Plex token (Settings tab) shared by
// every Plex integration — the library filter, the library_refresh sink, and
// the Tools reconcile — instead of per-plugin url/token config. The account
// client discovers the account's servers via plex.tv and fans out to all
// owned ones.
package mediaserver

import (
	"context"
	"fmt"
)

// Where the Sign-in-with-Plex account token is stored. The bucket predates
// the account mode (the reconcile tool introduced it), so existing sign-ins
// keep working.
const (
	PlexSettingsBucket = "tools_settings"
	PlexTokenKey       = "plex_token"
)

// tokenBucket is the minimal store interface needed to read the token;
// store.Bucket satisfies it.
type tokenBucket interface {
	Get(key string, dest any) (bool, error)
}

// PlexAccountToken reads the stored account token; "" when not signed in.
func PlexAccountToken(b tokenBucket) string {
	var tok string
	if found, _ := b.Get(PlexTokenKey, &tok); found {
		return tok
	}
	return ""
}

// NewPlexAccount returns a Client spanning every owned server of the Plex
// account. tokenFn is consulted on each operation so a sign-in performed
// after daemon start takes effect without a restart.
func NewPlexAccount(tokenFn func() string) Client {
	return &plexAccountClient{tokenFn: tokenFn}
}

type plexAccountClient struct {
	tokenFn func() string

	// deep scanning forwarded to every per-server client, cache-scoped by the
	// stable server name (connection bases flip between direct and relay).
	deep       bool
	rangeCache RangeCache
}

func (c *plexAccountClient) enableDeepScan(cache RangeCache, _ string) {
	c.deep = true
	c.rangeCache = cache
}

// forEachOwned discovers the account's owned servers, connects to each, and
// invokes fn with a per-server client.
func (c *plexAccountClient) forEachOwned(ctx context.Context, fn func(name string, cl Client) error) error {
	token := c.tokenFn()
	if token == "" {
		return fmt.Errorf("plex account: not signed in — use Settings → Plex Account")
	}
	servers, err := DiscoverPlexServers(ctx, token)
	if err != nil {
		return err
	}
	owned := 0
	for _, srv := range servers {
		if !srv.Owned {
			continue
		}
		owned++
		base, err := srv.Connect(ctx)
		if err != nil {
			return err
		}
		cl, err := New("plex", base, srv.Token)
		if err != nil {
			return err
		}
		if c.deep {
			if pc, ok := cl.(*plexClient); ok {
				pc.enableDeepScan(c.rangeCache, srv.Name)
			}
		}
		if err := fn(srv.Name, cl); err != nil {
			return fmt.Errorf("plex server %q: %w", srv.Name, err)
		}
	}
	if owned == 0 {
		return fmt.Errorf("plex account: no owned servers found")
	}
	return nil
}

// ListItems aggregates the libraries of every owned server. Any unreachable
// owned server fails the whole listing — a partial library would make the
// library filter treat that server's entire content as missing.
func (c *plexAccountClient) ListItems(ctx context.Context) ([]Item, error) {
	var items []Item
	err := c.forEachOwned(ctx, func(_ string, cl Client) error {
		got, err := cl.ListItems(ctx)
		if err != nil {
			return err
		}
		items = append(items, got...)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return items, nil
}

// Refresh asks every owned server to rescan its libraries.
func (c *plexAccountClient) Refresh(ctx context.Context) error {
	return c.forEachOwned(ctx, func(_ string, cl Client) error {
		return cl.Refresh(ctx)
	})
}
