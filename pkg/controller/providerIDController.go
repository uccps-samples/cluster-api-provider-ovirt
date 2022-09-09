package controller

import (
	"context"
	"fmt"

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

var _ reconcile.Reconciler = &providerIDController{}

type providerIDController struct {
	baseController
}

// Creates a new ProviderID Controller.
func NewProviderIDController(k8sClient client.Client, cachedOVirtClient ovirt.CachedOVirtClient) *providerIDController {
	return &providerIDController{
		baseController: NewBaseController("ProviderIDController", k8sClient, cachedOVirtClient),
	}
}

// Adds the ProviderID Controller to the manager.
// The ProviderID Controller watches changes on Node objects in the cluster.
func (ctrl *providerIDController) AddToManager(mgr manager.Manager) error {
	c, err := controller.New(ctrl.Name, mgr, controller.Options{Reconciler: ctrl})
	if err != nil {
		return errors.Wrap(err, "error setting up watch on node changes")
	}

	//Watch node changes
	err = c.Watch(&source.Kind{Type: &corev1.Node{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}
	return nil
}

// Reconcile implements controller runtime Reconciler interface.
func (r *providerIDController) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
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
		return ResultRequeueDefault(), errors.Wrap(err, "error getting node: %v")
	}
	if node.Spec.ProviderID == "" {
		r.Log.Infof("spec.ProviderID for Node %s is empty, fetching from ovirt", node.Name)
		id, err := r.fetchOvirtVmID(node.Name)
		if err != nil {
			return ResultRequeueDefault(),
				fmt.Errorf("failed getting VM %s from oVirt requeue: %w", node.Name, err)
		}
		if id == "" {
			r.Log.Infof("Node %s not found in oVirt", node.Name)
			return ResultNoRequeue(), nil
		}
		node.Spec.ProviderID = utils.ProviderIDPrefix + id
		err = r.Client.Update(ctx, &node)
		if err != nil {
			return ResultRequeueDefault(), fmt.Errorf("failed updating node %s: %w", node.Name, err)
		}
	}
	return ResultNoRequeue(), nil
}

// fetchOvirtVmID returns the id of the oVirt VM which correlates to the node
func (r *providerIDController) fetchOvirtVmID(nodeName string) (string, error) {
	ovirtclient, err := r.GetoVirtClient()
	if err != nil {
		return "", errors.Wrap(err, "error getting connection to oVirt")
	}

	vm, err := ovirtclient.GetVMByName(nodeName)
	if err != nil {
		if ovirtC.HasErrorCode(err, ovirtC.ENotFound) {
			return "", nil
		}
		r.Log.Errorf("Error occurred while searching for VM '%s': %v", nodeName, err)
		return "", fmt.Errorf("failed getting VM %s from oVirt: %w", nodeName, err)
	}

	return string(vm.ID()), nil
}
