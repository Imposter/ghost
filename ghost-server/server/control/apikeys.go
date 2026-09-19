package control

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Imposter/ghost/ghost-server/server/store"
)

// Scope is a control API permission.
type Scope string

const (
	ScopeNetworksRead  Scope = "networks:read"
	ScopeNetworksWrite Scope = "networks:write"
	ScopePeersRead     Scope = "peers:read"
	ScopePeersWrite    Scope = "peers:write"
	ScopePolicyRead    Scope = "policy:read"
	ScopePolicyWrite   Scope = "policy:write"
	ScopeKeysRead      Scope = "keys:read"
	ScopeKeysWrite     Scope = "keys:write"
	ScopeAuditRead     Scope = "audit:read"
	ScopeWatch         Scope = "watch"
	// ScopeAdmin manages API keys. Only the service token holds it
	// implicitly; it can also be granted to an API key.
	ScopeAdmin Scope = "admin"
)

// AllScopes lists every scope.
var AllScopes = []Scope{
	ScopeNetworksRead, ScopeNetworksWrite, ScopePeersRead, ScopePeersWrite, ScopePolicyRead,
	ScopePolicyWrite, ScopeKeysRead, ScopeKeysWrite, ScopeAuditRead, ScopeWatch, ScopeAdmin,
}

// Principal is an authenticated control API caller.
type Principal struct {
	// Actor is recorded in the audit log: "service" or "apikey:<id>".
	Actor  string
	Scopes []Scope
	// Networks limits the caller to these networks (nil: every network).
	Networks []string
}

// ServicePrincipal is the service token's principal: every scope, every
// network.
func ServicePrincipal() Principal {
	return Principal{Actor: "service", Scopes: slices.Clone(AllScopes)}
}

// Has reports whether the principal holds scope.
func (p Principal) Has(scope Scope) bool { return slices.Contains(p.Scopes, scope) }

// CanAccess reports whether the principal may act on network.
func (p Principal) CanAccess(network string) bool {
	return p.Networks == nil || slices.Contains(p.Networks, network)
}

// APIKeyInput creates an API key.
type APIKeyInput struct {
	Name     string   `json:"name"`
	Scopes   []Scope  `json:"scopes"`
	Networks []string `json:"networks"`
	// ExpiresIn, when positive, expires the key after this long.
	ExpiresIn time.Duration `json:"-"`
}

// IssuedAPIKey is a newly created API key. The plaintext key is only
// available here.
type IssuedAPIKey struct {
	Key    string       `json:"key"`
	APIKey store.APIKey `json:"-"`
}

// CreateAPIKey issues a scoped API key.
func (s *Service) CreateAPIKey(ctx context.Context, in APIKeyInput) (IssuedAPIKey, error) {
	if len(in.Scopes) == 0 {
		return IssuedAPIKey{}, invalidf("at least one scope is required")
	}
	scopes := make([]string, 0, len(in.Scopes))
	for _, sc := range in.Scopes {
		if !slices.Contains(AllScopes, sc) {
			return IssuedAPIKey{}, invalidf("unknown scope %q", sc)
		}
		scopes = append(scopes, string(sc))
	}
	for _, n := range in.Networks {
		if _, err := s.Network(ctx, n); err != nil {
			return IssuedAPIKey{}, invalidf("network %q: %v", n, err)
		}
	}
	now := s.now().UTC()
	key := newSecret(APIKeyPrefix)
	k := store.APIKey{ID: newID("apikey_"), KeyHash: HashSecret(key), Name: in.Name, Scopes: scopes,
		Networks: in.Networks, CreatedAt: now}
	if in.ExpiresIn > 0 {
		exp := now.Add(in.ExpiresIn)
		k.ExpiresAt = &exp
	}
	if err := s.st.CreateAPIKey(ctx, k); err != nil {
		return IssuedAPIKey{}, mapStoreErr(err)
	}
	s.Audit(ctx, "", "api_key.created", k.ID, map[string]any{"name": k.Name, "scopes": k.Scopes, "networks": k.Networks})
	return IssuedAPIKey{Key: key, APIKey: k}, nil
}

// ListAPIKeys returns every API key (hashes only).
func (s *Service) ListAPIKeys(ctx context.Context) ([]store.APIKey, error) {
	return s.st.ListAPIKeys(ctx)
}

// RevokeAPIKey revokes an API key.
func (s *Service) RevokeAPIKey(ctx context.Context, id string) (store.APIKey, error) {
	k, err := s.st.RevokeAPIKey(ctx, id, s.now().UTC())
	if err != nil {
		return k, mapStoreErr(err)
	}
	s.Audit(ctx, "", "api_key.revoked", id, nil)
	return k, nil
}

// AuthenticateAPIKey resolves an API key to a principal.
func (s *Service) AuthenticateAPIKey(ctx context.Context, key string) (Principal, error) {
	k, err := s.st.GetAPIKeyByHash(ctx, HashSecret(key))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Principal{}, ErrUnauthorized
		}
		return Principal{}, err
	}
	now := s.now()
	if k.RevokedAt != nil || (k.ExpiresAt != nil && !now.Before(*k.ExpiresAt)) {
		return Principal{}, ErrUnauthorized
	}
	if k.LastUsedAt == nil || now.Sub(*k.LastUsedAt) > time.Minute {
		_ = s.st.TouchAPIKey(ctx, k.ID, now.UTC())
	}
	p := Principal{Actor: "apikey:" + k.ID}
	for _, sc := range k.Scopes {
		p.Scopes = append(p.Scopes, Scope(sc))
	}
	if len(k.Networks) > 0 {
		p.Networks = slices.Clone(k.Networks)
	}
	return p, nil
}
