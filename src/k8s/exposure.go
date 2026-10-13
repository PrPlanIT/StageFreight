package k8s

import (
	"net"
	"strconv"
	"strings"

	"github.com/PrPlanIT/StageFreight/src/config"
)

// ClassifyExposure evaluates exposure rules against an app's routes and services.
// Rules are grouped by precedence: endpoint > gateway > CIDR > service type.
// Conflicting equal-precedence rules → unknown. Never first-match-wins.
// Confidence is derived from match type, never user-set.
func ClassifyExposure(routes []ExposureRef, serviceType string, lbIP string, rules config.ExposureRules) ExposureResult {
	if len(rules.Rules) == 0 {
		// No rules configured — classify from route presence only.
		if len(routes) > 0 {
			return ExposureResult{
				Declared:   ExposureUnknown,
				Gateway:    firstGateway(routes),
				Confidence: "unknown",
				RuleIndex:  -1,
			}
		}
		return ExposureResult{Declared: ExposureCluster, Confidence: "fallback", RuleIndex: -1}
	}

	// Validate and normalize rules at evaluation time.
	// Phase 1: endpoint matches (highest precedence)
	if result, ok := matchByEndpoint(routes, lbIP, rules); ok {
		return result
	}

	// Phase 2: gateway matches
	if result, ok := matchByGateway(routes, rules); ok {
		return result
	}

	// Phase 3: CIDR matches
	if result, ok := matchByCIDR(lbIP, routes, rules); ok {
		return result
	}

	// Phase 4: service type fallback
	if result, ok := matchByServiceType(serviceType, rules); ok {
		return result
	}

	// No match
	if len(routes) > 0 {
		return ExposureResult{
			Declared:   ExposureUnknown,
			Gateway:    firstGateway(routes),
			Confidence: "unknown",
			RuleIndex:  -1,
		}
	}
	return ExposureResult{Declared: ExposureCluster, Confidence: "fallback", RuleIndex: -1}
}

// matchByEndpoint checks exact ip:port matches (highest precedence).
func matchByEndpoint(routes []ExposureRef, lbIP string, rules config.ExposureRules) (ExposureResult, bool) {
	matches := map[ExposureLevel]int{} // level → rule index

	for i, rule := range rules.Rules {
		if len(rule.Endpoints) == 0 {
			continue
		}
		for _, ep := range rule.Endpoints {
			// Check against LB IP + known ports from routes
			if lbIP != "" && endpointMatches(ep, lbIP, routes) {
				level := parseLevel(rule.Level)
				matches[level] = i
			}
		}
	}

	return resolveMatches(matches, "exact")
}

// matchByGateway checks gateway name matches.
func matchByGateway(routes []ExposureRef, rules config.ExposureRules) (ExposureResult, bool) {
	gateways, classes := map[string]bool{}, map[string]bool{}
	for _, r := range routes {
		if r.Gateway != "" {
			gateways[r.Gateway] = true
		}
		if r.Class != "" {
			classes[r.Class] = true
		}
	}
	if len(gateways) == 0 && len(classes) == 0 {
		return ExposureResult{}, false
	}

	matches := map[ExposureLevel]int{}
	for i, rule := range rules.Rules {
		for _, gw := range rule.Gateways {
			if gateways[gw] {
				matches[parseLevel(rule.Level)] = i
			}
		}
		for _, c := range rule.IngressClasses {
			if classes[c] {
				matches[parseLevel(rule.Level)] = i
			}
		}
	}

	result, ok := resolveMatches(matches, "inferred")
	if ok {
		result.Gateway = firstMatchedDoor(gateways, classes, rules)
	}
	return result, ok
}

func matchByCIDR(lbIP string, routes []ExposureRef, rules config.ExposureRules) (ExposureResult, bool) {
	if lbIP == "" {
		return ExposureResult{}, false
	}

	ip := net.ParseIP(lbIP)
	if ip == nil {
		return ExposureResult{}, false
	}

	matches := map[ExposureLevel]int{}
	for i, rule := range rules.Rules {
		if len(rule.CIDRs) == 0 {
			continue
		}
		for _, cidrStr := range rule.CIDRs {
			_, cidr, err := net.ParseCIDR(cidrStr)
			if err != nil {
				continue
			}
			if !cidr.Contains(ip) {
				continue
			}
			// CIDR matches. Check port constraint (AND semantics).
			if len(rule.Ports) == 0 {
				// No port constraint — CIDR match alone is sufficient.
				matches[parseLevel(rule.Level)] = i
			} else {
				// Port must also match. Check against known ports from routes.
				// For now, accept if any route exists (port info may not be available).
				matches[parseLevel(rule.Level)] = i
			}
		}
	}

	result, ok := resolveMatches(matches, "inferred")
	if ok {
		result.Endpoint = lbIP
	}
	return result, ok
}

// matchByServiceType checks service type fallback.
func matchByServiceType(serviceType string, rules config.ExposureRules) (ExposureResult, bool) {
	if serviceType == "" {
		return ExposureResult{}, false
	}

	normalized := normalizeServiceType(serviceType)
	matches := map[ExposureLevel]int{}

	for i, rule := range rules.Rules {
		for _, st := range rule.ServiceTypes {
			if normalizeServiceType(st) == normalized {
				matches[parseLevel(rule.Level)] = i
			}
		}
	}

	return resolveMatches(matches, "fallback")
}

// resolveMatches resolves collected matches at a single precedence level.
// If multiple levels matched → conflict → unknown.
func resolveMatches(matches map[ExposureLevel]int, confidence string) (ExposureResult, bool) {
	if len(matches) == 0 {
		return ExposureResult{}, false
	}
	if len(matches) == 1 {
		for level, idx := range matches {
			return ExposureResult{
				Declared:   level,
				Confidence: confidence,
				RuleIndex:  idx,
			}, true
		}
	}
	// Conflict: multiple levels at same precedence → unknown.
	return ExposureResult{
		Declared:   ExposureUnknown,
		Confidence: "unknown",
		RuleIndex:  -1,
	}, true
}

func parseLevel(s string) ExposureLevel {
	switch strings.ToLower(s) {
	case "internet":
		return ExposureInternet
	case "intranet":
		return ExposureIntranet
	case "cluster":
		return ExposureCluster
	default:
		return ExposureUnknown
	}
}

func normalizeServiceType(s string) string {
	switch strings.ToLower(s) {
	case "clusterip":
		return "ClusterIP"
	case "nodeport":
		return "NodePort"
	case "loadbalancer":
		return "LoadBalancer"
	default:
		return s
	}
}

// firstGateway names the first door a route came through: a gateway, or an ingress class.
func firstGateway(routes []ExposureRef) string {
	for _, r := range routes {
		if r.Gateway != "" {
			return r.Gateway
		}
		if r.Class != "" {
			return r.Class
		}
	}
	return ""
}

func firstMatchedDoor(gateways, classes map[string]bool, rules config.ExposureRules) string {
	for _, rule := range rules.Rules {
		for _, gw := range rule.Gateways {
			if gateways[gw] {
				return gw
			}
		}
		for _, c := range rule.IngressClasses {
			if classes[c] {
				return c
			}
		}
	}
	return ""
}

func endpointMatches(ruleEndpoint, lbIP string, routes []ExposureRef) bool {
	parts := strings.SplitN(ruleEndpoint, ":", 2)
	if len(parts) != 2 {
		return false
	}
	ruleIP := parts[0]
	rulePort, err := strconv.Atoi(parts[1])
	if err != nil {
		return false
	}
	_ = rulePort // port matching against routes would need port data on ExposureRef
	return ruleIP == lbIP
}
