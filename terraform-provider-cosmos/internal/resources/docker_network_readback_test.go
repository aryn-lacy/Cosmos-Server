package resources

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azukaar/terraform-provider-cosmos/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// testNetworkListJSON is the envelope returned by GET /api/networks for a
// single user-defined bridge network with an IPAM-assigned subnet.
const testNetworkListJSON = `{"status":"OK","data":[` +
	`{"Id":"net-test-123","Name":"test-net","Driver":"bridge",` +
	`"IPAM":{"Driver":"default","Config":[{"Subnet":"172.24.0.0/16","Gateway":"172.24.0.1"}]}}]}`

// testNetworkNoIPAMJSON covers networks created without an explicit subnet:
// no IPAM config at all.
const testNetworkNoIPAMJSON = `{"status":"OK","data":[` +
	`{"Id":"net-test-123","Name":"test-net","Driver":"bridge","IPAM":{"Driver":"default","Config":[]}}]}`

func newTestNetworkClient(t *testing.T, listBody string) (*dockerNetworkResource, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(listBody))
	}))
	t.Cleanup(srv.Close)

	c, err := client.NewCosmosClient(srv.URL, "", false)
	if err != nil {
		t.Fatalf("NewCosmosClient: %v", err)
	}
	return &dockerNetworkResource{client: c}, srv
}

// parseTestNetwork unmarshals a single-network list envelope into networkInfo,
// exercising the same JSON tags the production code relies on.
func parseTestNetwork(t *testing.T, listJSON string) *networkInfo {
	t.Helper()
	var networks []networkInfo
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal([]byte(listJSON), &envelope); err != nil {
		t.Fatalf("unmarshalling envelope: %v", err)
	}
	if err := json.Unmarshal(envelope.Data, &networks); err != nil {
		t.Fatalf("unmarshalling networks: %v", err)
	}
	if len(networks) == 0 {
		t.Fatal("test payload contains no networks")
	}
	return &networks[0]
}

// The lacy.3-era bug: Create saved the plan verbatim, leaving subnet unknown
// when omitted from config, which tofu rejects after apply. applyNetworkInfo
// (now called from Create via readNetworkIntoModel) must replace unknowns
// with the live IPAM values.
func TestApplyNetworkInfo_ReplacesUnknownsWithLiveValues(t *testing.T) {
	model := &dockerNetworkResourceModel{
		ID:     types.StringUnknown(),
		Name:   types.StringValue("test-net"),
		Driver: types.StringUnknown(),
		Subnet: types.StringUnknown(),
	}

	applyNetworkInfo(model, parseTestNetwork(t, testNetworkListJSON))

	if model.Subnet.IsUnknown() || model.Subnet.IsNull() {
		t.Fatalf("subnet still unknown/null after readback: %s", model.Subnet)
	}
	if got := model.Subnet.ValueString(); got != "172.24.0.0/16" {
		t.Fatalf("subnet = %q, want 172.24.0.0/16", got)
	}
	if model.Driver.IsUnknown() || model.Driver.IsNull() {
		t.Fatalf("driver still unknown/null after readback: %s", model.Driver)
	}
	if got := model.ID.ValueString(); got != "net-test-123" {
		t.Fatalf("id = %q, want net-test-123", got)
	}
}

// A network with no IPAM config must not crash or fabricate a subnet; Create
// nulls any survivors after the readback.
func TestApplyNetworkInfo_NoIPAMKeepsSubnetUntouched(t *testing.T) {
	model := &dockerNetworkResourceModel{
		ID:     types.StringUnknown(),
		Name:   types.StringValue("test-net"),
		Driver: types.StringUnknown(),
		Subnet: types.StringUnknown(),
	}

	applyNetworkInfo(model, &networkInfo{ID: "net-test-123", Name: "test-net", Driver: "bridge"})

	if !model.Subnet.IsUnknown() {
		t.Fatalf("subnet should remain untouched (unknown) with no IPAM config, got %s", model.Subnet)
	}
	if got := model.Driver.ValueString(); got != "bridge" {
		t.Fatalf("driver = %q, want bridge", got)
	}
}

// End-to-end over the wire: readNetworkIntoModel (the Create readback path)
// against a stub Cosmos API, unknowns in, knowns out.
func TestReadNetworkIntoModel_PopulatesFromAPI(t *testing.T) {
	r, _ := newTestNetworkClient(t, testNetworkListJSON)

	model := &dockerNetworkResourceModel{
		ID:     types.StringUnknown(),
		Name:   types.StringValue("test-net"),
		Driver: types.StringUnknown(),
		Subnet: types.StringUnknown(),
	}
	var diags diag.Diagnostics

	ok := r.readNetworkIntoModel(context.Background(), "net-test-123", "test-net", model, &diags)

	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if !ok {
		t.Fatal("readNetworkIntoModel returned false for an existing network")
	}
	if got := model.Subnet.ValueString(); got != "172.24.0.0/16" {
		t.Fatalf("subnet = %q, want 172.24.0.0/16", got)
	}
}

// Refresh path: unknown ID match falls back to name.
func TestFindNetwork_FallsBackToName(t *testing.T) {
	r, _ := newTestNetworkClient(t, testNetworkListJSON)
	var diags diag.Diagnostics

	// ID deliberately different from the one in the list payload.
	found := r.findNetwork(context.Background(), "stale-id", "test-net", &diags)

	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if found == nil {
		t.Fatal("findNetwork returned nil; want name-based fallback match")
	}
	if found.ID != "net-test-123" {
		t.Fatalf("matched ID = %q, want net-test-123", found.ID)
	}
}

func TestFindNetwork_MissingReturnsNilWithoutError(t *testing.T) {
	r, _ := newTestNetworkClient(t, `{"status":"OK","data":[]}`)
	var diags diag.Diagnostics

	found := r.findNetwork(context.Background(), "gone-id", "gone-net", &diags)

	if diags.HasError() {
		t.Fatalf("missing network must not be an error: %+v", diags)
	}
	if found != nil {
		t.Fatalf("findNetwork = %+v, want nil", found)
	}
}

// Read removes the resource from state when the API returns an empty list
// (the "no longer exists" branch).
func TestFindNetwork_EmptyPayloadStopsLookup(t *testing.T) {
	for name, body := range map[string]string{
		"null-data":  `{"status":"OK","data":null}`,
		"empty-list": `{"status":"OK","data":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := newTestNetworkClient(t, body)
			var diags diag.Diagnostics
			if found := r.findNetwork(context.Background(), "x", "y", &diags); found != nil {
				t.Fatalf("findNetwork = %+v, want nil", found)
			}
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %+v", diags)
			}
		})
	}
}

// Regression guard for the hister apply failure: the Create readback via
// readNetworkIntoModel must populate subnet on a model that had it unknown,
// simulating exactly what Create does after the POST succeeds.
func TestCreateReadback_SubnetUnknownToKnown(t *testing.T) {
	r, _ := newTestNetworkClient(t, testNetworkListJSON)

	// This is the state of the model inside Create right before the fix's
	// readback ran: ID set from the create response, subnet unknown because
	// the config omitted it.
	model := &dockerNetworkResourceModel{
		ID:     types.StringValue("net-test-123"),
		Name:   types.StringValue("test-net"),
		Driver: types.StringNull(),
		Subnet: types.StringUnknown(),
	}
	var diags diag.Diagnostics

	if !r.readNetworkIntoModel(context.Background(), "net-test-123", "test-net", model, &diags) {
		t.Fatal("readback failed")
	}
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}

	if model.Subnet.IsUnknown() {
		t.Fatal("BUG REPRODUCED: subnet unknown after Create readback — tofu would reject this state")
	}
}
