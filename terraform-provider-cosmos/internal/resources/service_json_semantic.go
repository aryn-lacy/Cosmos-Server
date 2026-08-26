package resources

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
)

// semanticServiceJSON is a plan modifier for cosmos_docker_service's
// service_json attribute. The configuration value comes from jsonencode()
// over a user/generator-authored body, while a freshly imported state value
// comes verbatim from the Cosmos /export endpoint. The two are equivalent
// but never byte-identical: the export carries Cosmos-managed fields
// ("routes", "mac_address"), explicit nulls for keys the config omits, and
// Docker's unstable mount ordering. A byte comparison therefore reports a
// change (and, with RequiresReplace, a replacement) on every plan for
// imported services. This modifier compares the two documents semantically
// — ignoring that known noise — and adopts the state value when they match,
// yielding a clean plan.
type semanticServiceJSON struct{}

// SemanticServiceJSON returns a service_json plan modifier that reconciles
// config-vs-state equivalence at plan time.
func SemanticServiceJSON() planmodifier.String {
	return semanticServiceJSON{}
}

func (m semanticServiceJSON) Description(_ context.Context) string {
	return "Compares service_json semantically, ignoring Cosmos-managed fields, null-vs-absent keys, and volume order"
}

func (m semanticServiceJSON) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m semanticServiceJSON) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Only reconcile when we have concrete config, state, and plan values.
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.StateValue.IsNull() || req.StateValue.IsUnknown() {
		return
	}
	if req.PlanValue.IsUnknown() {
		return
	}
	// Byte-identical: nothing to reconcile; later modifiers (RequiresReplace)
	// see no change either.
	if req.PlanValue.Equal(req.StateValue) {
		return
	}

	if serviceJSONEquivalent(req.StateValue.ValueString(), req.PlanValue.ValueString()) {
		// Adopt the prior state value so the attribute — and the resource —
		// plan as unchanged.
		resp.PlanValue = req.StateValue
	}
}

// serviceJSONEquivalent reports whether two service definition documents are
// equivalent for drift purposes. Differences ignored:
//
//   - top-level "routes": managed by Cosmos from cosmos_route resources;
//     present in exports, intentionally absent from generated config
//   - "mac_address": Docker regenerates it whenever a container is recreated
//   - null vs absent object keys: exports materialize nulls the config omits
//   - "volumes" ordering: Docker's mount order is not stable across
//     restarts/inspects; entries are compared as a multiset
func serviceJSONEquivalent(a, b string) bool {
	var va, vb interface{}
	if err := json.Unmarshal([]byte(a), &va); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &vb); err != nil {
		return false
	}
	return valueEquivalent(va, vb, "")
}

// ignoredTopLevelKeys are Cosmos/Docker-managed keys that never participate
// in service definition drift.
var ignoredTopLevelKeys = map[string]bool{
	"routes":      true,
	"mac_address": true,
}

func valueEquivalent(a, b interface{}, key string) bool {
	switch at := a.(type) {
	case map[string]interface{}:
		bt, ok := b.(map[string]interface{})
		if !ok {
			return false
		}
		for k, av := range at {
			if ignoredTopLevelKeys[k] {
				continue
			}
			bv, present := bt[k]
			if !present {
				// null vs absent
				if av == nil {
					continue
				}
				return false
			}
			if !valueEquivalent(av, bv, k) {
				return false
			}
		}
		for k, bv := range bt {
			if ignoredTopLevelKeys[k] {
				continue
			}
			if _, present := at[k]; !present && bv != nil {
				return false
			}
		}
		return true
	case []interface{}:
		bt, ok := b.([]interface{})
		if !ok {
			return false
		}
		if len(at) != len(bt) {
			return false
		}
		if key == "volumes" || key == "ports" {
			// Docker mount and port-mapping order is not stable across
			// restarts/inspects (both derive from map iteration): compare
			// as a multiset.
			return listMultisetEqual(at, bt)
		}
		for i := range at {
			if !valueEquivalent(at[i], bt[i], key) {
				return false
			}
		}
		return true
	case nil:
		return b == nil
	default:
		return scalarEquivalent(a, b)
	}
}

// listMultisetEqual compares two slices of JSON values ignoring order. Keyed
// matching avoids the O(n!) worst case of naive pairwise search; entries are
// bucketed by their canonical JSON encoding, then buckets must match by
// count and by canonical equality of members.
func listMultisetEqual(a, b []interface{}) bool {
	if len(a) != len(b) {
		return false
	}
	bucketsA := bucketByCanonical(a)
	bucketsB := bucketByCanonical(b)
	if len(bucketsA) != len(bucketsB) {
		return false
	}
	for k, countA := range bucketsA {
		if bucketsB[k] != countA {
			return false
		}
	}
	return true
}

func bucketByCanonical(list []interface{}) map[string]int {
	out := make(map[string]int, len(list))
	for _, v := range list {
		canonical, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		out[string(canonical)]++
	}
	return out
}

// scalarEquivalent compares scalars via their canonical JSON encoding, which
// normalizes number formatting differences between documents.
func scalarEquivalent(a, b interface{}) bool {
	ba, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return bytes.Equal(ba, bb)
}

// interface guard: ensures the modifier satisfies the planmodifier.String
// interface at compile time.
var _ planmodifier.String = semanticServiceJSON{}
