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
	"regexp"
	"strings"
)

type Ticket string
type Pattern string

const (
	HEADER = Ticket("header")
	COOKIE = Ticket("cookie")
	QUERY  = Ticket("query")

	REGEX = Pattern("regex")
	EXACT = Pattern("exact")

	serviceMatchGroupRegex = `[^,.]+?: *(header|cookie|query)\(.+?,.+?\)`
	serviceMatchParseRegex = `(.+?): *(header|cookie|query)\("(.+?)",(.+?)\)`
)

// MatchRule represents a traffic shipping rule
type MatchRule struct {
	Ticket  Ticket  `json:"ticket"`
	Pattern Pattern `json:"pattern"`
	Key     string  `json:"key"`
	Value   string  `json:"value"`
}

// ServiceMatchConfig returns the configuration rules for gray release
type ServiceMatchConfig struct {
	ServiceMatchEnabled bool
	ServiceMatchCfgMap  map[string]MatchRule
}

// nginx.ingress.kubernetes.io/service-match: 'svc1: header("name", /value/), svc2: cookie("name", "value")'
func parseServiceMatch(ing *extensions.Ingress) (*ServiceMatchConfig, error) {
	serviceMatch, _ := parser.GetStringAnnotation("service-match", ing)
	if len(serviceMatch) == 0 {
		return nil, nil
	}

	configs := make(map[string]MatchRule)
	reg := regexp.MustCompile(serviceMatchGroupRegex)
	matchs := reg.FindAllStringSubmatch(serviceMatch, -1)
	for _, match := range matchs {
		if len(match) == 0 {
			continue
		}

		service, rule, err := parseRule(match[0])
		if err != nil {
			return nil, err
		}

		if len(service) > 0 && rule != nil {
			configs[service] = *rule
		}
	}

	return &ServiceMatchConfig{
		ServiceMatchEnabled: len(configs) > 0,
		ServiceMatchCfgMap:  configs,
	}, nil
}

func parseRule(match string) (string, *MatchRule, error) {
	if len(match) == 0 {
		return "", nil, nil
	}

	reg := regexp.MustCompile(serviceMatchParseRegex)
	items := reg.FindAllStringSubmatch(match, -1)
	if len(items) == 0 || len(items[0]) != 5 {
		return "", nil, errors.NewInvalidAnnotationContent("service-match", match)
	}

	service := strings.TrimSpace(items[0][1])
	if len(service) == 0 {
		return "", nil, errors.NewInvalidAnnotationContent("service-match", match)
	}

	ticket := strings.TrimSpace(items[0][2])
	if !validateTicket(Ticket(ticket)) {
		return "", nil, errors.NewInvalidAnnotationContent("service-match", match)
	}

	key := strings.TrimSpace(items[0][3])
	if len(key) == 0 {
		return "", nil, errors.NewInvalidAnnotationContent("service-match", match)
	}

	value, pattern := parsePatternValue(items[0][4])
	if len(value) == 0 || len(pattern) == 0 {
		return "", nil, errors.NewInvalidAnnotationContent("service-match", match)
	}

	return service, &MatchRule{
		Ticket:  Ticket(ticket),
		Pattern: pattern,
		Key:     key,
		Value:   value,
	}, nil
}

func validateTicket(ticket Ticket) bool {
	return ticket == HEADER || ticket == COOKIE || ticket == QUERY
}

func parsePatternValue(patternValue string) (string, Pattern) {
	pValue := strings.TrimSpace(patternValue)
	if len(pValue) == 0 {
		return "", ""
	}

	if strings.HasPrefix(pValue, "/") && strings.HasSuffix(pValue, "/") {
		pValue = strings.TrimPrefix(pValue, "/")
		pValue = strings.TrimSuffix(pValue, "/")
		return pValue, REGEX
	}

	if strings.HasPrefix(pValue, "\"") && strings.HasSuffix(pValue, "\"") {
		pValue = strings.TrimPrefix(pValue, "\"")
		pValue = strings.TrimSuffix(pValue, "\"")
		return pValue, EXACT
	}

	return "", ""
}
