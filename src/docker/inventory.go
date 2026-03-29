package docker

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// AnsibleInventory implements InventorySource by parsing Ansible inventory files.
// Supports YAML and INI formats. Resolves hosts by group membership.
// DD-UI proven patterns carried forward.
type AnsibleInventory struct {
	Path string
}

func (a *AnsibleInventory) Name() string { return "ansible" }

// Resolve parses the inventory and returns hosts matching the selector's groups.
func (a *AnsibleInventory) Resolve(_ context.Context, selector TargetSelector) ([]HostTarget, error) {
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return nil, fmt.Errorf("reading inventory %s: %w", a.Path, err)
	}

	allHosts, groupMap, err := parseInventory(data)
	if err != nil {
		return nil, fmt.Errorf("parsing inventory %s: %w", a.Path, err)
	}

	if len(selector.Groups) == 0 {
		return nil, fmt.Errorf("selector has no groups — targets must be declared, not inferred")
	}

	// Resolve group membership (including children)
	eligible := map[string]bool{}
	for _, g := range selector.Groups {
		members := resolveGroupMembers(g, groupMap)
		for _, m := range members {
			eligible[m] = true
		}
	}

	var targets []HostTarget
	for _, h := range allHosts {
		if !eligible[h.Name] {
			continue
		}
		// Determine which selector groups this host belongs to
		var memberGroups []string
		for _, g := range selector.Groups {
			members := resolveGroupMembers(g, groupMap)
			for _, m := range members {
				if m == h.Name {
					memberGroups = append(memberGroups, g)
				}
			}
		}
		h.Groups = memberGroups
		targets = append(targets, h)
	}

	sort.Slice(targets, func(i, j int) bool {
		return targets[i].Name < targets[j].Name
	})

	return targets, nil
}

// ansibleYAML is the top-level Ansible inventory YAML structure.
type ansibleYAML struct {
	All struct {
		Hosts    map[string]map[string]any `yaml:"hosts"`
		Children map[string]ansibleGroup   `yaml:"children"`
	} `yaml:"all"`
}

type ansibleGroup struct {
	Hosts    map[string]map[string]any `yaml:"hosts"`
	Children map[string]ansibleGroup   `yaml:"children"`
	Vars     map[string]any            `yaml:"vars"`
}

// groupMap maps group name → direct host members + child group names
type groupInfo struct {
	Hosts    []string
	Children []string
}

// parseInventory tries YAML then INI.
func parseInventory(data []byte) ([]HostTarget, map[string]groupInfo, error) {
	hosts, groups, err := parseYAMLInventory(data)
	if err == nil && len(hosts) > 0 {
		return hosts, groups, nil
	}

	hosts, err = parseINIInventory(data)
	if err == nil && len(hosts) > 0 {
		// INI doesn't have rich group info
		return hosts, map[string]groupInfo{}, nil
	}

	return nil, nil, fmt.Errorf("unable to parse inventory as YAML or INI")
}

func parseYAMLInventory(data []byte) ([]HostTarget, map[string]groupInfo, error) {
	var inv ansibleYAML
	if err := yaml.Unmarshal(data, &inv); err != nil {
		return nil, nil, err
	}

	allHosts := map[string]*HostTarget{}
	groups := map[string]groupInfo{}

	// Top-level hosts
	for name, vars := range inv.All.Hosts {
		allHosts[name] = buildHost(name, vars)
	}

	// Walk children recursively
	var walkGroup func(name string, g ansibleGroup)
	walkGroup = func(name string, g ansibleGroup) {
		gi := groupInfo{}
		for hostName, vars := range g.Hosts {
			if _, exists := allHosts[hostName]; !exists {
				allHosts[hostName] = buildHost(hostName, vars)
			}
			gi.Hosts = append(gi.Hosts, hostName)
		}
		for childName, childGroup := range g.Children {
			gi.Children = append(gi.Children, childName)
			walkGroup(childName, childGroup)
		}
		sort.Strings(gi.Hosts)
		sort.Strings(gi.Children)
		groups[name] = gi
	}

	for name, group := range inv.All.Children {
		walkGroup(name, group)
	}

	// Convert to sorted slice
	var result []HostTarget
	for _, h := range allHosts {
		result = append(result, *h)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})

	return result, groups, nil
}

func buildHost(name string, vars map[string]any) *HostTarget {
	h := &HostTarget{
		Name: name,
		Vars: map[string]string{},
	}
	for k, v := range vars {
		if k == "ansible_host" {
			h.Address = fmt.Sprintf("%v", v)
		} else {
			h.Vars[k] = fmt.Sprintf("%v", v)
		}
	}
	return h
}

// resolveGroupMembers returns all hosts in a group, including children recursively.
func resolveGroupMembers(group string, groups map[string]groupInfo) []string {
	gi, ok := groups[group]
	if !ok {
		return nil
	}

	seen := map[string]bool{}
	var result []string

	var walk func(g string)
	walk = func(g string) {
		info, ok := groups[g]
		if !ok {
			return
		}
		for _, h := range info.Hosts {
			if !seen[h] {
				seen[h] = true
				result = append(result, h)
			}
		}
		for _, child := range info.Children {
			walk(child)
		}
	}

	// Direct hosts
	for _, h := range gi.Hosts {
		if !seen[h] {
			seen[h] = true
			result = append(result, h)
		}
	}
	// Children
	for _, child := range gi.Children {
		walk(child)
	}

	sort.Strings(result)
	return result
}

// parseINIInventory handles minimal INI-style: "hostname ansible_host=1.2.3.4 key=val"
func parseINIInventory(data []byte) ([]HostTarget, error) {
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	var hosts []HostTarget
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "[") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		h := HostTarget{Name: fields[0], Vars: map[string]string{}}
		for _, f := range fields[1:] {
			kv := strings.SplitN(f, "=", 2)
			if len(kv) != 2 {
				continue
			}
			if kv[0] == "ansible_host" {
				h.Address = kv[1]
			} else {
				h.Vars[kv[0]] = kv[1]
			}
		}
		hosts = append(hosts, h)
	}
	if len(hosts) == 0 {
		return nil, fmt.Errorf("no hosts found in INI inventory")
	}
	return hosts, nil
}
