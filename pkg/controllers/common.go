package ovirt

import (
	"fmt"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
	kloglogger "github.com/ovirt/go-ovirt-client-log-klog/v2"
	ovirtclient "github.com/ovirt/go-ovirt-client/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const requeueDefaultTime = 30 * time.Second

func ResultRequeueAfter(sec int) reconcile.Result {
	return reconcile.Result{RequeueAfter: time.Duration(sec) * time.Second}
}

func ResultRequeueDefault() reconcile.Result {
	return reconcile.Result{RequeueAfter: requeueDefaultTime}
}

func ResultNoRequeue() reconcile.Result {
	return reconcile.Result{Requeue: false}
}

type BaseController struct {
	Log                logr.Logger
	Client             client.Client
	OVirtClientFactory OVirtClientFactory
}

// GetoVirtClient returns a a client to oVirt's API endpoint
func (b *BaseController) GetoVirtClient() (ovirtclient.Client, error) {
	return b.OVirtClientFactory.GetOVirtClient()
}

type OVirtClientFactory interface {
	GetOVirtClient() (ovirtclient.Client, error)
}

type oVirtClientFactory struct {
	oVirtClient ovirtclient.Client
	create      CreateOVirtClientFunc

	k8sClient client.Client
}

type CreateOVirtClientFunc func(creds *Creds) (ovirtclient.Client, error)

func NewOvirtClientFactory(k8sClient client.Client, create CreateOVirtClientFunc) *oVirtClientFactory {
	return &oVirtClientFactory{
		oVirtClient: nil,
		create:      create,
		k8sClient:   k8sClient,
	}
}

func (factory *oVirtClientFactory) GetOVirtClient() (ovirtclient.Client, error) {
	// check if session expired or some other error occured, try re-login
	if !factory.isConnected() {
		creds, err := factory.fetchCredentials()
		if err != nil {
			return nil, err
		}

		factory.oVirtClient, err = factory.create(creds)
		if err != nil {
			return nil, fmt.Errorf("failed creating ovirt connection %w", err)
		}
	}
	return factory.oVirtClient, nil
}

func (factory *oVirtClientFactory) isConnected() bool {
	return factory.oVirtClient != nil && factory.oVirtClient.Test() == nil
}

func (factory *oVirtClientFactory) fetchCredentials() (*Creds, error) {
	creds, err := GetCredentialsSecret(factory.k8sClient, utils.NAMESPACE, utils.OvirtCloudCredsSecretName)
	if err != nil {
		return nil, fmt.Errorf("failed getting credentials for namespace %s, %w", utils.NAMESPACE, err)
	}
	return creds, nil
}

func CreateNewOVirtClient(creds *Creds) (ovirtclient.Client, error) {
	tls := ovirtclient.TLS()
	if creds.Insecure {
		tls.Insecure()
	} else {
		if creds.CAFile != "" {
			tls.CACertsFromFile(creds.CAFile)
		}
		if creds.CABundle != "" {
			tls.CACertsFromMemory([]byte(creds.CABundle))
		}
		tls.CACertsFromSystem()
	}
	return ovirtclient.New(
		creds.URL,
		creds.Username,
		creds.Password,
		tls,
		kloglogger.New(),
		nil,
	)
}
