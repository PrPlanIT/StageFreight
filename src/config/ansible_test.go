package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const ansibleFixture = `
ansible:
  image: docker.io/hlhd/ansible:v2.20.4
  inventory: ansible/inventory
  ssh:
    user: automation
    credentials: DUNGEON_NODES
    known_hosts: ansible/known_hosts
  playbooks:
    provision-hosts:
      path: ansible/k8s/provision-hosts.yml
      groups: [k8s_worker, k8s_master]
      converge: true
    postgres-major-upgrade:
      path: ansible/k8s/postgres-major-upgrade.yml
      groups: [k8s_master]
      converge: false
`

func decodeAnsibleFixture(t *testing.T, doc string) Config {
	t.Helper()
	cfg := *defaults()
	dec := yaml.NewDecoder(strings.NewReader(doc))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	return cfg
}

// TestAnsibleConfig_RoundTrip pins the library model: strict decode, document
// order, id stamping from the map key, and the converge/runbook split.
func TestAnsibleConfig_RoundTrip(t *testing.T) {
	cfg := decodeAnsibleFixture(t, ansibleFixture)
	a := cfg.Ansible

	if a.Image != "docker.io/hlhd/ansible:v2.20.4" || a.Inventory != "ansible/inventory" {
		t.Errorf("image/inventory: %q %q", a.Image, a.Inventory)
	}
	if a.SSH.EnvPrefix() != "DUNGEON_NODES" {
		t.Errorf("env prefix: %q", a.SSH.EnvPrefix())
	}
	if len(a.Playbooks) != 2 || a.Playbooks[0].ID != "provision-hosts" || a.Playbooks[1].ID != "postgres-major-upgrade" {
		t.Fatalf("playbook order/ids: %+v", a.Playbooks)
	}
	if !a.HasConvergePlaybooks() {
		t.Error("converge presence gate must be true")
	}
	if got := a.ConvergePlaybooks(); len(got) != 1 || got[0].ID != "provision-hosts" {
		t.Errorf("converge set: %+v", got)
	}
	if rb, ok := a.PlaybookByID("postgres-major-upgrade"); !ok || rb.Converge {
		t.Errorf("runbook lookup: %+v ok=%v", rb, ok)
	}
}

// TestAnsiblePlaybook_IsRequired pins the fail-loud default: unset means a
// play failure hard-fails the phase; only an explicit required: false opts
// down to advisory.
func TestAnsiblePlaybook_IsRequired(t *testing.T) {
	if !(AnsiblePlaybook{}).IsRequired() {
		t.Error("unset required must default to true")
	}
	cfg := decodeAnsibleFixture(t, `
ansible:
  image: docker.io/hlhd/ansible:v2.20.4
  inventory: ansible/inventory
  ssh: {user: automation, credentials: DUNGEON, known_hosts: ansible/known_hosts}
  playbooks:
    gating: {path: a.yml, groups: [all], converge: true}
    advisory: {path: b.yml, groups: [all], converge: true, required: false}
`)
	gating, _ := cfg.Ansible.PlaybookByID("gating")
	advisory, _ := cfg.Ansible.PlaybookByID("advisory")
	if !gating.IsRequired() {
		t.Error("gating play must be required")
	}
	if advisory.IsRequired() {
		t.Error("required: false must decode to advisory")
	}
}

// TestAnsibleConfig_EnvPrefix pins the gitops-convention derivation.
func TestAnsibleConfig_EnvPrefix(t *testing.T) {
	s := AnsibleSSH{Credentials: "dungeon-nodes"}
	if s.EnvPrefix() != "DUNGEON_NODES" {
		t.Errorf("EnvPrefix = %q", s.EnvPrefix())
	}
}

// TestValidateAnsible pins the presence-gated rules: absent subsystem is
// silent; declared plays need path+groups; a converge play requires the full
// SSH identity including strict host-key material.
func TestValidateAnsible(t *testing.T) {
	// Absent → no errors.
	cfg := *defaults()
	if errs := validateAnsible(&cfg); len(errs) != 0 {
		t.Errorf("absent subsystem must validate clean: %v", errs)
	}

	// Complete fixture → clean.
	cfg = decodeAnsibleFixture(t, ansibleFixture)
	if errs := validateAnsible(&cfg); len(errs) != 0 {
		t.Errorf("fixture must validate clean: %v", errs)
	}

	// Converge play with missing ssh + inventory + play fields → each named.
	cfg = *defaults()
	cfg.Ansible.Inventory = ""
	cfg.Ansible.Image = ""
	cfg.Ansible.Playbooks = OrderedAnsiblePlaybooks{{ID: "broken", Converge: true}}
	errs := validateAnsible(&cfg)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{
		"ansible.inventory: required",
		"ansible.image: required",
		"ansible.playbooks.broken.path: required",
		"ansible.playbooks.broken.groups: at least one",
		"ansible.ssh.user: required",
		"ansible.ssh.credentials: required",
		"ansible.ssh.known_hosts: required",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing error %q in:\n%s", want, joined)
		}
	}
}
