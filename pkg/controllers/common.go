package ovirt

import (
	"fmt"
	ovirtclient "github.com/ovirt/go-ovirt-client"
	kloglogger "github.com/ovirt/go-ovirt-client-log-klog"
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
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
	Log         logr.Logger
	Client      client.Client
	ovirtClient ovirtclient.Client
}

// getClient returns a a client to oVirt's API endpoint
func (b *BaseController) GetoVirtClient() (ovirtclient.Client, error) {
	if b.ovirtClient == nil || b.ovirtClient.Test() != nil {
		var err error
		// session expired or some other error, re-login.
		b.ovirtClient, err = GetoVirtClient(b.Client)
		if err != nil {
			return nil, fmt.Errorf("failed creating ovirt connection %w", err)
		}
	}
	return b.ovirtClient, nil
}

// NewClient returns a ovirt client  by using the given credentials
func GetoVirtClient(k8sClient client.Client) (ovirtclient.Client, error) {
	creds, err := GetCredentialsSecret(k8sClient, utils.NAMESPACE, utils.OvirtCloudCredsSecretName)
	if err != nil {
		return nil, fmt.Errorf("failed getting credentials for namespace %s, %w", utils.NAMESPACE, err)
	}

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
	logger := kloglogger.New()
	return ovirtclient.New(
		creds.URL,
		creds.Username,
		creds.Password,
		tls,
		logger,
		nil,
	)
}
