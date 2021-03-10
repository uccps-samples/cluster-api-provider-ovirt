package providerIDcontroller

import (
	"context"
	"fmt"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/cloud/ovirt"
	common "github.com/openshift/cluster-api-provider-ovirt/pkg/cloud/ovirt/controllers"
	ovirtsdk "github.com/ovirt/go-ovirt"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/klog/klogr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

var _ reconcile.Reconciler = &providerIDReconciler{}

type providerIDReconciler struct {
	common.BaseController
	listNodesByFieldFunc func(key, value string) ([]corev1.Node, error)
	fetchProviderIDFunc  func(string) (string, error)
}

func (r *providerIDReconciler) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	r.Log.Info("Reconciling", "Node", request.NamespacedName)

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
	id, err := r.fetchProviderIDFunc(node.Name)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed getting VM from oVirt: %v", err)
	}
	if id == "" {
		// Node doesn't exist in oVirt platform, deleting node object
		r.Log.Info(
			"Deleting Node from cluster since it has been removed from the oVirt engine",
			"node", request.NamespacedName)
		return deleteNode(ctx, r.Client, &node)
	}
	if node.Spec.ProviderID != "" {
		// Node exist and providerID is set
		c, err := r.GetConnection(common.NAMESPACE, common.CREDENTIALS_SECRET)
		vmResponse, err := c.SystemService().VmsService().VmService(id).Get().Send()
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed getting VM from oVirt: %v", err)
		}
		if vmResponse.MustVm().MustStatus() == ovirtsdk.VMSTATUS_DOWN {
			r.Log.Info("Node VM status is Down, requeuing for 1 min",
				"Node", node.Name, "Vm Status", ovirtsdk.VMSTATUS_DOWN)
			return reconcile.Result{Requeue: true, RequeueAfter: common.RETRY_INTERVAL_VM_DOWN}, nil
		}
	} else {
		r.Log.Info("spec.ProviderID is empty, fetching from ovirt", "node", request.NamespacedName)
		node.Spec.ProviderID = ovirt.ProviderIDPrefix + id
		err = r.Client.Update(ctx, &node)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed updating node %s: %v", node.Name, err)
		}
	}
	return reconcile.Result{}, nil
}

func deleteNode(ctx context.Context, client client.Client, node *corev1.Node) (reconcile.Result, error) {
	if err := client.Delete(ctx, node); err != nil {
		return reconcile.Result{}, fmt.Errorf("Error deleting node: %v, error is: %v", node.Name, err)
	}
	return reconcile.Result{}, nil
}

func (r *providerIDReconciler) fetchOvirtVmID(nodeName string) (string, error) {
	c, err := r.GetConnection(common.NAMESPACE, common.CREDENTIALS_SECRET)
	if err != nil {
		return "", err
	}
	send, err := c.SystemService().VmsService().List().Search(fmt.Sprintf("name=%s", nodeName)).Send()
	if err != nil {
		r.Log.Error(err, "Error occurred will searching VM", "VM name", nodeName)
		return "", err
	}
	vms := send.MustVms().Slice()
	if l := len(vms); l > 1 {
		return "", fmt.Errorf("expected to get 1 VM but got %v", l)
	} else if l == 0 {
		return "", nil
	}
	return vms[0].MustId(), nil
}

func Add(mgr manager.Manager, opts manager.Options) error {
	reconciler, err := NewProviderIDReconciler(mgr)

	if err != nil {
		return fmt.Errorf("error building reconciler: %v", err)
	}

	c, err := controller.New("provdierID-controller", mgr, controller.Options{Reconciler: reconciler})
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

func NewProviderIDReconciler(mgr manager.Manager) (*providerIDReconciler, error) {
	log.SetLogger(klogr.New())
	r := providerIDReconciler{
		BaseController: common.BaseController{
			Log:    log.Log.WithName("controllers").WithName("providerID-reconciler"),
			Client: mgr.GetClient(),
		},
	}
	r.fetchProviderIDFunc = r.fetchOvirtVmID
	return &r, nil
}
