package nodeController

import (
	"context"
	"fmt"
	"strings"

	ovirtClient "github.com/openshift/cluster-api-provider-ovirt/pkg/clients/ovirt"
	common "github.com/openshift/cluster-api-provider-ovirt/pkg/controllers"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
	ovirtsdk "github.com/ovirt/go-ovirt"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/klogr"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var _ reconcile.Reconciler = &nodeController{}

const (
	retryIntervalVMDownSec = 60
	controllerName         = "NodeController"
)

type nodeController struct {
	common.BaseController
}

// Reconcile implements controller runtime Reconciler interface.
func (r *nodeController) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	r.Log.Info("NodeController, Reconciling:", "Node", request.NamespacedName)
	// Fetch the Node instance
	node := corev1.Node{}
	err := r.Client.Get(ctx, request.NamespacedName, &node)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, fmt.Errorf("error getting node: %v", err)
	}
	if !strings.Contains(node.Spec.ProviderID, utils.ProviderIDPrefix) {
		return reconcile.Result{}, nil
	}
	c, err := r.GetConnection()
	if err != nil {
		return reconcile.Result{}, err
	}
	ovirtC := ovirtClient.NewOvirtClient(c)
	vm, err := ovirtC.GetVMByName(node.Name)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed getting VM from oVirt: %v", err)
	} else if vm == nil {
		// Node doesn't exist in oVirt platform, deleting node object
		r.Log.Info(
			"Deleting Node from cluster since it has been removed from the oVirt engine",
			"Node", node.Name)
		if err := r.Client.Delete(ctx, &node); err != nil {
			return reconcile.Result{}, fmt.Errorf("Error deleting node: %v, error is: %v", node.Name, err)
		}
	} else if vm.MustStatus() == ovirtsdk.VMSTATUS_DOWN {
		r.Log.Info("Node VM status is Down, requeuing for 1 min",
			"Node", node.Name, "Vm Status", ovirtsdk.VMSTATUS_DOWN)
		return reconcile.Result{Requeue: true, RequeueAfter: RETRY_INTERVAL_VM_DOWN}, nil
	}
	return reconcile.Result{}, nil
}

// Creates a new Node Controller and adds it to the manager
// The Node Controller watches changes on Node objects in the cluster
func Add(mgr manager.Manager) error {
	nc := NewNodeController(mgr)
	c, err := controller.New(controllerName, mgr, controller.Options{Reconciler: nc})
	if err != nil {
		return errors.Wrap(err, "error creating node controller")
	}
	//Watch node changes
	err = c.Watch(&source.Kind{Type: &corev1.Node{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return errors.Wrap(err, "error setting up watch on node changes")
	}

	return nil
}

func NewNodeController(mgr manager.Manager) *nodeController {
	log.SetLogger(klogr.New())
	return &nodeController{
		BaseController: common.BaseController{
			Log:    log.Log.WithName("controllers").WithName(controllerName),
			Client: mgr.GetClient(),
		},
	}
}
