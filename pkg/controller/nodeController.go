package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/openshift/cluster-api-provider-ovirt/pkg/ovirt"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
	ovirtC "github.com/ovirt/go-ovirt-client/v2"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var _ reconcile.Reconciler = &nodeController{}

const (
	retryIntervalVMDownSec = 60
)

type nodeController struct {
	baseController
}

// Creates a new Node Controller.
func NewNodeController(k8sClient client.Client, cachedOVirtClient ovirt.CachedOVirtClient) *nodeController {
	return &nodeController{
		baseController: NewBaseController("NodeController", k8sClient, cachedOVirtClient),
	}
}

// Adds the Node Controller to the manager,
// The Node Controller watches changes on Node objects in the cluster.
func (ctrl *nodeController) AddToManager(mgr manager.Manager) error {
	c, err := controller.New(ctrl.Name, mgr, controller.Options{Reconciler: ctrl})
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

// Reconcile implements controller runtime Reconciler interface.
func (r *nodeController) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	r.Log.Infof("Reconciling node %s", request.NamespacedName)
	// Fetch the Node instance
	node := corev1.Node{}
	err := r.Client.Get(ctx, request.NamespacedName, &node)
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ResultNoRequeue(), nil
		}
		// Error reading the object - requeue the request.
		return ResultRequeueDefault(), errors.Wrap(err, "error getting node requeue")
	}
	// Check if the node has a ovirt ProviderID set, if not then ignore it
	if !strings.Contains(node.Spec.ProviderID, utils.ProviderIDPrefix) {
		return ResultNoRequeue(), nil
	}
	ovirtClient, err := r.GetoVirtClient()
	if err != nil {
		msg := "error getting connection to oVirt, requeuing"
		r.Log.Errorf(msg+": %v", err)
		return ResultRequeueDefault(), errors.Wrap(err, msg)
	}

	vm, err := ovirtClient.GetVMByName(node.Name)
	if err != nil {
		if ovirtC.HasErrorCode(err, ovirtC.ENotFound) {
			r.Log.Infof("Deleting Node %s from cluster since it has been removed from the oVirt engine", node.Name)
			if err := r.Client.Delete(ctx, &node); err != nil {
				return ResultRequeueDefault(), fmt.Errorf("error deleting node: %v, error: %w", node.Name, err)
			}
		}
		return ResultRequeueDefault(),
			fmt.Errorf("failed getting VM %s from oVirt, requeue: %w", node.Name, err)
	} else if vm.Status() == ovirtC.VMStatusDown {
		r.Log.Infof("Node %s VM status is Down, requeuing for 1 min", node.Name)
		return ResultRequeueAfter(retryIntervalVMDownSec), nil
	}
	return ResultNoRequeue(), nil
}
