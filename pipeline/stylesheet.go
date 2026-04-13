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
		rule, err := parseStyleBlock(block)
		if err != nil {
			return nil, err
		}
		if rule != nil {
			ss.Rules = append(ss.Rules, *rule)
		}
	}
	return ss, nil
}

// parseStyleBlock parses a single "selector { props }" block (without the closing brace).
func parseStyleBlock(block string) (*StyleRule, error) {
	parts := strings.SplitN(block, "{", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid stylesheet rule: %q", block)
	}
	selector := strings.TrimSpace(parts[0])
	if selector == "" {
		return nil, fmt.Errorf("empty selector")
	}
	properties, err := parseStyleProperties(strings.TrimSpace(parts[1]))
	if err != nil {
		return nil, err
	}
	if len(properties) == 0 {
		return nil, nil
	}
	return &StyleRule{Selector: selector, Properties: properties}, nil
}

// parseStyleProperties parses semicolon-separated "key: value" declarations.
func parseStyleProperties(propsStr string) (map[string]string, error) {
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
	return properties, nil
}

// specificityOf returns the specificity rank of a selector.
// Specificity order is universal < shape < class < ID.
func specificityOf(selector string) int {
	if selector == "*" {
		return 0
	}
	if strings.HasPrefix(selector, "#") {
		return 3
	}
	if strings.HasPrefix(selector, ".") {
		return 2
	}
	return 1
}

// matchedRule holds a stylesheet rule with its resolved priority metadata.
type matchedRule struct {
	specificity int
	order       int
	properties  map[string]string
}

// Resolve applies the stylesheet to a node and returns the final resolved
// property map. Rules are applied in specificity order (low to high), so
// higher-specificity selectors override lower ones. Explicit node attributes
// override all stylesheet rules.
func (ss *Stylesheet) Resolve(node *Node) map[string]string {
	nodeClasses := parseClasses(node)
	matches := ss.collectMatches(node, nodeClasses)
	sortMatches(matches)

	resolved := mergeMatchedProperties(matches)
	applyExplicitNodeAttrs(resolved, node)
	return resolved
}

// collectMatches returns all stylesheet rules that apply to the node.
func (ss *Stylesheet) collectMatches(node *Node, nodeClasses map[string]bool) []matchedRule {
	var matches []matchedRule
	for i, rule := range ss.Rules {
		if ruleMatchesNode(rule.Selector, node, nodeClasses) {
			matches = append(matches, matchedRule{
				specificity: specificityOf(rule.Selector),
				order:       i,
				properties:  rule.Properties,
			})
		}
	}
	return matches
}

// sortMatches sorts rules by specificity ascending, then source order ascending.
func sortMatches(matches []matchedRule) {
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].specificity != matches[j].specificity {
			return matches[i].specificity < matches[j].specificity
		}
		return matches[i].order < matches[j].order
	})
}

// mergeMatchedProperties combines all matched rule properties into a single map.
func mergeMatchedProperties(matches []matchedRule) map[string]string {
	resolved := make(map[string]string)
	for _, m := range matches {
		for k, v := range m.properties {
			resolved[k] = v
		}
	}
	return resolved
}

// applyExplicitNodeAttrs overlays explicit node attributes, skipping structural keys.
func applyExplicitNodeAttrs(resolved map[string]string, node *Node) {
	for k, v := range node.Attrs {
		if k == "class" || k == "shape" || k == "label" {
			continue
		}
		resolved[k] = v
	}
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
	return node.Shape == selector
}

// parseClasses extracts the set of class names from a node's "class" attribute.
func parseClasses(node *Node) map[string]bool {
	classes := make(map[string]bool)
	if classAttr, ok := node.Attrs["class"]; ok {
		for _, c := range strings.Split(classAttr, ",") {
			c = strings.TrimSpace(c)
			if c == "" {
				continue
			}
			classes[c] = true
		}
	}
	return classes
}
