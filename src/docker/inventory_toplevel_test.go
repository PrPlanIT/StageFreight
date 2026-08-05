package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// dungeonShapedInventory mirrors the real-world YAML inventory shape: hosts
// declared once under all:, groups as TOP-LEVEL sibling keys with null-valued
// host references, and a parent group composing children.
const dungeonShapedInventory = `all:
  hosts:
    dungeon_map_001:
      ansible_host: 172.22.144.150
    dungeon_worker_001:
      ansible_host: 172.22.144.170
    lonely-box:
      ansible_host: 10.0.0.9
k8s_master:
  hosts:
    dungeon_map_001:
k8s_worker:
  hosts:
    dungeon_worker_001:
ubuntu:
  children:
    k8s_master:
    k8s_worker:
  vars:
    ansible_user: kai
`

// TestAnsibleInventory_TopLevelGroups pins the Ansible YAML inventory contract:
// every top-level key is a group — not only all: — and null-valued host entries
// reference hosts declared elsewhere without redefining them.
func TestAnsibleInventory_TopLevelGroups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "inventory")
	if err := os.WriteFile(path, []byte(dungeonShapedInventory), 0o644); err != nil {
		t.Fatal(err)
	}
	inv := &AnsibleInventory{Path: path}

	hosts, err := inv.Resolve(context.Background(), TargetSelector{Groups: []string{"k8s_worker", "k8s_master"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(hosts) != 2 {
		t.Fatalf("hosts = %d, want 2: %+v", len(hosts), hosts)
	}
	byName := map[string]HostTarget{}
	for _, h := range hosts {
		byName[h.Name] = h
	}
	if h, ok := byName["dungeon_map_001"]; !ok || h.Address != "172.22.144.150" {
		t.Errorf("dungeon_map_001: %+v", h)
	}
	if h, ok := byName["dungeon_worker_001"]; !ok || h.Address != "172.22.144.170" {
		t.Errorf("dungeon_worker_001: %+v", h)
	}
	if _, ok := byName["lonely-box"]; ok {
		t.Error("lonely-box is in no selected group and must not resolve")
	}

	// A parent group composed of children resolves through them.
	viaParent, err := inv.Resolve(context.Background(), TargetSelector{Groups: []string{"ubuntu"}})
	if err != nil {
		t.Fatalf("resolve parent: %v", err)
	}
	if len(viaParent) != 2 {
		t.Errorf("ubuntu children resolution = %d hosts, want 2", len(viaParent))
	}
}
