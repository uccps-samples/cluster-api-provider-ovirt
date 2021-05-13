package ovirt

import (
	"fmt"
	ovirtClient "github.com/openshift/cluster-api-provider-ovirt/pkg/clients/ovirt"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"

	"github.com/go-logr/logr"
	ovirtsdk "github.com/ovirt/go-ovirt"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type BaseController struct {
	Log             logr.Logger
	Client          client.Client
	ovirtConnection *ovirtsdk.Connection
}

func (b *BaseController) GetConnection() (*ovirtsdk.Connection, error) {
	var err error
	if b.ovirtConnection == nil || b.ovirtConnection.Test() != nil {
		creds, err := ovirtClient.GetCredentialsSecret(b.Client, utils.NAMESPACE, utils.OvirtCloudCredsSecretName)
		if err != nil {
			return nil, fmt.Errorf("failed getting credentials for namespace %s, %s", utils.NAMESPACE, err)
		}
		// session expired or some other error, re-login.
		b.ovirtConnection, err = ovirtClient.CreateApiConnection(creds)
	}
	return b.ovirtConnection, err
}
