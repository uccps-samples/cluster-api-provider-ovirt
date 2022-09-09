/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	configv1 "github.com/openshift/api/config/v1"
	machinev1 "github.com/openshift/api/machine/v1beta1"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/actuators/machine"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/apis"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/controller"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/ovirt"
	"github.com/openshift/cluster-api-provider-ovirt/pkg/utils"
	capimachine "github.com/openshift/machine-api-operator/pkg/controller/machine"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	logz "sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"
)

// The default durations for the leader election operations.
var (
	leaseDuration = 120 * time.Second
	renewDeadline = 110 * time.Second
	retryPeriod   = 20 * time.Second
	syncPeriod    = 10 * time.Minute
)

func main() {
	cfg := config.GetConfigOrDie()
	if cfg == nil {
		panic(fmt.Errorf("GetConfigOrDie didn't die and cfg is nil"))
	}

	// create oVirt client service to create cached clients syncing with secrets
	ctx, cancel := context.WithCancel(context.Background())
	oVirtClientService := ovirt.NewClientService(cfg, ovirt.SecretsToWatch{
		Namespace:  utils.NAMESPACE,
		SecretName: utils.OvirtCloudCredsSecretName,
	})

	flags := parseFlags()

	log := logz.New().WithName("ovirt-controller-manager")
	entryLog := log.WithName("entrypoint")

	mgr, err := setupManager(cfg, flags.ToManagerOptions(), oVirtClientService)
	if err != nil {
		entryLog.Error(err, "Unable to set up controller manager")
		os.Exit(1)
	}

	capimachine.AddWithActuator(mgr, machine.NewActuator(machine.ActuatorParams{
		Namespace:         flags.Namespace,
		Client:            mgr.GetClient(),
		Scheme:            mgr.GetScheme(),
		EventRecorder:     mgr.GetEventRecorderFor("ovirtprovider"),
		CachedOVirtClient: oVirtClientService.NewCachedClient("actuator"),
	}))
	controller.NewProviderIDController(mgr.GetClient(), oVirtClientService.NewCachedClient("providerID")).AddToManager(mgr)
	controller.NewNodeController(mgr.GetClient(), oVirtClientService.NewCachedClient("node")).AddToManager(mgr)

	// start the service to receive secret updates and immediately return
	oVirtClientService.Run(ctx)

	err = mgr.Start(signals.SetupSignalHandler())

	// shutdown oVirt client service
	cancel()
	oVirtClientService.Shutdown(3 * time.Second)

	if err != nil {
		entryLog.Error(err, "unable to run manager")
		os.Exit(1)
	}
}

func healthCheck(cachedOVirtClient ovirt.CachedOVirtClient) func(*http.Request) error {
	return func(req *http.Request) error {
		logger := ovirt.NewKLogr("HealthzCheck")
		logger.Infof("starting healthz check...")

		client, err := cachedOVirtClient.Get()
		if err != nil {
			logger.Errorf("failed to get ovirt client: %v", err)
			return err
		}
		err = client.Test()
		if err != nil {
			logger.Errorf("ovirt client connection test failed: %v", err)
			return err
		}
		logger.Infof("finished healthz check")

		return nil
	}
}

type Flags struct {
	Namespace string

	MetricsAddr string
	HealthAddr  string

	LeaderElectResourceNamespace string
	LeaderElect                  bool
	LeaderElectLeaseDuration     time.Duration
}

func (f Flags) ToManagerOptions() manager.Options {
	// Setup a Manager
	opts := manager.Options{
		LeaderElection:          f.LeaderElect,
		LeaderElectionNamespace: f.LeaderElectResourceNamespace,
		LeaderElectionID:        "cluster-api-provider-ovirt-leader",
		LeaseDuration:           &f.LeaderElectLeaseDuration,
		HealthProbeBindAddress:  f.HealthAddr,
		SyncPeriod:              &syncPeriod,
		MetricsBindAddress:      f.MetricsAddr,
		// Slow the default retry and renew election rate to reduce etcd writes at idle: BZ 1858400
		RetryPeriod:   &retryPeriod,
		RenewDeadline: &renewDeadline,
	}
	if f.Namespace != "" {
		opts.Namespace = f.Namespace
		klog.Infof("Watching machine-api objects only in namespace %q for reconciliation.", opts.Namespace)
	}

	return opts
}

func parseFlags() Flags {
	klog.InitFlags(nil)

	watchNamespace := flag.String(
		"namespace",
		"",
		"Namespace that the controller watches to reconcile machine-api objects. If unspecified, the controller watches for machine-api objects across all namespaces.",
	)

	metricsAddr := flag.String(
		"metrics-addr",
		":8081",
		"The address the metric endpoint binds to.",
	)

	healthAddr := flag.String(
		"health-addr",
		":9440",
		"The address for health checking.",
	)

	leaderElectResourceNamespace := flag.String(
		"leader-elect-resource-namespace",
		"",
		"The namespace of resource object that is used for locking during leader election. If unspecified and running in cluster, defaults to the service account namespace for the controller. Required for leader-election outside of a cluster.",
	)

	leaderElect := flag.Bool(
		"leader-elect",
		false,
		"Start a leader election client and gain leadership before executing the main loop. Enable this when running replicated components for high availability.",
	)

	leaderElectLeaseDuration := flag.Duration(
		"leader-elect-lease-duration",
		leaseDuration,
		"The duration that non-leader candidates will wait after observing a leadership renewal until attempting to acquire leadership of a led but unrenewed leader slot. This is effectively the maximum duration that a leader can be stopped before it is replaced by another candidate. This is only applicable if leader election is enabled.",
	)

	flag.Parse()

	return Flags{
		Namespace:                    *watchNamespace,
		MetricsAddr:                  *metricsAddr,
		HealthAddr:                   *healthAddr,
		LeaderElectResourceNamespace: *leaderElectResourceNamespace,
		LeaderElect:                  *leaderElect,
		LeaderElectLeaseDuration:     *leaderElectLeaseDuration,
	}
}

func setupManager(cfg *rest.Config, options manager.Options, oVirtClientService ovirt.ClientService) (manager.Manager, error) {
	mgr, err := manager.New(cfg, options)
	if err != nil {
		return nil, err
	}

	if err := apis.AddToScheme(mgr.GetScheme()); err != nil {
		return nil, err
	}

	if err := configv1.Install(mgr.GetScheme()); err != nil {
		return nil, err
	}

	if err := machinev1.AddToScheme(mgr.GetScheme()); err != nil {
		return nil, err
	}

	if err := mgr.AddReadyzCheck("ping", healthz.Ping); err != nil {
		return nil, fmt.Errorf("failed to add ready check to controller manager: %v", err)
	}

	if err := mgr.AddHealthzCheck("ping", healthCheck(oVirtClientService.NewCachedClient("healthz"))); err != nil {
		return nil, fmt.Errorf("failed to add health check to controller manager: %v", err)
	}

	return mgr, nil
}
