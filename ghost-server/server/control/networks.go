package control

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Imposter/ghost/ghost-server/server/events"
	"github.com/Imposter/ghost/ghost-server/server/ipam"
	"github.com/Imposter/ghost/ghost-server/server/policy"
	"github.com/Imposter/ghost/ghost-server/server/store"
)

var nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

// ValidNetworkName reports whether s is a valid network name.
func ValidNetworkName(s string) bool { return nameRE.MatchString(s) }

// NetworkInput creates a network.
type NetworkInput struct {
	Name string `json:"name"`
	// Pool defaults to the server's default pool.
	Pool string `json:"pool"`
	// Isolation defaults to "none".
	Isolation policy.Isolation `json:"isolation"`
	// InteractiveEnrollment allows interactive enrolment codes; it defaults to
	// true. Pre-auth keys work either way.
	InteractiveEnrollment *bool `json:"interactive_enrollment"`
}

// CreateNetwork creates a network with the default policy.
func (s *Service) CreateNetwork(ctx context.Context, in NetworkInput) (store.Network, error) {
	if !ValidNetworkName(in.Name) {
		return store.Network{}, invalidf("network name must match %s", nameRE)
	}
	if in.Pool == "" {
		in.Pool = s.defaultPool
	}
	if in.Isolation == "" {
		in.Isolation = policy.IsolationNone
	}
	if !in.Isolation.Valid() {
		return store.Network{}, invalidf("isolation must be %q or %q", policy.IsolationNone, policy.IsolationHubOnly)
	}
	p, err := ipam.ParsePool(in.Pool)
	if err != nil {
		return store.Network{}, invalidf("%v", err)
	}
	interactive := true
	if in.InteractiveEnrollment != nil {
		interactive = *in.InteractiveEnrollment
	}
	n := store.Network{
		Name: in.Name, Pool: p.String(), Isolation: in.Isolation, InteractiveEnrollment: interactive,
		Policy: policy.Default(), CreatedAt: s.now().UTC(),
	}
	if err := s.st.CreateNetwork(ctx, n); err != nil {
		return store.Network{}, mapStoreErr(err)
	}
	s.Audit(ctx, n.Name, "network.created", n.Name, map[string]any{
		"pool": n.Pool, "isolation": n.Isolation, "interactive_enrollment": n.InteractiveEnrollment,
	})
	s.bus.Publish(events.Event{Type: events.NetworkUpdated, Network: n.Name, Data: map[string]any{"created": true}})
	return n, nil
}

// EnsureNetwork creates a network if it does not exist; an existing network
// is left unchanged.
func (s *Service) EnsureNetwork(ctx context.Context, in NetworkInput) error {
	if _, err := s.st.GetNetwork(ctx, in.Name); err == nil {
		return nil
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	_, err := s.CreateNetwork(ctx, in)
	if errors.Is(err, ErrConflict) {
		return nil
	}
	return err
}

// Network returns a network.
func (s *Service) Network(ctx context.Context, name string) (store.Network, error) {
	n, err := s.st.GetNetwork(ctx, name)
	return n, mapStoreErr(err)
}

// ListNetworks returns every network.
func (s *Service) ListNetworks(ctx context.Context) ([]store.Network, error) {
	return s.st.ListNetworks(ctx)
}

// DeleteNetwork deletes a network with no peers (its keys and enrolments go
// with it).
func (s *Service) DeleteNetwork(ctx context.Context, name string) error {
	if err := s.st.DeleteNetwork(ctx, name); err != nil {
		return mapStoreErr(err)
	}
	s.Audit(ctx, name, "network.deleted", name, nil)
	s.bus.Publish(events.Event{Type: events.NetworkUpdated, Network: name, Data: map[string]any{"deleted": true}})
	return nil
}

// NetworkPatch changes a network's settings; nil fields are left unchanged.
type NetworkPatch struct {
	Isolation             *policy.Isolation `json:"isolation"`
	InteractiveEnrollment *bool             `json:"interactive_enrollment"`
}

// UpdateNetwork applies a patch to a network. Changing the isolation mode
// bumps the policy revision and pushes the resulting netmaps.
func (s *Service) UpdateNetwork(ctx context.Context, name string, patch NetworkPatch) (store.Network, error) {
	if patch.Isolation == nil && patch.InteractiveEnrollment == nil {
		return store.Network{}, invalidf("nothing to update: set isolation or interactive_enrollment")
	}
	if patch.Isolation != nil && !patch.Isolation.Valid() {
		return store.Network{}, invalidf("isolation must be %q or %q", policy.IsolationNone, policy.IsolationHubOnly)
	}
	n, err := s.st.UpdateNetwork(ctx, name, store.NetworkUpdate{
		Isolation: patch.Isolation, InteractiveEnrollment: patch.InteractiveEnrollment,
	})
	if err != nil {
		return n, mapStoreErr(err)
	}
	data := map[string]any{}
	if patch.Isolation != nil {
		data["isolation"] = n.Isolation
		s.Audit(ctx, name, "network.isolation", name, map[string]any{"isolation": n.Isolation, "revision": n.PolicyRevision})
	}
	if patch.InteractiveEnrollment != nil {
		data["interactive_enrollment"] = n.InteractiveEnrollment
		s.Audit(ctx, name, "network.interactive_enrollment", name, map[string]any{"enabled": n.InteractiveEnrollment})
	}
	s.bus.Publish(events.Event{Type: events.NetworkUpdated, Network: name, Data: data})
	if l := s.live(); l != nil && patch.Isolation != nil {
		l.PolicyChanged(ctx, name)
	}
	return n, nil
}

// SetPolicy replaces a network's policy document, then re-consults the
// authorizer for the network's live peers and pushes the new netmaps. A tag
// still carried by a peer cannot be removed.
func (s *Service) SetPolicy(ctx context.Context, network string, doc policy.Document) (store.Network, error) {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	return s.setPolicyLocked(ctx, network, doc)
}

func (s *Service) setPolicyLocked(ctx context.Context, network string, doc policy.Document) (store.Network, error) {
	doc = doc.Normalize()
	if err := doc.Validate(); err != nil {
		return store.Network{}, invalidf("%v", err)
	}
	peers, err := s.st.ListPeers(ctx, store.PeerFilter{Network: network})
	if err != nil {
		return store.Network{}, err
	}
	for _, p := range peers {
		if undefined := doc.UndefinedTags(p.Tags); len(undefined) > 0 {
			return store.Network{}, fmt.Errorf("%w: peer %s still carries %s", ErrConflict, p.ID, strings.Join(undefined, ", "))
		}
	}
	n, err := s.st.SetNetworkPolicy(ctx, network, doc)
	if err != nil {
		return n, mapStoreErr(err)
	}
	s.Audit(ctx, network, "policy.updated", network, map[string]any{
		"revision": n.PolicyRevision, "acls": len(doc.ACLs), "exit_rules": len(doc.Exit), "tags": len(doc.Tags),
	})
	s.bus.Publish(events.Event{Type: events.PolicyUpdated, Network: network, Data: map[string]any{"revision": n.PolicyRevision}})
	if l := s.live(); l != nil {
		l.PolicyChanged(ctx, network)
	}
	return n, nil
}

// editPolicy applies fn to the current document and stores the result.
func (s *Service) editPolicy(ctx context.Context, network string, fn func(*policy.Document) error) (store.Network, error) {
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	n, err := s.st.GetNetwork(ctx, network)
	if err != nil {
		return n, mapStoreErr(err)
	}
	doc := n.Policy.Normalize()
	if err := fn(&doc); err != nil {
		return n, err
	}
	return s.setPolicyLocked(ctx, network, doc)
}

// SetACLs replaces a network's ACL rules.
func (s *Service) SetACLs(ctx context.Context, network string, rules []policy.ACLRule) (store.Network, error) {
	return s.editPolicy(ctx, network, func(d *policy.Document) error {
		d.ACLs = rules
		return nil
	})
}

// PutTag defines (or redefines) a tag.
func (s *Service) PutTag(ctx context.Context, network, tag string, def policy.TagDef) (store.Network, error) {
	if !policy.ValidTag(tag) {
		return store.Network{}, invalidf("tag %q must look like tag:name", tag)
	}
	return s.editPolicy(ctx, network, func(d *policy.Document) error {
		d.Tags[tag] = def
		return nil
	})
}

// DeleteTag removes a tag definition. It fails while a peer carries the tag
// or a rule references it.
func (s *Service) DeleteTag(ctx context.Context, network, tag string) (store.Network, error) {
	return s.editPolicy(ctx, network, func(d *policy.Document) error {
		if _, ok := d.Tags[tag]; !ok {
			return ErrNotFound
		}
		delete(d.Tags, tag)
		return nil
	})
}

// PutExitRule adds or replaces the exit rule with rule.Name.
func (s *Service) PutExitRule(ctx context.Context, network string, rule policy.ExitRule) (store.Network, error) {
	return s.editPolicy(ctx, network, func(d *policy.Document) error {
		i := slices.IndexFunc(d.Exit, func(e policy.ExitRule) bool { return e.Name == rule.Name })
		if i >= 0 {
			d.Exit[i] = rule
		} else {
			d.Exit = append(d.Exit, rule)
		}
		return nil
	})
}

// DeleteExitRule removes an exit rule by name.
func (s *Service) DeleteExitRule(ctx context.Context, network, name string) (store.Network, error) {
	return s.editPolicy(ctx, network, func(d *policy.Document) error {
		i := slices.IndexFunc(d.Exit, func(e policy.ExitRule) bool { return e.Name == name })
		if i < 0 {
			return ErrNotFound
		}
		d.Exit = slices.Delete(d.Exit, i, i+1)
		return nil
	})
}
