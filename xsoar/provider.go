package xsoar

import (
	"context"
	"crypto/tls"
	"github.com/badarsebard/xsoar-sdk-go/openapi"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"net/http"
	"os"
)

var _ = os.Stderr

func New() func() tfsdk.Provider {
	return func() tfsdk.Provider {
		return &provider{}
	}
}

type provider struct {
	configured bool
	client     *openapi.APIClient
	data       *providerData
}

func (p *provider) GetSchema(_ context.Context) (tfsdk.Schema, diag.Diagnostics) {
	return tfsdk.Schema{
		Attributes: map[string]tfsdk.Attribute{
			"main_host": {
				Type:     types.StringType,
				Optional: true,
			},
			"api_key": {
				Type:     types.StringType,
				Optional: true,
			},
			"insecure": {
				Type:     types.BoolType,
				Optional: true,
			},
			"http_headers_from_env": {
				Type:     types.MapType{ElemType: types.StringType},
				Optional: true,
			},
		},
	}, nil
}

// Provider schema struct
type providerData struct {
	Apikey             types.String      `tfsdk:"api_key"`
	MainHost           types.String      `tfsdk:"main_host"`
	Insecure           types.Bool        `tfsdk:"insecure"`
	HttpHeadersFromEnv map[string]string `tfsdk:"http_headers_from_env"`
}

func (p *provider) Configure(ctx context.Context, req tfsdk.ConfigureProviderRequest, resp *tfsdk.ConfigureProviderResponse) {
	// Retrieve provider data from configuration
	var config providerData
	diags := req.Config.Get(ctx, &config)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// User must provide an api key to the provider
	var apikey string
	if config.Apikey.Unknown {
		// Cannot connect to client with an unknown value
		resp.Diagnostics.AddWarning(
			"Unable to create client",
			"Cannot use unknown value as API key",
		)
		return
	}

	if config.Apikey.Null {
		config.Apikey.Value = os.Getenv("DEMISTO_API_KEY")
		config.Apikey.Null = false
	}
	apikey = config.Apikey.Value

	if apikey == "" {
		// Error vs warning - empty value must stop execution
		resp.Diagnostics.AddError(
			"Unable to find API key",
			"API key cannot be an empty string",
		)
		return
	}

	// User must specify a host
	var mainhost string
	if config.MainHost.Unknown {
		// Cannot connect to client with an unknown value
		resp.Diagnostics.AddError(
			"Unable to create client",
			"Cannot use unknown value as main host",
		)
		return
	}

	if config.MainHost.Null {
		config.MainHost.Value = os.Getenv("DEMISTO_BASE_URL")
		config.MainHost.Null = false
	}
	mainhost = config.MainHost.Value

	if mainhost == "" {
		// Error vs warning - empty value must stop execution
		resp.Diagnostics.AddError(
			"Unable to find main host",
			"Main host cannot be an empty string",
		)
		return
	}

	var insecure bool
	if config.Insecure.Null {
		if len(os.Getenv("DEMISTO_INSECURE")) > 0 {
			config.Insecure.Value = true
			config.Insecure.Null = false
		} else {
			insecure = false
			config.Insecure.Value = false
			config.Insecure.Null = false
		}
	}
	insecure = config.Insecure.Value

	// Create a new xsoar client and set it to the provider client
	openapiConfig := openapi.NewConfiguration()
	openapiConfig.Servers[0].URL = mainhost
	openapiConfig.AddDefaultHeader("Authorization", apikey)
	openapiConfig.AddDefaultHeader("Accept", "application/json,*/*")
	if config.HttpHeadersFromEnv != nil {
		for key, value := range config.HttpHeadersFromEnv {
			openapiConfig.AddDefaultHeader(key, os.Getenv(value))
		}
	}
	if insecure {
		tr := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		}
		client := &http.Client{Transport: tr}
		openapiConfig.HTTPClient = client
	}
	c := openapi.NewAPIClient(openapiConfig)

	p.client = c
	p.configured = true
	p.data = &config
}

// GetResources - Defines provider resources
func (p *provider) GetResources(_ context.Context) (map[string]tfsdk.ResourceType, diag.Diagnostics) {
	return map[string]tfsdk.ResourceType{
		"xsoar_account":              resourceAccountType{},
		"xsoar_ha_group":             resourceHAGroupType{},
		"xsoar_host":                 resourceHostType{},
		"xsoar_integration_instance": resourceIntegrationInstanceType{},
		"xsoar_classifier":           resourceClassifierType{},
		"xsoar_mapper":               resourceMapperType{},
	}, nil
}

// GetDataSources - Defines provider data sources
func (p *provider) GetDataSources(_ context.Context) (map[string]tfsdk.DataSourceType, diag.Diagnostics) {
	return map[string]tfsdk.DataSourceType{
		"xsoar_account":              dataSourceAccountType{},
		"xsoar_accounts":             dataSourceAccountsType{},
		"xsoar_ha_group":             dataSourceHAGroupType{},
		"xsoar_ha_groups":            dataSourceHAGroupsType{},
		"xsoar_host":                 dataSourceHostType{},
		"xsoar_integration_instance": dataSourceIntegrationInstanceType{},
		"xsoar_classifier":           dataSourceClassifierType{},
		"xsoar_mapper":               dataSourceMapperType{},
	}, nil
}
