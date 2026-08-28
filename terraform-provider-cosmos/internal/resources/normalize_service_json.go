package resources

import (
	"context"
	"encoding/json"

	"github.com/azukaar/terraform-provider-cosmos/internal/client"
)

// normalizeServiceJSON rewrites the raw export document into the canonical
// form stored in state:
//
//   - network_mode "container:<64-hex-id>" is rewritten to
//     "container:<name>" using the servapp container list (Docker embeds
//     the resolved ID in exports; authored config uses the name — see
//     upstream issue azukaar/Cosmos-Server#254). If the ID cannot be
//     resolved the value is left untouched.
//
// The returned document is compact JSON. On any parse failure the original
// string is returned unchanged (callers treat normalization as best-effort).
func normalizeServiceJSON(ctx context.Context, c *client.CosmosClient, exported string) string {
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(exported), &doc); err != nil {
		return exported
	}
	if mode, ok := doc["network_mode"].(string); ok {
		doc["network_mode"] = normalizeNetworkMode(ctx, c, mode, nil)
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return exported
	}
	return string(out)
}
