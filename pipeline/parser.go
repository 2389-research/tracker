// ABOUTME: Parses Graphviz DOT format into the pipeline Graph model.
// ABOUTME: Uses gographviz for parsing and extracts nodes, edges, and attributes.
package pipeline

import (
	"fmt"
	"strings"

	"github.com/awalterschulze/gographviz"
)

// ParseDOT parses a DOT-format string into a Graph.
// Returns an error if the DOT syntax is invalid or the input is empty.
func ParseDOT(dot string) (*Graph, error) {
	if strings.TrimSpace(dot) == "" {
		return nil, fmt.Errorf("empty DOT input")
	}

	parsed, err := gographviz.Parse([]byte(dot))
	if err != nil {
		return nil, fmt.Errorf("DOT parse error: %w", err)
	}

	collector := newDotCollector()
	if err := gographviz.Analyse(parsed, collector); err != nil {
		return nil, fmt.Errorf("DOT analysis error: %w", err)
	}

	g := NewGraph(cleanQuotes(collector.name))

	// Extract graph-level attributes.
	for key, val := range collector.graphAttrs {
		g.Attrs[cleanQuotes(key)] = cleanQuotes(val)
	}

	// Extract nodes.
	for _, cn := range collector.nodes {
		node := &Node{
			ID:    cleanQuotes(cn.name),
			Attrs: make(map[string]string),
		}

		for key, val := range cn.attrs {
			cleaned := cleanQuotes(val)
			switch key {
			case "shape":
				node.Shape = cleaned
			case "label":
				node.Label = cleaned
			default:
				node.Attrs[key] = cleaned
			}
		}

		g.AddNode(node)
	}

	// Extract edges.
	for _, ce := range collector.edges {
		edge := &Edge{
			From:  cleanQuotes(ce.src),
			To:    cleanQuotes(ce.dst),
			Attrs: make(map[string]string),
		}

		for key, val := range ce.attrs {
			cleaned := cleanQuotes(val)
			switch key {
			case "label":
				edge.Label = cleaned
			case "condition":
				edge.Condition = cleaned
			default:
				edge.Attrs[key] = cleaned
			}
		}

		g.AddEdge(edge)
	}

	return g, nil
}

// cleanQuotes removes surrounding double quotes from a DOT attribute value
// and processes standard escape sequences (\n, \t, \\, \").
func cleanQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return unescapeDOT(s)
}

// unescapeDOT processes DOT escape sequences in attribute values.
func unescapeDOT(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
				i++
			case 't':
				b.WriteByte('\t')
				i++
			case '\\':
				b.WriteByte('\\')
				i++
			case '"':
				b.WriteByte('"')
				i++
			default:
				b.WriteByte(s[i])
			}
		} else {
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// collectedNode holds a parsed DOT node and its raw attributes.
type collectedNode struct {
	name  string
	attrs map[string]string
}

// collectedEdge holds a parsed DOT edge and its raw attributes.
type collectedEdge struct {
	src   string
	dst   string
	attrs map[string]string
}

// dotCollector implements gographviz.Interface to collect DOT elements
// without validating attribute names, allowing custom pipeline attributes.
type dotCollector struct {
	name       string
	graphAttrs map[string]string
	nodes      []*collectedNode
	edges      []*collectedEdge
	nodeIndex  map[string]*collectedNode
}

func newDotCollector() *dotCollector {
	return &dotCollector{
		graphAttrs: make(map[string]string),
		nodeIndex:  make(map[string]*collectedNode),
	}
}

func (d *dotCollector) SetStrict(strict bool) error { return nil }
func (d *dotCollector) SetDir(directed bool) error  { return nil }

func (d *dotCollector) SetName(name string) error {
	d.name = name
	return nil
}

func (d *dotCollector) AddPortEdge(src, srcPort, dst, dstPort string, directed bool, attrs map[string]string) error {
	d.edges = append(d.edges, &collectedEdge{
		src:   src,
		dst:   dst,
		attrs: copyAttrs(attrs),
	})
	return nil
}

func (d *dotCollector) AddEdge(src, dst string, directed bool, attrs map[string]string) error {
	return d.AddPortEdge(src, "", dst, "", directed, attrs)
}

func (d *dotCollector) AddNode(parentGraph string, name string, attrs map[string]string) error {
	if existing, ok := d.nodeIndex[name]; ok {
		// Merge attributes into the existing node.
		for k, v := range attrs {
			existing.attrs[k] = v
		}
		return nil
	}
	cn := &collectedNode{
		name:  name,
		attrs: copyAttrs(attrs),
	}
	d.nodes = append(d.nodes, cn)
	d.nodeIndex[name] = cn
	return nil
}

func (d *dotCollector) AddAttr(parentGraph string, field, value string) error {
	d.graphAttrs[field] = value
	return nil
}

func (d *dotCollector) AddSubGraph(parentGraph string, name string, attrs map[string]string) error {
	return nil
}

func (d *dotCollector) String() string { return "" }

// copyAttrs creates a shallow copy of an attribute map.
func copyAttrs(attrs map[string]string) map[string]string {
	out := make(map[string]string, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	return out
}

// Compile-time check that dotCollector implements gographviz.Interface.
var _ gographviz.Interface = (*dotCollector)(nil)

