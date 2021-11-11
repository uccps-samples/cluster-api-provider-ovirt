module github.com/openshift/cluster-api-provider-ovirt

go 1.16

require (
	github.com/go-logr/logr v0.4.0
	github.com/golang/mock v1.5.0
	github.com/onsi/gomega v1.14.0
	github.com/openshift/api v0.0.0-20211025104849-a11323ccb6ea
	github.com/openshift/client-go v0.0.0-20211025111749-96ca2abfc56c
	github.com/openshift/machine-api-operator v0.2.1-0.20211111133920-c8bba3e64310
	github.com/ovirt/go-ovirt v0.0.0-20210308100159-ac0bcbc88d7c
	github.com/pkg/errors v0.9.1
	k8s.io/api v0.22.1
	k8s.io/apimachinery v0.22.1
	k8s.io/client-go v0.22.1
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.9.6
	sigs.k8s.io/controller-tools v0.6.3-0.20210916130746-94401651a6c3
	sigs.k8s.io/yaml v1.2.0
)
