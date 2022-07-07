# cluster-api-provider-ovirt

https://github.com/oVirt/cluster-api-provider-ovirt

Implementation of the  oVirt provider for the [cluster-api project] version `v1beta` \
using openshift/cluster-api-provider api, which implements the machine actuator.

# Development

Fast development cycle is to build the binarties, `manager` and `machine-controller-manager` \
and run those against a running cluster kubeconfig.

## Build

```
make build
```

## Testing

Build tags are used to distinguish between different test scopes: 
```go
//go:build unit
//go:build functional
```

Run all available tests via
```bash 
make test
```

## Run the components locally

```console
$ export KUBECONFIG=path/to/kubecofig

$  bin/manager &

$  bin/machine-controller-manager --namespace openshift-machine-api --metrics-addr=:8888 &
``` 
