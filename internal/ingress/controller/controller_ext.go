package controller

import (
	"strings"

	"k8s.io/ingress-nginx/internal/ingress"
	"k8s.io/ingress-nginx/internal/ingress/annotations/canary"
)

// configureReleasePolicy update Backend's TrafficShapingPolicy based on ingress and hostPath
func (n *NGINXController) configureReleasePolicy(upstream *ingress.Backend, ing *ingress.Ingress, hostPath string) {
	annos := ing.ParsedAnnotations
	hostnamePath := strings.Split(hostPath, "/")
	hostname := hostnamePath[0]

	if !annos.Canary.Enabled && (annos.Canary.ServiceWeightEnabled || annos.Canary.ServiceMatchEnabled) {
		upstream.TrafficShapingPolicy = ingress.TrafficShapingPolicy{
			HostPath: hostPath,
		}
	}

	if annos.Canary.ServiceWeightEnabled {
		upstream.TrafficShapingPolicy.ServiceWeight = make(map[string]int, 0)
		for service, weight := range annos.Canary.ServiceWeightCfgMap {
			// name: namespace-service-port
			if name := genUpstreamName(service, ing, hostname); len(name) > 0 {
				upstream.TrafficShapingPolicy.ServiceWeight[name] = weight
			}
		}
	}

	if annos.Canary.ServiceMatchEnabled {
		upstream.TrafficShapingPolicy.ServiceMatch = make(map[string]canary.MatchRule, 0)
		for service, rule := range annos.Canary.ServiceMatchCfgMap {
			if name := genUpstreamName(service, ing, hostname); len(name) > 0 {
				upstream.TrafficShapingPolicy.ServiceMatch[name] = rule
			}
		}
	}
}

// genUpstreamName return namespace-service-port based on service and hostname
func genUpstreamName(service string, ing *ingress.Ingress, hostname string) string {
	for _, rule := range ing.Spec.Rules {
		if hostname == rule.Host {
			for _, path := range rule.HTTP.Paths {
				if service == path.Backend.ServiceName {
					return upstreamName(ing.Namespace, service, path.Backend.ServicePort)
				}
			}
		}
	}
	return ""
}

// mergeReleaseAlternativeBackends update Backend's AlternativeBackends
func (n *NGINXController) mergeReleaseAlternativeBackends(releaseIngresses []*ingress.Ingress,
	upstreams map[string]*ingress.Backend, servers map[string]*ingress.Server) error {
	for _, ing := range releaseIngresses {
		// TODO take the default backend into account?

		for _, rule := range ing.Spec.Rules {
			colleagueBackends := make(map[string][]string, 0)
			for _, path := range rule.HTTP.Paths {
				if _, ok := colleagueBackends[path.Path]; !ok {
					colleagueBackends[path.Path] = make([]string, 0)
				}

				upsName := upstreamName(ing.Namespace, path.Backend.ServiceName, path.Backend.ServicePort)
				if _, ok := upstreams[upsName]; !ok {
					continue
				}

				colleagueBackends[path.Path] = append(colleagueBackends[path.Path], upsName)
			}

			for _, upsNames := range colleagueBackends {
				if len(upsNames) < 2 {
					continue
				}

				for _, upsName := range upsNames {
					upstream, _ := upstreams[upsName]
					alternatives := findAlternativeBackends(upsName, upsNames)
					upstream.AlternativeBackends = alternatives
				}
			}
		}
	}
	return nil
}

// findAlternativeBackends return the alternative backends
func findAlternativeBackends(backend string, backends []string) []string {
	alternatives := make([]string, 0)
	for _, b := range backends {
		if b != backend {
			alternatives = append(alternatives, b)
		}
	}
	return alternatives
}
