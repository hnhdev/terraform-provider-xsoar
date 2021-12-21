package xsoar

import (
	"context"
	"fmt"
	"github.com/badarsebard/xsoar-sdk-go/openapi"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/resource"
	"io"
	"log"
	"net/http"
	"time"
)

type resourceAccountType struct{}

// GetSchema Resource schema
func (r resourceAccountType) GetSchema(_ context.Context) (tfsdk.Schema, diag.Diagnostics) {
	var planModifiers []tfsdk.AttributePlanModifier
	return tfsdk.Schema{
		Attributes: map[string]tfsdk.Attribute{
			"account_roles": {
				Type: types.ListType{
					ElemType: types.StringType,
				},
				Optional: true,
				Computed: true,
			},
			"host_group_name": {
				Type:     types.StringType,
				Required: true,
			},
			"host_group_id": {
				Type:     types.StringType,
				Computed: true,
				Optional: false,
			},
			"name": {
				Type:          types.StringType,
				Required:      true,
				PlanModifiers: append(planModifiers, tfsdk.RequiresReplace()),
			},
			"propagation_labels": {
				Type: types.ListType{
					ElemType: types.StringType,
				},
				Optional: true,
				Computed: true,
			},
			"id": {
				Type:     types.StringType,
				Computed: true,
			},
		},
	}, nil
}

// NewResource instance
func (r resourceAccountType) NewResource(_ context.Context, p tfsdk.Provider) (tfsdk.Resource, diag.Diagnostics) {
	return resourceAccount{
		p: *(p.(*provider)),
	}, nil
}

type resourceAccount struct {
	p provider
}

// Create a new resource
func (r resourceAccount) Create(ctx context.Context, req tfsdk.CreateResourceRequest, resp *tfsdk.CreateResourceResponse) {
	if !r.p.configured {
		resp.Diagnostics.AddError(
			"Provider not configured",
			"The provider hasn't been configured before apply, likely because it depends on an unknown value from another resource. This leads to weird stuff happening, so we'd prefer if you didn't do that. Thanks!",
		)
		return
	}

	// Retrieve values from plan
	var plan Account
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		log.Printf("%+v\n", req.Plan)
		return
	}

	// Generate API request body from plan
	createAccountRequest := *openapi.NewCreateAccountRequest()
	haGroups, _, err := r.p.client.DefaultApi.ListHAGroups(ctx).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing HA groups",
			"Could not list HA groups: "+err.Error(),
		)
		return
	}
	var hostGroupId = ""
	for _, group := range haGroups {
		if group["name"].(string) == plan.HostGroupName.Value {
			hostGroupId = group["id"].(string)
			break
		}
	}
	createAccountRequest.SetHostGroupId(hostGroupId)
	createAccountRequest.SetName(plan.Name.Value)
	if !plan.AccountRoles.Null && len(plan.AccountRoles.Elems) > 0 {
		var accountRoles []string
		plan.AccountRoles.ElementsAs(ctx, accountRoles, true)
		createAccountRequest.SetAccountRoles(accountRoles)
	} else {
		createAccountRequest.SetAccountRoles([]string{"Administrator"})
	}
	if !plan.PropagationLabels.Null && len(plan.PropagationLabels.Elems) > 0 {
		var propagationLabels []string
		plan.PropagationLabels.ElementsAs(ctx, propagationLabels, true)
		createAccountRequest.SetPropagationLabels(propagationLabels)
	}
	createAccountRequest.SetSyncOnCreation(true)

	// Create new account
	var accounts []map[string]interface{}
	err = resource.RetryContext(ctx, 300*time.Second, func() *resource.RetryError {
		var httpResponse *http.Response
		accounts, httpResponse, err = r.p.client.DefaultApi.CreateAccount(ctx).CreateAccountRequest(createAccountRequest).Execute()
		log.Printf("creating account")
		if err != nil {
			body, _ := io.ReadAll(httpResponse.Body)
			payload, _ := io.ReadAll(httpResponse.Request.Body)
			log.Printf("%s : %s - %s\n", payload, httpResponse.Status, body)
			time.Sleep(30 * time.Second)
			return resource.RetryableError(fmt.Errorf("error creating instance: %s", err))
		}
		return nil
	})
	details, _, err := r.p.client.DefaultApi.ListAccountsDetails(ctx).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing account details",
			"Could not read account details"+err.Error(),
		)
		return
	}

	// Map response body to resource schema attribute
	var result Account
	for _, account := range accounts {
		if account["displayName"].(string) == plan.Name.Value {
			var propagationLabels []attr.Value
			if account["propagationLabels"] == nil {
				propagationLabels = []attr.Value{}
			} else {
				for _, label := range account["propagationLabels"].([]interface{}) {
					propagationLabels = append(propagationLabels, types.String{
						Unknown: false,
						Null:    false,
						Value:   label.(string),
					})
				}
			}

			var hostGroupName string
			for _, group := range haGroups {
				if group["id"].(string) == account["hostGroupId"].(string) {
					hostGroupName = group["name"].(string)
					break
				}
			}

			var roles []attr.Value
			for _, detail := range details {
				castDetail := detail.(map[string]interface{})
				if account["name"].(string) == castDetail["name"].(string) {
					roleObjects := castDetail["roles"].([]interface{})
					for _, roleObject := range roleObjects {
						roleName := roleObject.(map[string]interface{})["name"]
						roles = append(roles, types.String{
							Unknown: false,
							Null:    false,
							Value:   roleName.(string),
						})
					}
				}
			}

			result = Account{
				Name:          types.String{Value: account["displayName"].(string)},
				HostGroupName: types.String{Value: hostGroupName},
				HostGroupId:   types.String{Value: hostGroupId},
				PropagationLabels: types.List{
					Unknown:  false,
					Null:     false,
					Elems:    propagationLabels,
					ElemType: types.StringType,
				},
				AccountRoles: types.List{
					Unknown:  false,
					Null:     false,
					Elems:    roles,
					ElemType: types.StringType,
				},
				Id: types.String{Value: account["id"].(string)},
			}
			break
		}
	}

	// Generate resource state struct
	diags = resp.State.Set(ctx, result)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Read resource information
func (r resourceAccount) Read(ctx context.Context, req tfsdk.ReadResourceRequest, resp *tfsdk.ReadResourceResponse) {
	// Get current state
	var state Account
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get account from API and then update what is in state from what the API returns
	accName := "acc_" + state.Name.Value

	// Get account current value
	account, _, err := r.p.client.DefaultApi.GetAccount(ctx, accName).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error getting account",
			"Could not read account "+accName+": "+err.Error(),
		)
		return
	}

	var propagationLabels []attr.Value
	if account["propagationLabels"] == nil {
		propagationLabels = []attr.Value{}
	} else {
		for _, prop := range account["propagationLabels"].([]interface{}) {
			propagationLabels = append(propagationLabels, types.String{
				Unknown: false,
				Null:    false,
				Value:   prop.(string),
			})
		}
	}

	details, _, err := r.p.client.DefaultApi.ListAccountsDetails(ctx).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing account details",
			"Could not read account details"+err.Error(),
		)
		return
	}
	var roles []attr.Value
	for _, detail := range details {
		castDetail := detail.(map[string]interface{})
		if castDetail["name"] != nil && account["name"].(string) == castDetail["name"].(string) {
			roleObjects := castDetail["roles"].([]interface{})
			for _, roleObject := range roleObjects {
				roleName := roleObject.(map[string]interface{})["name"]
				roles = append(roles, types.String{
					Unknown: false,
					Null:    false,
					Value:   roleName.(string),
				})
			}
		}
	}
	haGroups, _, err := r.p.client.DefaultApi.ListHAGroups(ctx).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing HA groups",
			"Could not read HA groups"+err.Error(),
		)
		return
	}
	var hostGroupName = ""
	for _, group := range haGroups {
		if group["id"].(string) == account["hostGroupId"].(string) {
			hostGroupName = group["name"].(string)
			break
		}
	}

	// Map response body to resource schema attribute
	state = Account{
		Name:          types.String{Value: account["displayName"].(string)},
		HostGroupName: types.String{Value: hostGroupName},
		HostGroupId:   types.String{Value: account["hostGroupId"].(string)},
		PropagationLabels: types.List{
			Unknown:  false,
			Null:     false,
			Elems:    propagationLabels,
			ElemType: types.StringType,
		},
		AccountRoles: types.List{
			Unknown:  false,
			Null:     false,
			Elems:    roles,
			ElemType: types.StringType,
		},
		Id: types.String{Value: account["id"].(string)},
	}

	// Set state
	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Update resource
func (r resourceAccount) Update(ctx context.Context, req tfsdk.UpdateResourceRequest, resp *tfsdk.UpdateResourceResponse) {
	// Get plan values
	var plan Account
	diags := req.Plan.Get(ctx, &plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Get current state
	var state Account
	diags = req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Generate API request body from plan
	// This requires up to two requests: roles and propagation labels, and host migration
	// RolesAndPropagationLabels
	if !plan.AccountRoles.Null || !plan.PropagationLabels.Null {
		updateRolesAndPropagationLabelsRequest := *openapi.NewUpdateRolesAndPropagationLabelsRequest()
		var updateRolesAndPropagationLabels = false
		if !plan.AccountRoles.Null && len(plan.AccountRoles.Elems) > 0 && !plan.AccountRoles.Equal(state.AccountRoles) {
			var roles []string
			for _, elem := range plan.AccountRoles.Elems {
				var role interface{}
				role, _ = elem.ToTerraformValue(ctx)
				roles = append(roles, role.(string))
			}
			updateRolesAndPropagationLabelsRequest.SetSelectedRoles(roles)
			updateRolesAndPropagationLabels = true
		} else {
			var roles []string
			for _, elem := range state.AccountRoles.Elems {
				var role interface{}
				role, _ = elem.ToTerraformValue(ctx)
				roles = append(roles, role.(string))
			}
			updateRolesAndPropagationLabelsRequest.SetSelectedRoles(roles)
		}
		if !plan.PropagationLabels.Null && len(plan.PropagationLabels.Elems) > 0 && !plan.PropagationLabels.Equal(state.PropagationLabels) {
			var propagationLabels []string
			for _, elem := range plan.PropagationLabels.Elems {
				var label interface{}
				label, _ = elem.ToTerraformValue(ctx)
				propagationLabels = append(propagationLabels, label.(string))
			}
			updateRolesAndPropagationLabelsRequest.SetSelectedPropagationLabels(propagationLabels)
			updateRolesAndPropagationLabels = true
		} else {
			var propagationLabels []string
			for _, elem := range state.PropagationLabels.Elems {
				var label interface{}
				label, _ = elem.ToTerraformValue(ctx)
				propagationLabels = append(propagationLabels, label.(string))
			}
			updateRolesAndPropagationLabelsRequest.SetSelectedPropagationLabels(propagationLabels)
		}
		if updateRolesAndPropagationLabels {
			_, _, err := r.p.client.DefaultApi.UpdateAccount(ctx, plan.Name.Value).UpdateRolesAndPropagationLabelsRequest(updateRolesAndPropagationLabelsRequest).Execute()
			if err != nil {
				resp.Diagnostics.AddError(
					"Error update account",
					"Could not update account "+plan.Name.Value+": "+err.Error(),
				)
				return
			}
		}
	}

	// Host
	// todo: implement after updating sdk with account host migration capability
	if plan.HostGroupName.Value != state.HostGroupName.Value {
		haGroups, _, err := r.p.client.DefaultApi.ListHAGroups(ctx).Execute()
		if err != nil {
			resp.Diagnostics.AddError(
				"Error listing HA groups",
				"Could not read HA groups"+err.Error(),
			)
			return
		}
		var targetHostGroupId = ""
		for _, group := range haGroups {
			if group["name"].(string) == plan.HostGroupName.Value {
				targetHostGroupId = group["id"].(string)
				break
			}
		}
		_, _, err = r.p.client.DefaultApi.UpdateAccountHost(ctx, "acc_"+plan.Name.Value, targetHostGroupId).Execute()
		if err != nil {
			resp.Diagnostics.AddError(
				"Error updating account host",
				"Could not update account host for "+plan.Name.Value+": "+err.Error(),
			)
			return
		}
	}

	// Get account from API and then update what is in state from what the API returns
	accName := "acc_" + state.Name.Value

	// Get account current value
	account, _, err := r.p.client.DefaultApi.GetAccount(ctx, accName).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error getting account",
			"Could not read account "+accName+": "+err.Error(),
		)
		return
	}

	var propagationLabels []attr.Value
	if account["propagationLabels"] != nil {
		propagationLabels = []attr.Value{}
	} else {
		for _, prop := range account["propagationLabels"].([]interface{}) {
			propagationLabels = append(propagationLabels, types.String{
				Unknown: false,
				Null:    false,
				Value:   prop.(string),
			})
		}
	}

	details, _, err := r.p.client.DefaultApi.ListAccountsDetails(ctx).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing account details",
			"Could not read account details"+err.Error(),
		)
		return
	}
	var roles []attr.Value
	for _, detail := range details {
		castDetail := detail.(map[string]interface{})
		if account["name"].(string) == castDetail["name"].(string) {
			roleObjects := castDetail["roles"].([]interface{})
			for _, roleObject := range roleObjects {
				roleName := roleObject.(map[string]interface{})["name"]
				roles = append(roles, types.String{
					Unknown: false,
					Null:    false,
					Value:   roleName.(string),
				})
			}
		}
	}
	haGroups, _, err := r.p.client.DefaultApi.ListHAGroups(ctx).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing HA groups",
			"Could not read HA groups"+err.Error(),
		)
		return
	}
	var hostGroupName = ""
	for _, group := range haGroups {
		if group["id"].(string) == account["hostGroupId"].(string) {
			hostGroupName = group["name"].(string)
			break
		}
	}
	// Map response body to resource schema attribute
	result := Account{
		Name:          types.String{Value: account["displayName"].(string)},
		HostGroupName: types.String{Value: hostGroupName},
		HostGroupId:   types.String{Value: account["hostGroupId"].(string)},
		PropagationLabels: types.List{
			Unknown:  false,
			Null:     false,
			Elems:    propagationLabels,
			ElemType: types.StringType,
		},
		AccountRoles: types.List{
			Unknown:  false,
			Null:     false,
			Elems:    roles,
			ElemType: types.StringType,
		},
		Id: types.String{Value: account["id"].(string)},
	}

	// Set state
	diags = resp.State.Set(ctx, result)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}

// Delete resource
func (r resourceAccount) Delete(ctx context.Context, req tfsdk.DeleteResourceRequest, resp *tfsdk.DeleteResourceResponse) {
	var state Account
	diags := req.State.Get(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	accName := "acc_" + state.Name.Value

	err := resource.RetryContext(ctx, 300*time.Second, func() *resource.RetryError {
		// Get account current value
		account, _, _ := r.p.client.DefaultApi.GetAccount(ctx, accName).Execute()
		if account != nil {
			_, httpResponse, err := r.p.client.DefaultApi.DeleteAccount(ctx, accName).Execute()
			if err != nil {
				body, bodyErr := io.ReadAll(httpResponse.Body)
				if bodyErr != nil {
					log.Println("error reading body: " + bodyErr.Error())
				}
				log.Printf("code: %d status: %s body: %s\n", httpResponse.StatusCode, httpResponse.Status, string(body))
				return resource.RetryableError(fmt.Errorf("error deleting instance: %s", err))
			}
		}
		return nil
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Error deleting account",
			"Could not delete account: "+err.Error(),
		)
		return
	}
	resp.State.RemoveResource(ctx)
}

func (r resourceAccount) ImportState(ctx context.Context, req tfsdk.ImportResourceStateRequest, resp *tfsdk.ImportResourceStateResponse) {
	var diags diag.Diagnostics
	accName := "acc_" + req.ID
	// Get account current value
	account, _, err := r.p.client.DefaultApi.GetAccount(ctx, accName).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error getting account",
			"Could not read account "+accName+": "+err.Error(),
		)
		return
	}

	var propagationLabels []attr.Value
	if account["propagationLabels"] != nil {
		propagationLabels = []attr.Value{}
	} else {
		if account["propagationLabels"] != nil {
			for _, prop := range account["propagationLabels"].([]interface{}) {
				propagationLabels = append(propagationLabels, types.String{
					Unknown: false,
					Null:    false,
					Value:   prop.(string),
				})
			}
		}
	}

	details, _, err := r.p.client.DefaultApi.ListAccountsDetails(ctx).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing account details",
			"Could not read account details"+err.Error(),
		)
		return
	}
	var roles []attr.Value
	for _, detail := range details {
		castDetail := detail.(map[string]interface{})
		if account["name"].(string) == castDetail["name"].(string) {
			roleObjects := castDetail["roles"].([]interface{})
			for _, roleObject := range roleObjects {
				roleName := roleObject.(map[string]interface{})["name"]
				roles = append(roles, types.String{
					Unknown: false,
					Null:    false,
					Value:   roleName.(string),
				})
			}
		}
	}
	haGroups, _, err := r.p.client.DefaultApi.ListHAGroups(ctx).Execute()
	if err != nil {
		resp.Diagnostics.AddError(
			"Error listing HA groups",
			"Could not read HA groups"+err.Error(),
		)
		return
	}
	var hostGroupName = ""
	for _, group := range haGroups {
		if group["id"].(string) == account["hostGroupId"].(string) {
			hostGroupName = group["name"].(string)
			break
		}
	}
	// Map response body to resource schema attribute
	var state = Account{
		Name:          types.String{Value: account["displayName"].(string)},
		HostGroupName: types.String{Value: hostGroupName},
		PropagationLabels: types.List{
			Unknown:  false,
			Null:     false,
			Elems:    propagationLabels,
			ElemType: types.StringType,
		},
		AccountRoles: types.List{
			Unknown:  false,
			Null:     false,
			Elems:    roles,
			ElemType: types.StringType,
		},
		Id: types.String{Value: account["id"].(string)},
	}

	// Set state
	diags = resp.State.Set(ctx, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
}
