package resources

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/azukaar/terraform-provider-cosmos/internal/client"
)

// containerIDPattern matches Docker's 64-hex container IDs embedded in
// network_mode values ("container:<id>").
var containerIDPattern = regexp.MustCompile(`^container:([0-9a-f]{64})$`)

// resolveContainerNames fetches the servapp container list and returns an
// id -> name map. Docker rewrites "container:<name>" to "container:<raw-id>"
// in the live config (upstream issue azukaar/Cosmos-Server#254), so exports
// carry the ID while authored configuration uses the name. The name form is
// the stable, portable one: Docker re-resolves it at start time.
func resolveContainerNames(ctx context.Context, c *client.CosmosClient) (map[string]string, error) {
	resp, err := c.Raw.GetApiServapps(ctx, nil)
	if err != nil {
		return nil, err
	}
	raw, err := client.ParseRawResponse(resp)
	if err != nil {
		return nil, err
	}
	var containers []struct {
		Id    string   `json:"Id"`
		Names []string `json:"Names"`
	}
	if err := json.Unmarshal(raw, &containers); err != nil {
		return nil, fmt.Errorf("parsing container list: %w", err)
	}
	out := make(map[string]string, len(containers))
	for _, ct := range containers {
		if ct.Id == "" || len(ct.Names) == 0 {
			continue
		}
		name := strings.TrimPrefix(ct.Names[0], "/")
		if name != "" {
			out[ct.Id] = name
		}
	}
	return out, nil
}

// normalizeNetworkMode rewrites "container:<64-hex-id>" to
// "container:<name>" when the ID resolves via the container list. Values
// that are already names, host/bridge/none, or unresolvable IDs are
// returned unchanged.
func normalizeNetworkMode(ctx context.Context, c *client.CosmosClient, mode string, idToName map[string]string) string {
	m := containerIDPattern.FindStringSubmatch(mode)
	if m == nil {
		return mode
	}
	if idToName == nil {
		var err error
		idToName, err = resolveContainerNames(ctx, c)
		if err != nil || idToName == nil {
			return mode
		}
	}
	if name, ok := idToName[m[1]]; ok {
		return "container:" + name
	}
	return mode
}
