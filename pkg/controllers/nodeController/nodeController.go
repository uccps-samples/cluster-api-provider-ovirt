package nodeController

import (
	"context"
	"fmt"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
	"k8s.io/klog/klogr"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"strings"
	"time"

	common "github.com/openshift/cluster-api-provider-ovirt/pkg/controllers"
	ovirtsdk "github.com/ovirt/go-ovirt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var _ reconcile.Reconciler = &nodeController{}

const RETRY_INTERVAL_VM_DOWN = 60 * time.Second

type nodeController struct {
	common.BaseController
}

func (r *nodeController) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	r.Log.Info("NodeController, Reconciling:", "Node", request.NamespacedName)
	// Fetch the Node instance
	node := corev1.Node{}
	err := r.Client.Get(ctx, request.NamespacedName, &node)
	if err != nil {
		if errors.IsNotFound(err) {
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
	vm, err := r.getVmByName(node.Name)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed getting VM from oVirt: %v", err)
	} else if vm == nil {
		// Node doesn't exist in oVirt platform, deleting node object
		r.Log.Info(
			"Deleting Node from cluster since it has been removed from the oVirt engine",
			"node", node.Name)
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

func (r *nodeController) getVmByName(name string) (*ovirtsdk.Vm, error) {
	c, err := r.GetConnection(common.NAMESPACE, common.CREDENTIALS_SECRET)
	if err != nil {
		return nil, err
	}
	send, err := c.SystemService().VmsService().List().Search(fmt.Sprintf("name=%s", name)).Send()
	if err != nil {
		r.Log.Error(err, "Error occurred will searching VM", "VM name", name)
		return nil, err
	}
	vms := send.MustVms().Slice()
	if l := len(vms); l > 1 {
		return nil, fmt.Errorf("expected to get 1 VM but got %v", l)
	} else if l == 0 {
		return nil, nil
	}
	return vms[0], nil
}

func Add(mgr manager.Manager, opts manager.Options) error {
	nc, err := NewNodeController(mgr)

	if err != nil {
		return fmt.Errorf("error building reconciler: %v", err)
	}

	c, err := controller.New("NodeController", mgr, controller.Options{Reconciler: nc})
	if err != nil {
		return err
	}

	//Watch node changes
	err = c.Watch(&source.Kind{Type: &corev1.Node{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

func NewNodeController(mgr manager.Manager) (*nodeController, error) {
	log.SetLogger(klogr.New())
	return &nodeController{
		BaseController: common.BaseController{
			Log:    log.Log.WithName("controllers").WithName("nodeController"),
			Client: mgr.GetClient(),
		},
	}, nil
}
