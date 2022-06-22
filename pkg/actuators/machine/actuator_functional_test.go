//go:build functional

package machine_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	machinev1 "github.com/openshift/api/machine/v1beta1"
	ovirtclientlog "github.com/ovirt/go-ovirt-client-log/v3"
	ovirtclient "github.com/ovirt/go-ovirt-client/v2"
	k8sCorev1 "k8s.io/api/core/v1"
	k8sMetav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	actuator "github.com/openshift/cluster-api-provider-ovirt/pkg/actuators/machine"
	capoV1Beta1 "github.com/openshift/cluster-api-provider-ovirt/pkg/apis/ovirtprovider/v1beta1"
	ovirt "github.com/openshift/cluster-api-provider-ovirt/pkg/controllers"
)

func init() {
	machinev1.AddToScheme(scheme.Scheme)
}

func TestActuator(t *testing.T) {
	cfg, stopEnv := setupTestEnv(t)
	defer stopEnv()

	mgr, cancel := setupCtrlManager(t, cfg)
	defer cancel()

	k8sClient := mgr.GetClient()
	const namespace = "openshift-machine-api"
	k8sComponentCleanup := setupK8sComponents(t, k8sClient, namespace)
	defer k8sComponentCleanup()

	ctx := context.Background()
	testcases := []struct {
		name string

		setup   func(ovirtSpec *capoV1Beta1.OvirtMachineProviderSpec, templateVMParams ovirtclient.BuildableVMParameters)
		execute func(actuator *actuator.OvirtActuator, machine *machinev1.Machine)
		verify  func(inputSpec *machinev1.Machine, createdVM ovirtclient.VM, helper ovirtclient.TestHelper)
	}{
		{
			name: "create VM should succeed",
			setup: func(ovirtSpec *capoV1Beta1.OvirtMachineProviderSpec, templateVMParams ovirtclient.BuildableVMParameters) {
			},
			execute: func(actuator *actuator.OvirtActuator, machine *machinev1.Machine) {
				err := actuator.Create(ctx, machine)
				if err != nil {
					t.Fatalf("Unexpected error occurred while calling actuator create: %v", err)
				}
			},
			verify: func(inputSpec *machinev1.Machine, createdVM ovirtclient.VM, helper ovirtclient.TestHelper) {
				if createdVM.Status() != ovirtclient.VMStatusDown {
					t.Errorf("Expected vm status to be %s, but got %s", ovirtclient.VMStatusDown, createdVM.Status())
				}
				if createdVM.ClusterID() != helper.GetClusterID() {
					t.Errorf("Expected clusterID to be %s, but got %s", helper.GetClusterID(), createdVM.ClusterID())
				}
				expectedMemory := int64(16348 * 1024 * 1024)
				if createdVM.Memory() != expectedMemory {
					t.Errorf("Expected memory to be %d, but got %d", expectedMemory, createdVM.Memory())
				}
				if createdVM.VMType() != ovirtclient.VMTypeServer {
					t.Errorf("Expected vm type to be %s, but got %s", ovirtclient.VMTypeServer, createdVM.VMType())
				}
				if createdVM.HugePages() != nil {
					t.Errorf("Expected hugepages to be <nil>, but got %v", createdVM.HugePages())
				}
				if createdVM.CPU().Topo().Cores() != 4 {
					t.Errorf("Expected cpu cores to be %d, but got %d", 4, createdVM.CPU().Topo().Cores())
				}
				if createdVM.CPU().Topo().Threads() != 1 {
					t.Errorf("Expected cpu threads to be %d, but got %d", 1, createdVM.CPU().Topo().Threads())
				}
				if createdVM.CPU().Topo().Sockets() != 1 {
					t.Errorf("Expected cpu sockets to be %d, but got %d", 1, createdVM.CPU().Topo().Sockets())
				}
				expectedGuaranteedMemory := int64(10000 * 1024 * 1024)
				if createdVM.MemoryPolicy().Guaranteed() == nil || *createdVM.MemoryPolicy().Guaranteed() != expectedGuaranteedMemory {
					t.Errorf("Expected guaranteed memory to be %d, but got %v", expectedGuaranteedMemory, createdVM.MemoryPolicy().Guaranteed())
				}
			},
		},
		{
			name: "create high_performance VM",
			setup: func(ovirtSpec *capoV1Beta1.OvirtMachineProviderSpec, templateVMParams ovirtclient.BuildableVMParameters) {
				ovirtSpec.VMType = "high_performance"
			},
			execute: func(actuator *actuator.OvirtActuator, machine *machinev1.Machine) {
				err := actuator.Create(ctx, machine)
				if err != nil {
					t.Fatalf("Unexpected error occurred while calling actuator create: %v", err)
				}
			},
			verify: func(inputSpec *machinev1.Machine, createdVM ovirtclient.VM, helper ovirtclient.TestHelper) {
				// check soundcard
				if createdVM.SoundcardEnabled() {
					t.Errorf("Expected soundcard to be disabled for high performance VM")
				}

				// check headless mode - no graphics consoles == headless
				graphicsConsoles, err := createdVM.ListGraphicsConsoles()
				if err != nil {
					t.Errorf("Unexpected error getting graphics consoles: %v", err)
				}
				if len(graphicsConsoles) > 0 {
					t.Errorf("Expected headless mode, but found %d graphics consoles", len(graphicsConsoles))
				}

				// check serial console
				if !createdVM.SerialConsole() {
					t.Errorf("Expected serial console to be enabled for high performance VM")
				}

				placementPolicy, placementPolicyExists := createdVM.PlacementPolicy()
				if !placementPolicyExists {
					t.Fatal("Expected placement policy to be set")
				}
				if placementPolicy.Affinity() == nil {
					t.Fatal("Expected affinity of placement policy to be set")
				}
				if *placementPolicy.Affinity() != ovirtclient.VMAffinityUserMigratable {
					t.Errorf("Expected affinity of placement policy to be %s, but got %s",
						ovirtclient.VMAffinityUserMigratable,
						*placementPolicy.Affinity(),
					)
				}

				// check CPU mode
				if createdVM.CPU().Mode() == nil {
					t.Fatal("Expected CPU mode not to be nil")
				}
				if *createdVM.CPU().Mode() != ovirtclient.CPUModeHostPassthrough {
					t.Errorf("Expected CPU mode to be Host Pass-Through for high performance VM")
				}

				// check memory ballooning
				if createdVM.MemoryPolicy().Ballooning() {
					t.Errorf("Expected memory ballooning to be disabled for high performance VM")

				}
			},
		},
	}

	for _, testcase := range testcases {
		t.Run(testcase.name, func(t *testing.T) {
			helper, err := ovirtclient.NewMockTestHelper(ovirtclientlog.NewTestLogger(t))
			if err != nil {
				t.Fatalf("Unexpected error occurred setting up test helper: %v", err)
			}

			templateName := "ovirt14-vhd9b-rhcos"
			ovirtSpec := basicOVirtSpec(templateName, string(helper.GetClusterID()))
			templateVMCreateParams := ovirtclient.NewCreateVMParams()
			testcase.setup(ovirtSpec, templateVMCreateParams)

			setupVMTemplate(t, helper, templateName, templateVMCreateParams)
			ovirtSpecRaw, err := capoV1Beta1.RawExtensionFromProviderSpec(ovirtSpec)
			if err != nil {
				t.Fatalf("Unexpected error occurred while parsing oVirtSpec: %v", err)
			}

			machine := basicMachineWithoutSpec(namespace)
			machine.Spec.ProviderSpec.Value = ovirtSpecRaw
			err = k8sClient.Create(ctx, machine)
			if err != nil {
				t.Fatalf("Unexpected error occurred while creating machine: %v", err)
			}
			defer func() {
				err = k8sClient.Delete(ctx, machine)
				if err != nil {
					t.Errorf("Unexpected error occurred while deleting machine: %v", err)
				}
			}()

			// Ensure the machine has synced to the cache
			if !waitForMachine(ctx, k8sClient, machine) {
				t.Fatalf("Unexpected error occurred while waiting for machine to be synced")
			}

			newActuator := actuator.NewActuator(actuator.ActuatorParams{
				Client:    k8sClient,
				Namespace: namespace,
				Scheme:    scheme.Scheme,
				OVirtClientFactory: ovirt.NewOvirtClientFactory(k8sClient, func(creds *ovirt.Creds) (ovirtclient.Client, error) {
					return helper.GetClient(), nil
				}),
				EventRecorder: mgr.GetEventRecorderFor("ovirtprovider"),
			})

			testcase.execute(newActuator, machine)

			createdVM, err := helper.GetClient().GetVMByName(machine.ObjectMeta.Name)
			if err != nil {
				t.Fatalf("Unexpected error occurred while fetching VM: %v", err)
			}
			testcase.verify(machine, createdVM, helper)

		})
	}
}

func setupK8sComponents(t *testing.T, k8sClient client.Client, namespace string) func() {
	testNamespace := &k8sCorev1.Namespace{
		ObjectMeta: k8sMetav1.ObjectMeta{
			Name: namespace,
		},
	}
	err := k8sClient.Create(context.Background(), testNamespace)
	if err != nil {
		t.Fatalf("Unexpected error occurred while creating k8s namespace: %v", err)
	}

	userDataSecret := &k8sCorev1.Secret{
		ObjectMeta: k8sMetav1.ObjectMeta{
			Name:      "ignitionscript",
			Namespace: namespace,
		},
		StringData: map[string]string{
			"userData": "igniteit",
		},
	}
	err = k8sClient.Create(context.Background(), userDataSecret)
	if err != nil {
		t.Fatalf("Unexpected error occurred while creating k8s user secret: %v", err)
	}

	ovirtCredentials := &k8sCorev1.Secret{
		ObjectMeta: k8sMetav1.ObjectMeta{
			Name:      "ovirt-credentials",
			Namespace: namespace,
		},
		StringData: map[string]string{
			"ovirt_url":       "http://localhost/ovirt-engine/api",
			"ovirt_username":  "user@internal",
			"ovirt_password":  "topsecret",
			"ovirt_cafile":    "",
			"ovirt_insecure":  "true",
			"ovirt_ca_bundle": "",
		},
	}
	err = k8sClient.Create(context.Background(), ovirtCredentials)
	if err != nil {
		t.Fatalf("Unexpected error occurred while creating k8s ovirt credentials secret: %v", err)
	}

	return func() {
		if err := k8sClient.Delete(context.Background(), testNamespace); err != nil {
			t.Errorf("Unexpected error occurred while deleting k8s namespace: %v", err)
		}
		if err := k8sClient.Delete(context.Background(), userDataSecret); err != nil {
			t.Errorf("Unexpected error occurred while deleting k8s user secret: %v", err)
		}
		if err := k8sClient.Delete(context.Background(), ovirtCredentials); err != nil {
			t.Errorf("Unexpected error occurred while deleting k8s ovirt credentials secret: %v", err)
		}
	}
}

func setupTestEnv(t *testing.T) (*rest.Config, func()) {
	testEnv := &envtest.Environment{
		CRDDirectoryPaths: []string{
			filepath.Join("..", "..", "..", "vendor", "github.com", "openshift", "api", "machine", "v1beta1"),
		},
	}

	cfg, err := testEnv.Start()
	if err != nil {
		t.Fatalf("Unexpected error occurred while starting testEnv: %v", err)
	}
	stopEnv := func() {
		if err := testEnv.Stop(); err != nil {
			t.Fatalf("Unexpected error occurred while stopping testEnv: %v", err)
		}
	}

	return cfg, stopEnv
}

func setupCtrlManager(t *testing.T, cfg *rest.Config) (manager.Manager, func()) {
	mgr, err := manager.New(cfg, manager.Options{Scheme: scheme.Scheme})
	if err != nil {
		t.Fatalf("Unexpected error occurred while creating manager: %v", err)
	}

	mgrCtx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := mgr.Start(mgrCtx); err != nil {
			t.Fatalf("Unexpected error occurred while running manager: %v", err)
		}
	}()

	return mgr, cancel
}

func setupVMTemplate(t *testing.T, helper ovirtclient.TestHelper, templateName string, vmParams ovirtclient.OptionalVMParameters) string {
	ovirtC := helper.GetClient()
	vm, err := ovirtC.CreateVM(helper.GetClusterID(), helper.GetBlankTemplateID(), "ovirt14-vhd9b", vmParams)
	if err != nil {
		t.Fatalf("Unexpected error occurred while creating VM for base template: %v", err)
	}

	baseVMTemplate, err := ovirtC.CreateTemplate(vm.ID(), templateName, nil)
	if err != nil {
		t.Fatalf("Unexpected error occurred while creating base template: %v", err)
	}

	return baseVMTemplate.Name()
}

func basicMachineWithoutSpec(namespace string) *machinev1.Machine {
	return &machinev1.Machine{
		ObjectMeta: k8sMetav1.ObjectMeta{
			Name:      "vm-12345",
			Namespace: namespace,
			Labels: map[string]string{
				machinev1.MachineClusterIDLabel: "CLUSTERID",
			},
		},
		Spec: machinev1.MachineSpec{
			ProviderSpec: machinev1.ProviderSpec{},
		},
	}
}

func waitForMachine(ctx context.Context, k8sClient client.Client, machine *machinev1.Machine) bool {
	for i := 0; i < 10; i++ {
		machineKey := types.NamespacedName{Namespace: machine.Namespace, Name: machine.Name}
		err := k8sClient.Get(ctx, machineKey, &machinev1.Machine{})
		if err == nil {
			return true
		}
		time.Sleep(1 * time.Second)
	}
	return false
}

func basicOVirtSpec(templateName string, clusterID string) *capoV1Beta1.OvirtMachineProviderSpec {
	return &capoV1Beta1.OvirtMachineProviderSpec{
		ClusterId:    clusterID,
		TemplateName: templateName,
		Name:         "vm-hello-ovirt",
		VMType:       "server",
		MemoryMB:     16348,
		Format:       "raw",
		OSDisk:       &capoV1Beta1.Disk{SizeGB: 31},
		CPU: &capoV1Beta1.CPU{
			Cores:   4,
			Threads: 1,
			Sockets: 1,
		},
		UserDataSecret: &k8sCorev1.LocalObjectReference{
			Name: "ignitionscript",
		},
		AutoPinningPolicy:  "",
		Hugepages:          0,
		GuaranteedMemoryMB: 10000,
	}
}

// failingOVirtClient embeds ovirtclient.Client from the TestHelper and redefines Test()
// to always fail, thereby simulating a credentials change
type failingOVirtClient struct {
	ovirtclient.Client
}

func (mc failingOVirtClient) Test(retries ...ovirtclient.RetryStrategy) error {
	return fmt.Errorf("Ups, I did it again")
}

func newFailingOVirtClient(helper ovirtclient.TestHelper) failingOVirtClient {
	return failingOVirtClient{
		Client: helper.GetClient(),
	}
}

func TestActuatorCredentialsUpdate(t *testing.T) {
	cfg, stopEnv := setupTestEnv(t)
	defer stopEnv()

	mgr, cancel := setupCtrlManager(t, cfg)
	defer cancel()

	k8sClient := mgr.GetClient()
	const namespace = "openshift-machine-api"
	k8sComponentCleanup := setupK8sComponents(t, k8sClient, namespace)
	defer k8sComponentCleanup()

	helper, err := ovirtclient.NewMockTestHelper(ovirtclientlog.NewTestLogger(t))
	if err != nil {
		t.Fatalf("Unexpected error occurred setting up test helper: %v", err)
	}

	ctx := context.Background()

	templateName := "ovirt14-vhd9b-rhcos"
	ovirtSpec := basicOVirtSpec(templateName, string(helper.GetClusterID()))
	templateVMCreateParams := ovirtclient.NewCreateVMParams()

	setupVMTemplate(t, helper, templateName, templateVMCreateParams)
	ovirtSpecRaw, err := capoV1Beta1.RawExtensionFromProviderSpec(ovirtSpec)
	if err != nil {
		t.Fatalf("Unexpected error occurred while parsing oVirtSpec: %v", err)
	}

	machine := basicMachineWithoutSpec(namespace)
	machine.Spec.ProviderSpec.Value = ovirtSpecRaw
	err = k8sClient.Create(ctx, machine)
	if err != nil {
		t.Fatalf("Unexpected error occurred while creating machine: %v", err)
	}
	defer func() {
		err = k8sClient.Delete(ctx, machine)
		if err != nil {
			t.Errorf("Unexpected error occurred while deleting machine: %v", err)
		}
	}()

	// Ensure the machine has synced to the cache
	if !waitForMachine(ctx, k8sClient, machine) {
		t.Fatalf("Unexpected error occurred while waiting for machine to be synced")
	}

	var finalCredentials *ovirt.Creds
	newActuator := actuator.NewActuator(actuator.ActuatorParams{
		Client:    k8sClient,
		Namespace: namespace,
		Scheme:    scheme.Scheme,
		OVirtClientFactory: ovirt.NewOvirtClientFactory(k8sClient, func(creds *ovirt.Creds) (ovirtclient.Client, error) {
			finalCredentials = creds
			return newFailingOVirtClient(helper), nil
		}),
		EventRecorder: mgr.GetEventRecorderFor("ovirtprovider"),
	})

	// execute the actuator the first time
	err = newActuator.Create(ctx, machine)
	if err != nil {
		t.Fatalf("Unexpected error occurred while calling actuator create: %v", err)
	}

	// update the credentials in k8s
	newOVirtUser := "anotherone@internal"
	newOVirtPassword := "differentpassword"
	ovirtCredentials := &k8sCorev1.Secret{
		ObjectMeta: k8sMetav1.ObjectMeta{
			Name:      "ovirt-credentials",
			Namespace: namespace,
		},
		StringData: map[string]string{
			"ovirt_url":       "http://localhost/ovirt-engine/api",
			"ovirt_username":  newOVirtUser,
			"ovirt_password":  newOVirtPassword,
			"ovirt_cafile":    "",
			"ovirt_insecure":  "true",
			"ovirt_ca_bundle": "",
		},
	}
	err = k8sClient.Update(context.Background(), ovirtCredentials)
	if err != nil {
		t.Fatalf("Unexpected error occurred while updating k8s ovirt credentials secret: %v", err)
	}

	// execute the actuator a second time
	err = newActuator.Create(ctx, machine)
	if err != nil {
		t.Fatalf("Unexpected error occurred while calling actuator create: %v", err)
	}

	if finalCredentials == nil {
		t.Fatal("oVirtClientFactory hase never been called")
	}

	if finalCredentials.Username != newOVirtUser || finalCredentials.Password != newOVirtPassword {
		t.Fatalf("Expected updated credentials to be ('%s', '%s'), but got ('%s', '%s')",
			newOVirtUser, newOVirtPassword,
			finalCredentials.Username, finalCredentials.Password,
		)
	}
}
