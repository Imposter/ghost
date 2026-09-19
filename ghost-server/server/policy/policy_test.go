package policy

import (
	"slices"
	"testing"

	"github.com/Imposter/ghost/ghost-go/signal/proto"
)

func subj(id, addr string, roles ...proto.Role) Subject {
	return Subject{ID: id, Address: addr, Roles: roles}
}

var (
	hub   = subj("hub", "100.64.0.1/32", proto.RoleHub)
	exitA = Subject{ID: "exitA", Address: "100.64.0.2/32", Roles: []proto.Role{proto.RoleNode, proto.RoleExit}, Tags: []string{"tag:exit"}}
	exitB = Subject{ID: "exitB", Address: "100.64.0.3/32", Roles: []proto.Role{proto.RoleNode, proto.RoleExit}, Tags: []string{"tag:exit"}}
	nodeA = subj("nodeA", "100.64.0.4/32", proto.RoleNode)
	nodeB = subj("nodeB", "100.64.0.5/32", proto.RoleNode)
)

// TestDefaultACL: hub -> exit allowed; node -> node and exit -> exit denied.
func TestDefaultACL(t *testing.T) {
	d := Default()
	d.Tags["tag:exit"] = TagDef{}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, iso := range []Isolation{IsolationNone, IsolationHubOnly} {
		if !d.Allows(iso, hub, exitA) || !d.Visible(iso, exitA, hub) {
			t.Fatalf("%s: hub -> exit must be allowed", iso)
		}
		if d.Allows(iso, exitA, hub) {
			t.Fatalf("%s: no rule lets an exit open connections to the hub", iso)
		}
		if d.Visible(iso, nodeA, nodeB) || d.Visible(iso, exitA, exitB) {
			t.Fatalf("%s: node -> node and exit -> exit must be denied", iso)
		}
	}
}

// TestHubOnlyOverridesACL: a mesh rule works under "none" but hub-only still
// keeps non-hub peers apart.
func TestHubOnlyOverridesACL(t *testing.T) {
	d := Default()
	d.ACLs = append(d.ACLs, ACLRule{Action: ActionAccept, Src: []string{Any}, Dst: []string{Any}})
	if !d.Visible(IsolationNone, nodeA, nodeB) {
		t.Fatal("mesh rule: node <-> node must be visible under isolation none")
	}
	if d.Visible(IsolationHubOnly, nodeA, nodeB) || d.Visible(IsolationHubOnly, exitA, exitB) {
		t.Fatal("hub-only must keep non-hub peers apart whatever the ACLs say")
	}
	if !d.Visible(IsolationHubOnly, nodeA, hub) {
		t.Fatal("hub-only must keep hubs reachable")
	}
	// The filter of a non-hub peer under hub-only lists only hubs.
	f := d.Filter(IsolationHubOnly, exitA, []Subject{hub, exitB, nodeA})
	for _, r := range f.Rules {
		if !slices.Equal(r.Src, []string{hub.Address}) {
			t.Fatalf("hub-only filter admits %v", r.Src)
		}
	}
}

func TestPortsAndFilter(t *testing.T) {
	d := Default()
	d.Tags["tag:exit"] = TagDef{}
	d.ACLs = []ACLRule{{Action: ActionAccept, Src: []string{"role:hub"}, Dst: []string{"tag:exit"}, Ports: []int{1080, 9464}}}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	f := d.Filter(IsolationNone, exitA, []Subject{hub, nodeA})
	if len(f.Rules) != 1 || !slices.Equal(f.Rules[0].Src, []string{hub.Address}) || !slices.Equal(f.Rules[0].Ports, []int{1080, 9464}) {
		t.Fatalf("filter: %+v", f)
	}
	if f := d.Filter(IsolationNone, nodeA, []Subject{hub}); len(f.Rules) != 0 {
		t.Fatalf("an untagged node admits nothing: %+v", f)
	}
}

func TestExitPolicySpecificity(t *testing.T) {
	d := Default()
	d.Tags["tag:exit"] = TagDef{}
	d.Exit = []ExitRule{
		{Name: "all", Target: []string{Any}, Allow: []string{"a.example:443"}},
		{Name: "exits", Target: []string{"tag:exit"}, Allow: []string{"b.example:443"}, DailyBytes: 100},
		{Name: "one", Target: []string{"peer:exitB"}, Allow: []string{"c.example:443"}, Paused: true},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if p := d.ExitPolicy("n", 3, exitA); !slices.Equal(p.Allow, []string{"b.example:443"}) || p.DailyBytes != 100 || p.Revision != 3 {
		t.Fatalf("tag rule: %+v", p)
	}
	if p := d.ExitPolicy("n", 3, exitB); !slices.Equal(p.Allow, []string{"c.example:443"}) || !p.Paused {
		t.Fatalf("peer rule: %+v", p)
	}
	if p := d.ExitPolicy("n", 3, nodeA); !slices.Equal(p.Allow, []string{"a.example:443"}) {
		t.Fatalf("catch-all rule: %+v", p)
	}
	if p := Default().ExitPolicy("n", 1, exitA); p.Allow == nil || len(p.Allow) != 0 {
		t.Fatalf("no rule must deny everything: %+v", p)
	}
}

func TestValidateRejects(t *testing.T) {
	cases := map[string]Document{
		"open proxy":      {Exit: []ExitRule{{Name: "x", Target: []string{Any}, Allow: []string{"*:443"}}}},
		"undefined tag":   {ACLs: []ACLRule{{Action: ActionAccept, Src: []string{"tag:nope"}, Dst: []string{Any}}}},
		"bad selector":    {ACLs: []ACLRule{{Action: ActionAccept, Src: []string{"everyone"}, Dst: []string{Any}}}},
		"bad port":        {ACLs: []ACLRule{{Action: ActionAccept, Src: []string{Any}, Dst: []string{Any}, Ports: []int{0}}}},
		"deny action":     {ACLs: []ACLRule{{Action: "deny", Src: []string{Any}, Dst: []string{Any}}}},
		"negative cap":    {Exit: []ExitRule{{Name: "x", Target: []string{Any}, Allow: []string{"a:1"}, DailyBytes: -1}}},
		"duplicate names": {Exit: []ExitRule{{Name: "x", Target: []string{Any}}, {Name: "x", Target: []string{Any}}}},
	}
	for name, d := range cases {
		if err := d.Normalize().Validate(); err == nil {
			t.Errorf("%s: want a validation error", name)
		}
	}
}
