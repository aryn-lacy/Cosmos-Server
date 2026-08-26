package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	cosmossdk "github.com/azukaar/cosmos-server/go-sdk"
	"github.com/azukaar/terraform-provider-cosmos/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &dockerServiceResource{}
	_ resource.ResourceWithImportState = &dockerServiceResource{}
)

func NewDockerServiceResource() resource.Resource {
	return &dockerServiceResource{}
}

type dockerServiceResource struct {
	client *client.CosmosClient
}

// ---------- Terraform model ----------

type dockerServiceModel struct {
	Name        types.String `tfsdk:"name"`
	Image       types.String `tfsdk:"image"`
	ServiceJSON types.String `tfsdk:"service_json"`
}

// ---------- resource.Resource ----------

func (r *dockerServiceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_docker_service"
}

func (r *dockerServiceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Docker service (container) in Cosmos Server.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Description: "The container name. Used as the resource ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"image": schema.StringAttribute{
				Description: "The Docker image to use for the container.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"service_json": schema.StringAttribute{
				Description: "Optional JSON blob representing the full container configuration (ports, volumes, environment, etc.). Changes trigger container recreation.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					SemanticServiceJSON(),
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *dockerServiceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.CosmosClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.CosmosClient, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

// ---------- CRUD ----------

func (r *dockerServiceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan dockerServiceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	containerName := plan.Name.ValueString()
	image := plan.Image.ValueString()

	// Build the container definition. Start from service_json if provided,
	// otherwise construct a minimal container config.
	var container cosmossdk.DockerContainerCreateRequestContainer
	if !plan.ServiceJSON.IsNull() && !plan.ServiceJSON.IsUnknown() && plan.ServiceJSON.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.ServiceJSON.ValueString()), &container); err != nil {
			resp.Diagnostics.AddError("Error parsing service_json", err.Error())
			return
		}
	}

	// Always set image and container_name from the explicit attributes.
	container.Image = image
	container.ContainerName = client.StringPtr(containerName)

	services := map[string]cosmossdk.DockerContainerCreateRequestContainer{
		containerName: container,
	}

	createReq := cosmossdk.DockerDockerServiceCreateRequest{
		Services: &services,
	}

	httpResp, err := r.client.Raw.PostApiDockerService(ctx, createReq)
	if err != nil {
		resp.Diagnostics.AddError("Error creating docker service", err.Error())
		return
	}
	// This endpoint streams progress text (pull logs, status messages) before
	// the final result. We read the full body and check for success markers
	// rather than trying to parse it as a standard JSON envelope.
	if err := client.CheckStreamingResponse(httpResp); err != nil {
		resp.Diagnostics.AddError("Error creating docker service", err.Error())
		return
	}

	// Read back the created container to populate computed fields.
	if !r.readContainer(ctx, containerName, &plan, &resp.Diagnostics) {
		resp.Diagnostics.AddError("Error reading docker service", "Container not found after creation")
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dockerServiceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state dockerServiceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	found := r.readContainer(ctx, state.Name.ValueString(), &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if !found {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *dockerServiceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan dockerServiceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	containerName := plan.Name.ValueString()
	image := plan.Image.ValueString()

	// Build the update body from service_json or a minimal config.
	var updateBody map[string]interface{}
	if !plan.ServiceJSON.IsNull() && !plan.ServiceJSON.IsUnknown() && plan.ServiceJSON.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.ServiceJSON.ValueString()), &updateBody); err != nil {
			resp.Diagnostics.AddError("Error parsing service_json for update", err.Error())
			return
		}
	} else {
		updateBody = make(map[string]interface{})
	}

	// Always ensure the image is set.
	updateBody["image"] = image
	updateBody["container_name"] = containerName

	jsonBytes, err := json.Marshal(updateBody)
	if err != nil {
		resp.Diagnostics.AddError("Error marshalling update body", err.Error())
		return
	}

	httpResp, err := r.client.Raw.PostApiServappsContainerIdUpdateWithBody(
		ctx, containerName, "application/json", bytes.NewReader(jsonBytes),
	)
	if err != nil {
		resp.Diagnostics.AddError("Error updating docker service", err.Error())
		return
	}
	if err := client.CheckStreamingResponse(httpResp); err != nil {
		resp.Diagnostics.AddError("Error updating docker service", err.Error())
		return
	}

	// Read back the updated container.
	if !r.readContainer(ctx, containerName, &plan, &resp.Diagnostics) {
		resp.Diagnostics.AddError("Error reading docker service", "Container not found after update")
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *dockerServiceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state dockerServiceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	containerName := state.Name.ValueString()

	// Stop the container first — Docker won't remove a running container.
	stopAction := cosmossdk.GetApiServappsContainerIdManageActionParamsAction("stop")
	httpResp, err := r.client.Raw.GetApiServappsContainerIdManageAction(ctx, containerName, stopAction)
	if err != nil {
		resp.Diagnostics.AddError("Error stopping docker service", err.Error())
		return
	}
	if err := client.CheckStreamingResponse(httpResp); err != nil {
		resp.Diagnostics.AddError("Error stopping docker service", err.Error())
		return
	}

	// Now remove it.
	removeAction := cosmossdk.GetApiServappsContainerIdManageActionParamsAction("remove")
	httpResp, err = r.client.Raw.GetApiServappsContainerIdManageAction(ctx, containerName, removeAction)
	if err != nil {
		resp.Diagnostics.AddError("Error deleting docker service", err.Error())
		return
	}
	if err := client.CheckStreamingResponse(httpResp); err != nil {
		resp.Diagnostics.AddError("Error deleting docker service", err.Error())
		return
	}
}

func (r *dockerServiceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}

// ---------- helpers ----------

// readContainer fetches a container by name and populates the model.
// It returns false if the container was not found (404), in which case no
// diagnostic error is added — the caller should remove the resource from state.
func (r *dockerServiceResource) readContainer(ctx context.Context, containerName string, model *dockerServiceModel, diags interface {
	AddError(string, string)
	HasError() bool
}) bool {
	httpResp, err := r.client.Raw.GetApiServappsContainerId(ctx, containerName)
	if err != nil {
		diags.AddError("Error reading docker service", err.Error())
		return true
	}

	rawData, err := client.ParseRawResponse(httpResp)
	if err != nil {
		if client.IsNotFound(err) {
			return false
		}
		diags.AddError("Error reading docker service", err.Error())
		return true
	}
	if rawData == nil {
		return false
	}

	// Parse as a generic map to extract fields and store the full JSON.
	var containerData map[string]interface{}
	if err := json.Unmarshal(rawData, &containerData); err != nil {
		diags.AddError("Error parsing docker service response", err.Error())
		return true
	}

	model.Name = types.StringValue(containerName)

	// Extract image from the Config block if available (docker inspect format),
	// otherwise fall back to top-level "image" field.
	if cfg, ok := containerData["Config"].(map[string]interface{}); ok {
		if img, ok := cfg["Image"].(string); ok {
			model.Image = types.StringValue(img)
		}
	} else if img, ok := containerData["image"].(string); ok {
		model.Image = types.StringValue(img)
	}

	// service_json is Optional+Computed, but a null state value against a
	// non-null plan forced replacement on every plan for imported services.
	// On import (null state) fetch the container's service definition from
	// the export endpoint, which returns the same schema the configuration
	// uses. The export is NOT byte-identical to a config's jsonencode()
	// (it carries Cosmos-managed fields like "routes", explicit null keys,
	// and Docker's unstable mount order), so we never overwrite a non-null
	// value: a custom plan modifier (semanticJSONEquals) reconciles the
	// config-vs-state comparison at plan time instead. Overwriting here
	// would re-introduce a perpetual replacement loop.
	if model.ServiceJSON.IsNull() {
		if exported, ok := r.fetchServiceJSON(ctx, containerName); ok {
			model.ServiceJSON = types.StringValue(exported)
		}
	}

	return true
}

// fetchServiceJSON retrieves the Cosmos service definition for a container
// via /api/servapps/{id}/export. Returns ok=false on any failure; callers
// treat that as "cannot refresh service_json" rather than a hard error.
func (r *dockerServiceResource) fetchServiceJSON(ctx context.Context, containerName string) (string, bool) {
	httpResp, err := r.client.Raw.GetApiServappsContainerIdExport(ctx, containerName)
	if err != nil {
		return "", false
	}
	rawData, err := client.ParseRawResponse(httpResp)
	if err != nil {
		return "", false
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawData, &envelope); err != nil || len(envelope.Data) == 0 {
		return "", false
	}
	return string(envelope.Data), true
}

// jsonSemanticallyEqual reports whether two JSON documents are equivalent
// once normalized (object key order, insignificant whitespace). Returns
// false if either document fails to parse.
func jsonSemanticallyEqual(a, b string) bool {
	var na, nb interface{}
	if err := json.Unmarshal([]byte(a), &na); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &nb); err != nil {
		return false
	}
	normalizedA, err := json.Marshal(na)
	if err != nil {
		return false
	}
	normalizedB, err := json.Marshal(nb)
	if err != nil {
		return false
	}
	return bytes.Equal(normalizedA, normalizedB)
}
