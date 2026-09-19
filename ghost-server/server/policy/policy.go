// Package policy is ghost-server's policy engine. Each network has one policy
// document holding tag definitions, ACL rules and exit policies. The engine
// answers three questions for the netmap builder:
//
//   - Which peers may a peer reach, or be reached by? (Visible)
//   - Which inbound traffic must a peer admit? (Filter)
//   - Which exit policy applies to a peer? (ExitPolicy)
//
// See docs/policy.md for the format.
package policy

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

// Selector prefixes.
const (
	// Any matches every peer.
	Any = "*"
	// TagPrefix selects peers carrying a tag: "tag:exit".
	TagPrefix = "tag:"
	// RolePrefix selects peers holding a role: "role:hub".
	RolePrefix = "role:"
	// PeerPrefix selects one peer by id: "peer:peer_abc".
	PeerPrefix = "peer:"
)

// Isolation limits which peers of a network may see each other, on top of
// the ACLs.
type Isolation string

const (
	// IsolationNone applies the ACLs alone (any topology, including a mesh).
	IsolationNone Isolation = "none"
	// IsolationHubOnly lets a pair connect only if at least one side holds the
	// hub role: peers without the hub role never see or reach each other,
	// whatever the ACLs say.
	IsolationHubOnly Isolation = "hub-only"
)

// Valid reports whether i is a known isolation mode.
func (i Isolation) Valid() bool { return i == IsolationNone || i == IsolationHubOnly }

// Permits reports whether isolation lets a and b be connected at all.
func (i Isolation) Permits(a, b Subject) bool {
	if i != IsolationHubOnly {
		return true
	}
	return proto.HasRole(a.Roles, proto.RoleHub) || proto.HasRole(b.Roles, proto.RoleHub)
}

// ActionAccept is the only ACL action: rules only grant access; anything not
// granted is denied.
const ActionAccept = "accept"

// TagDef describes a tag. Peers may only carry tags the document defines.
type TagDef struct {
	Description string `json:"description,omitempty"`
}

// ACLRule lets peers matching Src open connections to peers matching Dst on
// Ports (every port when empty).
type ACLRule struct {
	Action string   `json:"action"`
	Src    []string `json:"src"`
	Dst    []string `json:"dst"`
	Ports  []int    `json:"ports,omitempty"`
}

// ExitRule attaches an exit policy to the peers matching Target. When several
// rules match a peer, the most specific wins: a "peer:" selector, then a
// "tag:" or "role:" selector (first in document order), then "*".
type ExitRule struct {
	Name           string            `json:"name"`
	Target         []string          `json:"target"`
	Allow          []string          `json:"allow"`
	DailyBytes     int64             `json:"daily_bytes,omitempty"`
	BytesPerSecond int64             `json:"bytes_per_second,omitempty"`
	Paused         bool              `json:"paused,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
}

// Document is a network's policy.
type Document struct {
	Tags map[string]TagDef `json:"tags"`
	ACLs []ACLRule         `json:"acls"`
	Exit []ExitRule        `json:"exit"`
}

// Default is the policy of a new network: hubs may reach every peer (so every
// peer can reach its hubs back over the same links), and no exit policy
// (exits deny everything).
func Default() Document {
	return Document{
		Tags: map[string]TagDef{},
		ACLs: []ACLRule{{Action: ActionAccept, Src: []string{RolePrefix + string(proto.RoleHub)}, Dst: []string{Any}}},
		Exit: []ExitRule{},
	}
}

// Normalize fills nil collections so the document serialises predictably.
func (d Document) Normalize() Document {
	if d.Tags == nil {
		d.Tags = map[string]TagDef{}
	}
	if d.ACLs == nil {
		d.ACLs = []ACLRule{}
	}
	if d.Exit == nil {
		d.Exit = []ExitRule{}
	}
	for i := range d.Exit {
		if d.Exit[i].Allow == nil {
			d.Exit[i].Allow = []string{}
		}
	}
	return d
}

var (
	tagNameRE  = regexp.MustCompile(`^tag:[a-z0-9][a-z0-9._-]{0,62}$`)
	exitNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)
)

// ValidTag reports whether s is a well-formed tag name ("tag:...").
func ValidTag(s string) bool { return tagNameRE.MatchString(s) }

// ValidExitName reports whether s is a well-formed exit rule name.
func ValidExitName(s string) bool { return exitNameRE.MatchString(s) }

// ErrInvalid wraps every validation failure.
var ErrInvalid = errors.New("invalid policy")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Validate checks the document: tag names, selectors (tags must be defined),
// ports, exit rule names, and that no exit allowlist opens every host.
func (d Document) Validate() error {
	var errs []error
	for name := range d.Tags {
		if !ValidTag(name) {
			errs = append(errs, invalid("tag %q must match %s", name, tagNameRE))
		}
	}
	checkSel := func(where, sel string) {
		if err := d.validSelector(sel); err != nil {
			errs = append(errs, invalid("%s: %v", where, err))
		}
	}
	for i, r := range d.ACLs {
		where := fmt.Sprintf("acls[%d]", i)
		if r.Action != ActionAccept {
			errs = append(errs, invalid("%s: action must be %q", where, ActionAccept))
		}
		if len(r.Src) == 0 || len(r.Dst) == 0 {
			errs = append(errs, invalid("%s: src and dst must not be empty", where))
		}
		for _, s := range r.Src {
			checkSel(where+".src", s)
		}
		for _, s := range r.Dst {
			checkSel(where+".dst", s)
		}
		for _, p := range r.Ports {
			if p < 1 || p > 65535 {
				errs = append(errs, invalid("%s: port %d out of range", where, p))
			}
		}
	}
	names := map[string]bool{}
	for i, e := range d.Exit {
		where := fmt.Sprintf("exit[%d]", i)
		if !ValidExitName(e.Name) {
			errs = append(errs, invalid("%s: name %q must match %s", where, e.Name, exitNameRE))
		}
		if names[e.Name] {
			errs = append(errs, invalid("%s: duplicate name %q", where, e.Name))
		}
		names[e.Name] = true
		if len(e.Target) == 0 {
			errs = append(errs, invalid("%s: target must not be empty", where))
		}
		for _, s := range e.Target {
			checkSel(where+".target", s)
		}
		if err := ValidateAllowlist(e.Allow); err != nil {
			errs = append(errs, invalid("%s: %v", where, err))
		}
		if e.DailyBytes < 0 || e.BytesPerSecond < 0 {
			errs = append(errs, invalid("%s: caps must not be negative", where))
		}
	}
	return errors.Join(errs...)
}

// ValidateAllowlist rejects entries that would turn an exit into an open
// proxy ("*", "*:port", empty entries).
func ValidateAllowlist(allow []string) error {
	for _, a := range allow {
		a = strings.TrimSpace(a)
		host, _, _ := strings.Cut(a, ":")
		if a == "" || host == "*" || host == "" {
			return fmt.Errorf("allow entry %q would open the exit to every host", a)
		}
	}
	return nil
}

func (d Document) validSelector(sel string) error {
	switch {
	case sel == Any:
		return nil
	case strings.HasPrefix(sel, TagPrefix):
		if _, ok := d.Tags[sel]; !ok {
			return fmt.Errorf("tag %q is not defined", sel)
		}
		return nil
	case strings.HasPrefix(sel, RolePrefix):
		if !proto.Role(strings.TrimPrefix(sel, RolePrefix)).Valid() {
			return fmt.Errorf("unknown role in %q", sel)
		}
		return nil
	case strings.HasPrefix(sel, PeerPrefix):
		if len(sel) == len(PeerPrefix) {
			return fmt.Errorf("empty peer id in %q", sel)
		}
		return nil
	}
	return fmt.Errorf("selector %q must be *, tag:<name>, role:<role> or peer:<id>", sel)
}

// UndefinedTags returns the entries of tags the document does not define.
func (d Document) UndefinedTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if _, ok := d.Tags[t]; !ok {
			out = append(out, t)
		}
	}
	return out
}

// Subject is a peer as the policy engine sees it.
type Subject struct {
	ID      string
	Roles   []proto.Role
	Tags    []string
	Address string
}

// Matches reports whether the selector selects s.
func (s Subject) Matches(sel string) bool {
	switch {
	case sel == Any:
		return true
	case strings.HasPrefix(sel, TagPrefix):
		return slices.Contains(s.Tags, sel)
	case strings.HasPrefix(sel, RolePrefix):
		return proto.HasRole(s.Roles, proto.Role(strings.TrimPrefix(sel, RolePrefix)))
	case strings.HasPrefix(sel, PeerPrefix):
		return s.ID == strings.TrimPrefix(sel, PeerPrefix)
	}
	return false
}

func matchesAny(s Subject, sels []string) bool {
	for _, sel := range sels {
		if s.Matches(sel) {
			return true
		}
	}
	return false
}

// Allows reports whether some rule lets src open connections to dst under
// the isolation mode.
func (d Document) Allows(iso Isolation, src, dst Subject) bool {
	if src.ID == dst.ID || !iso.Permits(src, dst) {
		return false
	}
	for _, r := range d.ACLs {
		if matchesAny(src, r.Src) && matchesAny(dst, r.Dst) {
			return true
		}
	}
	return false
}

// Visible reports whether a and b need a tunnel between them: some rule lets
// one reach the other, and the isolation mode permits the pair.
func (d Document) Visible(iso Isolation, a, b Subject) bool {
	return d.Allows(iso, a, b) || d.Allows(iso, b, a)
}

// Filter returns self's inbound packet filter: for every rule whose Dst
// matches self, the addresses of the other subjects matching its Src that the
// isolation mode permits, on the rule's ports. Rules with no matching source
// are omitted.
func (d Document) Filter(iso Isolation, self Subject, others []Subject) proto.PacketFilter {
	f := proto.PacketFilter{Rules: []proto.FilterRule{}}
	for _, r := range d.ACLs {
		if !matchesAny(self, r.Dst) {
			continue
		}
		var src []string
		for _, o := range others {
			if o.ID != self.ID && o.Address != "" && matchesAny(o, r.Src) && iso.Permits(o, self) {
				src = append(src, o.Address)
			}
		}
		if len(src) == 0 {
			continue
		}
		slices.Sort(src)
		f.Rules = append(f.Rules, proto.FilterRule{Src: src, Ports: slices.Clone(r.Ports)})
	}
	return f
}

// ExitRuleFor returns the exit rule that applies to self and whether one does.
func (d Document) ExitRuleFor(self Subject) (ExitRule, bool) {
	best, bestRank := ExitRule{}, -1
	for _, e := range d.Exit {
		rank := -1
		for _, sel := range e.Target {
			if !self.Matches(sel) {
				continue
			}
			r := 0
			switch {
			case strings.HasPrefix(sel, PeerPrefix):
				r = 2
			case sel != Any:
				r = 1
			}
			rank = max(rank, r)
		}
		if rank > bestRank {
			best, bestRank = e, rank
		}
	}
	return best, bestRank >= 0
}

// ExitPolicy converts the exit rule for self into the wire form. With no
// matching rule, the result denies everything.
func (d Document) ExitPolicy(network string, revision int64, self Subject) proto.ExitPolicy {
	p := proto.ExitPolicy{Network: network, Allow: []string{}, Revision: revision}
	if e, ok := d.ExitRuleFor(self); ok {
		p.Allow = slices.Clone(e.Allow)
		p.DailyBytes = e.DailyBytes
		p.BytesPerSecond = e.BytesPerSecond
		p.Paused = e.Paused
		p.Labels = e.Labels
	}
	if p.Allow == nil {
		p.Allow = []string{}
	}
	return p
}
