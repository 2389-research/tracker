package pipeline

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

const graphParamPrefix = "params."

// GraphParamAttrKey returns the graph.Attrs key used to store workflow
// param `key` (i.e. "params."+key). Callers should use this helper rather
// than hard-coding the prefix so the shape stays consistent if it ever
// changes.
func GraphParamAttrKey(key string) string {
	return graphParamPrefix + key
}

// ExtractParamsFromGraphAttrs returns params declared in graph attrs under
// the "params." prefix.
func ExtractParamsFromGraphAttrs(graphAttrs map[string]string) map[string]string {
	params := make(map[string]string)
	for key, value := range graphAttrs {
		if !strings.HasPrefix(key, graphParamPrefix) {
			continue
		}
		params[strings.TrimPrefix(key, graphParamPrefix)] = value
	}
	return params
}

// ApplyGraphParamOverrides applies runtime overrides to graph-level params.
// Each override key must already exist in graph attrs as "params.<key>".
func ApplyGraphParamOverrides(g *Graph, overrides map[string]string) error {
	if g == nil || len(overrides) == 0 {
		return nil
	}
	if g.Attrs == nil {
		g.Attrs = make(map[string]string)
	}

	params := ExtractParamsFromGraphAttrs(g.Attrs)
	for key, value := range overrides {
		if _, ok := params[key]; !ok {
			declared := slices.Sorted(maps.Keys(params))
			if len(declared) == 0 {
				return fmt.Errorf("unknown param %q (this workflow declares no params)", key)
			}
			return fmt.Errorf("unknown param %q (declared params: %s)", key, strings.Join(declared, ", "))
		}
		g.Attrs[graphParamPrefix+key] = value
	}
	return nil
}
