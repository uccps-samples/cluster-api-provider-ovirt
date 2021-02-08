module github.com/openshift/cluster-api-provider-ovirt

go 1.13

require (
	github.com/go-logr/logr v0.3.0
	github.com/openshift/machine-api-operator v0.2.1-0.20210104142355-8e6ae0acdfcf
	github.com/ovirt/go-ovirt v0.0.0-20210112072624-e4d3b104de71
	github.com/pkg/errors v0.9.1
	golang.org/dl v0.0.0-20210204224843-1557c60ec592 // indirect
	k8s.io/api v0.20.0
	k8s.io/apimachinery v0.20.0
	k8s.io/client-go v0.20.0
	k8s.io/klog v1.0.0
	sigs.k8s.io/controller-runtime v0.7.0
	sigs.k8s.io/controller-tools v0.3.0
	sigs.k8s.io/yaml v1.2.0
)

replace sigs.k8s.io/cluster-api-provider-aws => github.com/openshift/cluster-api-provider-aws v0.2.1-0.20201125052318-b85a18cbf338

replace sigs.k8s.io/cluster-api-provider-azure => github.com/openshift/cluster-api-provider-azure v0.1.0-alpha.3.0.20201130182513-88b90230f2a4
