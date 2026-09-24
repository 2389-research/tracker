// ABOUTME: Merges a request's per-provider options into an adapter's JSON body.
// ABOUTME: Shared by the provider adapters' request translators.
package llm

import (
	"encoding/json"
	"slices"
)

// MergeProviderOptions merges the options map stored under
// providerOpts[providerKey] into a JSON request body, skipping reserved keys
// (options the adapter consumes itself rather than sending on the wire). The
// body comes back unchanged when no options map is stored under providerKey.
func MergeProviderOptions(body []byte, providerOpts map[string]any, providerKey string, reserved ...string) ([]byte, error) {
	optsMap, ok := providerOpts[providerKey].(map[string]any)
	if !ok {
		return body, nil
	}
	var bodyMap map[string]any
	if err := json.Unmarshal(body, &bodyMap); err != nil {
		return nil, err
	}
	for k, v := range optsMap {
		if slices.Contains(reserved, k) {
			continue
		}
		bodyMap[k] = v
	}
	return json.Marshal(bodyMap)
}
