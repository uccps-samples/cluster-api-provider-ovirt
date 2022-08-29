package controller

import (
	"time"

	"github.com/go-logr/logr"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/ovirt"
	ovirtclient "github.com/ovirt/go-ovirt-client/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type baseController struct {
	Name string
	Log  logr.Logger

	Client             client.Client
	OVirtClientFactory ovirt.OVirtClientFactory
}

// GetoVirtClient returns a client to oVirt's API endpoint
func (b *baseController) GetoVirtClient() (ovirtclient.Client, error) {
	return b.OVirtClientFactory.GetOVirtClient()
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
