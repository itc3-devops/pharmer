package credential

import (
	"fmt"
	"time"

	aauthz "github.com/Azure/azure-sdk-for-go/arm/authorization"
	"github.com/Azure/azure-sdk-for-go/arm/graphrbac"
	"github.com/Azure/azure-sdk-for-go/arm/resources/subscriptions"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	azdate "github.com/Azure/go-autorest/autorest/date"
	api "github.com/appscode/api/credential/v1beta1"
	"github.com/appscode/appctl/pkg/config"
	"github.com/appscode/appctl/pkg/util"
	"github.com/appscode/go-term"
	"github.com/appscode/go/types"
	"github.com/cenkalti/backoff"
	"github.com/pborman/uuid"
)

const (
	azureNativeApplicationID = "a6fa51f3-f8b6-4eb5-833a-58a706552eae"
	azureTenantID            = "772268e5-d940-4bf6-be82-1c4a09a67f5d"
)

func getSptFromDeviceFlow(oauthConfig adal.OAuthConfig, clientID, resource string) (*adal.ServicePrincipalToken, error) {
	oauthClient := &autorest.Client{}
	deviceCode, err := adal.InitiateDeviceAuth(oauthClient, oauthConfig, clientID, resource)
	if err != nil {
		return nil, fmt.Errorf("Failed to start device auth flow: %s", err)
	}
	fmt.Println(*deviceCode.Message)

	token, err := adal.WaitForUserCompletion(oauthClient, deviceCode)
	if err != nil {
		return nil, fmt.Errorf("Failed to finish device auth flow: %s", err)
	}

	spt, err := adal.NewServicePrincipalTokenFromManualToken(
		oauthConfig,
		clientID,
		resource,
		*token)
	if err != nil {
		return nil, fmt.Errorf("Failed to get oauth token from device flow: %v", err)
	}
	return spt, nil
}

func CreateAzureCredential(req *api.CredentialCreateRequest) {
	apiReq = req

	oauthConfig, err := adal.NewOAuthConfig(azure.PublicCloud.ActiveDirectoryEndpoint, azureTenantID)
	term.ExitOnError(err)

	spt, err := getSptFromDeviceFlow(*oauthConfig, azureNativeApplicationID, azure.PublicCloud.ServiceManagementEndpoint)
	if err != nil {
		term.Fatalln("Failed to retrieve token:", err)
	}

	client := autorest.NewClientWithUserAgent(subscriptions.UserAgent())
	client.Authorizer = autorest.NewBearerAuthorizer(spt)

	tenantsClient := subscriptions.TenantsClient{
		ManagementClient: subscriptions.ManagementClient{
			Client:  client,
			BaseURI: subscriptions.DefaultBaseURI,
		},
	}
	tenants, err := tenantsClient.List()
	term.ExitOnError(err)
	tenantID := types.String((*tenants.Value)[0].TenantID)

	userOauthConfig, err := adal.NewOAuthConfig(azure.PublicCloud.ActiveDirectoryEndpoint, tenantID)
	term.ExitOnError(err)

	userSpt, err := adal.NewServicePrincipalTokenFromManualToken(
		*userOauthConfig,
		azureNativeApplicationID,
		azure.PublicCloud.ServiceManagementEndpoint,
		spt.Token)
	term.ExitOnError(err)

	err = userSpt.RefreshExchange(azure.PublicCloud.ServiceManagementEndpoint)
	term.ExitOnError(err)

	userClient := autorest.NewClientWithUserAgent(subscriptions.UserAgent())
	userClient.Authorizer = autorest.NewBearerAuthorizer(userSpt)

	userSubsClient := subscriptions.GroupClient{
		ManagementClient: subscriptions.ManagementClient{
			Client:  userClient,
			BaseURI: subscriptions.DefaultBaseURI,
		},
	}
	subs, err := userSubsClient.List()
	term.ExitOnError(err)
	subscriptionID := types.String((*subs.Value)[0].SubscriptionID)

	graphSpt, err := adal.NewServicePrincipalTokenFromManualToken(
		*userOauthConfig,
		azureNativeApplicationID,
		azure.PublicCloud.GraphEndpoint,
		userSpt.Token)
	term.ExitOnError(err)

	err = graphSpt.RefreshExchange(azure.PublicCloud.GraphEndpoint)
	term.ExitOnError(err)

	graphClient := autorest.NewClientWithUserAgent(graphrbac.UserAgent())
	graphClient.Authorizer = autorest.NewBearerAuthorizer(graphSpt)

	clientSecret := uuid.NewRandom().String()
	start := azdate.Time{Time: time.Now()}
	end := azdate.Time{Time: time.Now().Add(365 * 24 * time.Hour)}
	cred := graphrbac.PasswordCredential{
		StartDate: &start,
		EndDate:   &end,
		Value:     types.StringP(clientSecret),
	}

	appClient := graphrbac.ApplicationsClient{
		ManagementClient: graphrbac.ManagementClient{
			Client:   graphClient,
			BaseURI:  graphrbac.DefaultBaseURI,
			TenantID: tenantID,
		},
	}

	app, err := appClient.Create(graphrbac.ApplicationCreateParameters{
		AvailableToOtherTenants: types.FalseP(),
		DisplayName:             types.StringP(req.Name),
		Homepage:                types.StringP("http://" + req.Name),
		IdentifierUris:          &[]string{"http://" + req.Name},
		PasswordCredentials:     &[]graphrbac.PasswordCredential{cred},
	})
	term.ExitOnError(err)
	clientID := *app.AppID

	spClient := graphrbac.ServicePrincipalsClient{
		ManagementClient: graphrbac.ManagementClient{
			Client:   graphClient,
			BaseURI:  graphrbac.DefaultBaseURI,
			TenantID: tenantID,
		},
	}
	sp, err := spClient.Create(graphrbac.ServicePrincipalCreateParameters{
		AppID:          types.StringP(clientID),
		AccountEnabled: types.TrueP(),
	})
	term.ExitOnError(err)

	roleDefClient := aauthz.RoleDefinitionsClient{
		ManagementClient: aauthz.ManagementClient{
			Client:         userClient,
			BaseURI:        aauthz.DefaultBaseURI,
			SubscriptionID: subscriptionID,
		},
	}

	roles, err := roleDefClient.List("subscriptions/"+subscriptionID, `roleName eq 'Contributor'`)
	term.ExitOnError(err)
	if len(*roles.Value) == 0 {
		term.Fatalln("Can't find Contributor role.")
	}
	roleID := (*roles.Value)[0].ID

	roleAssignClient := aauthz.RoleAssignmentsClient{
		ManagementClient: aauthz.ManagementClient{
			Client:         userClient,
			BaseURI:        aauthz.DefaultBaseURI,
			SubscriptionID: subscriptionID,
		},
	}

	backoff.Retry(func() error {
		roleAssignmentName := uuid.NewRandom().String()
		_, err := roleAssignClient.Create("subscriptions/"+subscriptionID, roleAssignmentName, aauthz.RoleAssignmentCreateParameters{
			Properties: &aauthz.RoleAssignmentProperties{
				PrincipalID:      sp.ObjectID,
				RoleDefinitionID: roleID,
			},
		})
		return err
	}, backoff.NewExponentialBackOff())

	apiReq.Data = map[string]string{
		"subscription_id": subscriptionID,
		"tenant_id":       tenantID,
		"client_id":       clientID,
		"client_secret":   clientSecret,
	}
	c := config.ClientOrDie()
	_, err = c.CloudCredential().Create(c.Context(), apiReq)
	util.PrintStatus(err)
}
