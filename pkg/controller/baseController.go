package controller

import (
	"time"

	"github.com/openshift/cluster-api-provider-ovirt/pkg/ovirt"
	ovirtclient "github.com/ovirt/go-ovirt-client/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type baseController struct {
	Name string
	Log  *ovirt.KLogr

	Client            client.Client
	CachedOVirtClient ovirt.CachedOVirtClient
}

func NewBaseController(ctrlName string, k8sClient client.Client, cachedOVirtClient ovirt.CachedOVirtClient) baseController {
	return baseController{
		Name: ctrlName,
		Log:  ovirt.NewKLogr("controllers", ctrlName).WithVInfo(0),

		Client:            k8sClient,
		CachedOVirtClient: cachedOVirtClient,
	}
}

// GetoVirtClient returns a client to oVirt's API endpoint
func (b *baseController) GetoVirtClient() (ovirtclient.Client, error) {
	return b.CachedOVirtClient.Get()
}

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
