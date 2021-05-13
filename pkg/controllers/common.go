package ovirt

import (
	"fmt"
	ovirtClient "github.com/openshift/cluster-api-provider-ovirt/pkg/clients/ovirt"

	"github.com/go-logr/logr"
	ovirtsdk "github.com/ovirt/go-ovirt"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	NAMESPACE          = "openshift-machine-api"
	CREDENTIALS_SECRET = "ovirt-credentials"
)

type BaseController struct {
	Log             logr.Logger
	Client          client.Client
	ovirtConnection *ovirtsdk.Connection
}

func (b *BaseController) GetConnection(namespace, secretName string) (*ovirtsdk.Connection, error) {
	var err error
	if b.ovirtConnection == nil || b.ovirtConnection.Test() != nil {
		creds, err := ovirtClient.GetCredentialsSecret(b.Client, namespace, secretName)
		if err != nil {
			return nil, fmt.Errorf("failed getting credentials for namespace %s, %s", namespace, err)
		}
		// session expired or some other error, re-login.
		b.ovirtConnection, err = ovirtClient.CreateApiConnection(creds, namespace, secretName)
	}
	return b.ovirtConnection, err
}
