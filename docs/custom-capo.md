# Creating a custom Cluster API Provider oVirt (CAPO) for oVirt

When we develop the Cluster API Provider oVirt (CAPO) its usually helpful to test it against a running cluster.
The following doc explains how to do it.

## Replacing the container image

1.Launch a cluster:

Start an Openshift cluster on top of oVirt as usual with the required release image.

Note:
For info about installing and launching a new cluster using oVirt check:

- [Installer Provisioned Infrastructure (IPI)](https://github.com/uccps-samples/installer/blob/master/docs/user/ovirt/install_ipi.md)
- [User Provisioned Infrastructure (UPI)](https://github.com/uccps-samples/installer/blob/master/docs/user/ovirt/install_upi.md)

2. Create the container image:

```bash
$ podman login quay.io
$ podman build -t quay.io/${username}/cluster-api-provider-ovirt:${tag} .
$ podman push quay.io/${username}/cluster-api-provider-ovirt:${tag}
```

- ${username} - quay.io username
- ${tag} - cluster-api-provider-ovirt container image tag.

3. Stop Cluster Version Operator
In the running cluster cluster-version-operator is responsible for maintaining functioning and non-altered elements.
In that case to be able to use custom operator image one has to perform one of these operations:
Set your operator in umanaged state, see here for details, in short:
```yaml
$ oc patch clusterversion/version --type='merge' -p "$(cat <<- EOF
spec:
  overrides:
  - group: apps/v1
    kind: Deployment
    name: machine-api-operator
    namespace: openshift-machine-api
    unmanaged: true
EOF
)"
```

Scale down cluster-version-operator:

```bash
$ oc scale --replicas=0 deploy/cluster-version-operator -n openshift-cluster-version
```

IMPORTANT:

This approach disables cluster-version-operator completly, whereas previous only tells it to not manage a kube-scheduler-operator!

4. Replace the CAPO image

```bash
$ oc -n openshift-machine-api edit configmap/machine-api-operator-images
```

and replace clusterAPIControllerOvirt with the pull (by digest) of your image

NOTE:
To find the digest simply log into quay,
go to the cluster-api-provider-ovirt repo,
and under tags click on fetch tag by digest  

5. verify that the image has been replaced:

```bash
$ oc -n openshift-machine-api get pods
```

verify that pod pod/machine-api-controllers-***-*** has been updated