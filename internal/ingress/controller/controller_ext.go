package controller

import (
	"k8s.io/ingress-nginx/internal/ingress"
	"k8s.io/ingress-nginx/internal/ingress/annotations/canary"
)

func (n *NGINXController) configureReleasePolicy(upstream *ingress.Backend, ing *ingress.Ingress, hostPath string) {
	annos := ing.ParsedAnnotations

	if !annos.Canary.Enabled && (annos.Canary.ServiceWeightEnabled || annos.Canary.ServiceMatchEnabled) {
		upstream.TrafficShapingPolicy = ingress.TrafficShapingPolicy{
			HostPath: hostPath,
		}
	}

	if annos.Canary.ServiceWeightEnabled {
		upstream.TrafficShapingPolicy.ServiceWeight = make(map[string]int, 0)
		for service, weight := range annos.Canary.ServiceWeightCfgMap {
			if name := genUpstreamName(service, ing); len(name) > 0 {
				upstream.TrafficShapingPolicy.ServiceWeight[name] = weight
			}
		}
	}

	if annos.Canary.ServiceMatchEnabled {
		upstream.TrafficShapingPolicy.ServiceMatch = make(map[string]canary.MatchRule, 0)
		for service, rule := range annos.Canary.ServiceMatchCfgMap {
			if name := genUpstreamName(service, ing); len(name) > 0 {
				upstream.TrafficShapingPolicy.ServiceMatch[name] = rule
			}
		}
	}
}

func genUpstreamName(service string, ing *ingress.Ingress) string {
	for _, rule := range ing.Spec.Rules {
		for _, path := range rule.HTTP.Paths {
			if service == path.Backend.ServiceName {
				return upstreamName(ing.Namespace, service, path.Backend.ServicePort)
			}
		}
	}
	return ""
}

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

			for _, backends := range colleagueBackends {
				if len(backends) < 2 {
					continue
				}

				for _, backend := range backends {
					upstream, _ := upstreams[backend]
					alternatives := findAlternativeBackends(backend, backends)
					upstream.AlternativeBackends = alternatives
				}
			}
		}
	}
	return nil
}

func findAlternativeBackends(backend string, backends []string) []string {
	alternatives := make([]string, 0)
	for _, b := range backends {
		if b != backend {
			alternatives = append(alternatives, b)
		}
	}
	return alternatives
}
