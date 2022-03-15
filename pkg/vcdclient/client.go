/*
   Copyright 2021 VMware, Inc.
   SPDX-License-Identifier: Apache-2.0
*/

package vcdclient

import (
	"crypto/tls"
	"fmt"
	swaggerClient "github.com/vmware/cloud-director-named-disk-csi-driver/pkg/vcdswaggerclient"
	"k8s.io/klog"
	"net/http"
	"sync"

	"github.com/vmware/go-vcloud-director/v2/govcd"
)

var (
	clientCreatorLock sync.Mutex
	clientSingleton   *Client = nil
)

// Client :
type Client struct {
	vcdAuthConfig   *VCDAuthConfig
	ClusterOrgName  string
	ClusterOVDCName string
	ClusterVAppName string
	vcdClient       *govcd.VCDClient
	vdc             *govcd.Vdc
	ClusterID       string
	rwLock          sync.RWMutex
	apiClient       *swaggerClient.APIClient
}

func (client *Client) RefreshBearerToken() error {
	klog.Infof("Refreshing vcd client")

	href := fmt.Sprintf("%s/api", client.vcdAuthConfig.Host)
	client.vcdClient.Client.APIVersion = VCloudApiVersion

	klog.Infof("Is user sysadmin: [%v]", client.vcdAuthConfig.IsSysAdmin)
	if client.vcdAuthConfig.RefreshToken != "" {
		userOrg := client.vcdAuthConfig.UserOrg
		if client.vcdAuthConfig.IsSysAdmin {
			userOrg = "system"
		}
		// Refresh vcd client using refresh token as system org user
		err := client.vcdClient.SetToken(userOrg,
			govcd.ApiTokenHeader, client.vcdAuthConfig.RefreshToken)
		if err != nil {
			return fmt.Errorf("failed to refresh VCD client with refresh token: [%v]", err)
		}
	} else if client.vcdAuthConfig.User != "" && client.vcdAuthConfig.Password != "" {
		// Refresh vcd client using username and password
		resp, err := client.vcdClient.GetAuthResponse(client.vcdAuthConfig.User, client.vcdAuthConfig.Password,
			client.vcdAuthConfig.UserOrg)
		if err != nil {
			return fmt.Errorf("unable to authenticate [%s/%s] for url [%s]: [%+v] : [%v]",
				client.vcdAuthConfig.UserOrg, client.vcdAuthConfig.User, href, resp, err)
		}
	} else {
		return fmt.Errorf(
			"unable to find refresh token or secret to refresh vcd client for user [%s/%s] and url [%s]",
			client.vcdAuthConfig.UserOrg, client.vcdAuthConfig.User, href)
	}

	// reset legacy client
	org, err := client.vcdClient.GetOrgByNameOrId(client.ClusterOrgName)
	if err != nil {
		return fmt.Errorf("unable to get vcd organization [%s]: [%v]",
			client.ClusterOrgName, err)
	}

	vdc, err := org.GetVDCByName(client.ClusterOVDCName, true)
	if err != nil {
		return fmt.Errorf("unable to get vdc from org [%s], vdc [%s]: [%v]",
			client.ClusterOrgName, client.vcdAuthConfig.VDC, err)
	}
	client.vdc = vdc

	// reset swagger client
	swaggerConfig := swaggerClient.NewConfiguration()
	swaggerConfig.BasePath = fmt.Sprintf("%s/cloudapi", client.vcdAuthConfig.Host)
	swaggerConfig.AddDefaultHeader("Authorization", fmt.Sprintf("Bearer %s", client.vcdClient.Client.VCDToken))
	swaggerConfig.HTTPClient = &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: client.vcdAuthConfig.Insecure},
		},
	}
	client.apiClient = swaggerClient.NewAPIClient(swaggerConfig)

	klog.Info("successfully refreshed all clients")
	return nil
}

// NewVCDClientFromSecrets :
func NewVCDClientFromSecrets(host string, orgName string, vdcName string, vAppName string,
	userOrgName string, user string, password string, refreshToken string, insecure bool,
	clusterID string, getVdcClient bool) (*Client, error) {

	// TODO: validation of parameters

	clientCreatorLock.Lock()
	defer clientCreatorLock.Unlock()

	// Return old client if everything matches. Else create new one and cache it.
	// This is suboptimal but is not a common case.
	if clientSingleton != nil {
		if clientSingleton.vcdAuthConfig.Host == host &&
			clientSingleton.ClusterOrgName == orgName &&
			clientSingleton.vcdAuthConfig.VDC == vdcName &&
			clientSingleton.ClusterVAppName == vAppName &&
			clientSingleton.vcdAuthConfig.UserOrg == userOrgName &&
			clientSingleton.vcdAuthConfig.User == user &&
			clientSingleton.vcdAuthConfig.Password == password &&
			clientSingleton.vcdAuthConfig.Insecure == insecure {
			return clientSingleton, nil
		}
	}

	vcdAuthConfig := NewVCDAuthConfigFromSecrets(host, user, password, refreshToken, userOrgName, insecure)

	// Get API client
	vcdClient, apiClient, err := vcdAuthConfig.GetSwaggerClientFromSecrets()
	if err != nil {
		return nil, fmt.Errorf("unable to get swagger client from secrets: [%v]", err)
	}

	client := &Client{
		vcdAuthConfig:   vcdAuthConfig,
		ClusterOrgName:  orgName,
		ClusterOVDCName: vdcName,
		ClusterVAppName: vAppName,
		vcdClient:       vcdClient,
		ClusterID:       clusterID,
		apiClient:       apiClient,
	}

	if getVdcClient {
		org, err := vcdClient.GetOrgByName(orgName)
		if err != nil {
			return nil, fmt.Errorf("unable to get org from name [%s]: [%v]", orgName, err)
		}

		client.vdc, err = org.GetVDCByName(vdcName, true)
		if err != nil {
			return nil, fmt.Errorf("unable to get vdc [%s] from org [%s]: [%v]", vdcName, orgName, err)
		}
	}
	client.vcdClient = vcdClient

	clientSingleton = client

	return clientSingleton, nil
}