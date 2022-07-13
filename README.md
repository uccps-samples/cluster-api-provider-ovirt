# cluster-api-provider-ovirt

https://github.com/oVirt/cluster-api-provider-ovirt

Implementation of the  oVirt provider for the [cluster-api project] version `v1beta` \
using openshift/cluster-api-provider api, which implements the machine actuator.

# Development

Fast development cycle is to build the binarties, `manager` and `machine-controller-manager` \
and run those against a running cluster kubeconfig.

## Build

The following make targets are provided:
```bash
# build the binary
make build  

# build the image
make images
```

### Code Generation

On any changes in the [specification](https://github.com/openshift/cluster-api-provider-ovirt/blob/master/pkg/apis/ovirtprovider/v1beta1/types.go), run 
```bash
make generate
```
to automatically generate the new [CustomResourceDefinition](https://kubernetes.io/docs/tasks/extend-kubernetes/custom-resources/custom-resource-definitions/)s for oVirt.


## Testing

Run all tests together via 
```
make test
```

### Unit Testing

Unit tests are marked with the build tag
```go
//go:build unit
```
and can be run via  
```bash
make test-unit
```


### Functional Testing

Functional (or integration) tests for the `cluster-api-provider-ovirt` are based on [envtest] instead of a real cluser. For more information, refer to [The Cluster API Book](https://cluster-api.sigs.k8s.io/developer/testing.html#integration-tests) on integration tests. 

The necessary tooling can be fetched and installed via
```bash
./hack/fetch-envtest-tools.sh
mv ./hack/kubebuilder /usr/local/
export PATH=$PATH:/usr/local/kubebuilder/bin
```

Functional (or integration) tests are marked with the build tag
```go
//go:build functional
```
and can be run via 
```bash
make test-functional
```

## Run the components locally

```console
$ export KUBECONFIG=path/to/kubecofig

$  bin/manager &

$  bin/machine-controller-manager --namespace openshift-machine-api --metrics-addr=:8888 &
``` 
