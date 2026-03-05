// ABOUTME: CSS-like model stylesheet parser for per-node LLM configuration.
// ABOUTME: Supports universal (*), class (.name), and ID (#name) selectors with specificity ordering.
package pipeline

import (
	"fmt"
	"sort"
	"strings"
)

// StyleRule represents a single CSS-like rule with a selector and property map.
type StyleRule struct {
	Selector   string
	Properties map[string]string
}

// Stylesheet holds an ordered list of style rules parsed from a CSS-like input.
type Stylesheet struct {
	Rules []StyleRule
}

// ParseStylesheet parses a CSS-like stylesheet string into a Stylesheet.
// Each rule has the form: selector { key: value; key: value; }
func ParseStylesheet(input string) (*Stylesheet, error) {
	input = strings.TrimSpace(input)
	ss := &Stylesheet{}
	if input == "" {
		return ss, nil
	}

	blocks := strings.Split(input, "}")
	for _, block := range blocks {
		block = strings.TrimSpace(block)
		if block == "" {
			continue
		}
		parts := strings.SplitN(block, "{", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid stylesheet rule: %q", block)
		}
		selector := strings.TrimSpace(parts[0])
		propsStr := strings.TrimSpace(parts[1])
		if selector == "" {
			return nil, fmt.Errorf("empty selector")
		}
		properties := make(map[string]string)
		for _, prop := range strings.Split(propsStr, ";") {
			prop = strings.TrimSpace(prop)
			if prop == "" {
				continue
			}
			colonIdx := strings.Index(prop, ":")
			if colonIdx < 0 {
				return nil, fmt.Errorf("invalid property: %q", prop)
			}
			key := strings.TrimSpace(prop[:colonIdx])
			value := strings.TrimSpace(prop[colonIdx+1:])
			if key != "" && value != "" {
				properties[key] = value
			}
		}
		if len(properties) > 0 {
			ss.Rules = append(ss.Rules, StyleRule{Selector: selector, Properties: properties})
		}
	}
	return ss, nil
}

// specificityOf returns the specificity rank of a selector.
// ID selectors (#name) have highest specificity (2), class selectors (.name)
// are next (1), and the universal selector (*) has the lowest (0).
func specificityOf(selector string) int {
	if strings.HasPrefix(selector, "#") {
		return 2
	}
	if strings.HasPrefix(selector, ".") {
		return 1
	}
	return 0
}

// Resolve applies the stylesheet to a node and returns the final resolved
// property map. Rules are applied in specificity order (low to high), so
// higher-specificity selectors override lower ones. Explicit node attributes
// override all stylesheet rules.
func (ss *Stylesheet) Resolve(node *Node) map[string]string {
	type matchedRule struct {
		specificity int
		order       int
		properties  map[string]string
	}

	var matches []matchedRule
	nodeClasses := parseClasses(node)

	for i, rule := range ss.Rules {
		if ruleMatchesNode(rule.Selector, node, nodeClasses) {
			matches = append(matches, matchedRule{
				specificity: specificityOf(rule.Selector),
				order:       i,
				properties:  rule.Properties,
			})
		}
	}

	// Sort by specificity ascending, then by source order ascending.
	// Later applications overwrite earlier ones, so higher specificity wins.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].specificity != matches[j].specificity {
			return matches[i].specificity < matches[j].specificity
		}
		return matches[i].order < matches[j].order
	})

	resolved := make(map[string]string)
	for _, m := range matches {
		for k, v := range m.properties {
			resolved[k] = v
		}
	}

	// Explicit node attributes override stylesheet properties.
	// Skip structural attributes that are not LLM configuration.
	for k, v := range node.Attrs {
		if k == "class" || k == "shape" || k == "label" {
			continue
		}
		resolved[k] = v
	}

	return resolved
}

// ruleMatchesNode checks whether a selector applies to the given node.
func ruleMatchesNode(selector string, node *Node, nodeClasses map[string]bool) bool {
	if selector == "*" {
		return true
	}
	if strings.HasPrefix(selector, "#") {
		return node.ID == selector[1:]
	}
	if strings.HasPrefix(selector, ".") {
		return nodeClasses[selector[1:]]
	}
	return false
}

// parseClasses extracts the set of class names from a node's "class" attribute.
func parseClasses(node *Node) map[string]bool {
	classes := make(map[string]bool)
	if classAttr, ok := node.Attrs["class"]; ok {
		for _, c := range strings.Fields(classAttr) {
			classes[c] = true
		}
	}
	return classes
}
