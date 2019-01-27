/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package canary

import (
	extensions "k8s.io/api/extensions/v1beta1"

	"k8s.io/ingress-nginx/internal/ingress/annotations/parser"
	"k8s.io/ingress-nginx/internal/ingress/errors"
	"strings"
	"strconv"
)

// ServiceWeightConfig returns the configuration rules for blue-green release
type ServiceWeightConfig struct {
	ServiceWeightEnabled bool
	ServiceWeightCfgMap  map[string]int
}

// nginx.ingress.kubernetes.io/service-weight: "new-svc: 20, old-svc: 60"
func parseServiceWeight(ing *extensions.Ingress) (*ServiceWeightConfig, error) {
	serviceWeight, _ := parser.GetStringAnnotation("service-weight", ing)
	if len(serviceWeight) == 0 {
		return nil, nil
	}

	weights := make(map[string]int, 0)
	configs := strings.Split(serviceWeight, ",")
	for _, cfg := range configs {
		kv := strings.Split(cfg, ":")
		if len(kv) != 2 || len(strings.TrimSpace(kv[0])) == 0 {
			return nil, errors.NewInvalidAnnotationContent("service-weight", serviceWeight)
		}

		weight, err := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err != nil {
			return nil, errors.NewInvalidAnnotationContent("service-weight", serviceWeight)
		}

		weights[strings.TrimSpace(kv[0])] = weight
	}

	if len(weights) == 0 {
		return nil, nil
	}

	config := make(map[string]int, 0)
	if len(weights) == 1 { // the colleague weight is 100
		for service, weight := range weights {
			config[service] = 100 * weight / (100 + weight)
			break
		}
	} else {
		// TODO support n > 2 service weight setting
		services := make([]string, 0)
		totalWeight := 0
		for service, weight := range weights {
			totalWeight = totalWeight + weight
			services = append(services, service)
			if len(services) >= 2 {
				break
			}
		}

		for _, service := range services {
			config[service] = 100 * weights[service] / totalWeight
		}
	}

	return &ServiceWeightConfig{
		ServiceWeightEnabled: len(config) > 0,
		ServiceWeightCfgMap:  config,
	}, nil
}
