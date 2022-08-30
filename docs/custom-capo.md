# Creating a custom Cluster API Provider oVirt (CAPO) for oVirt

When we develop the Cluster API Provider oVirt (CAPO) its usually helpful to test it against a running cluster.
The following doc explains how to do it.

## Prerequisites

- Ready OpenShift cluster running on oVirt and a user with admin permissions.
- Container registry (recommended quay.io) account which the OpenShift cluster can reach and pull from.

Note:
For info about installing and launching a new cluster using oVirt check:

- [Installer Provisioned Infrastructure (IPI)](https://github.com/openshift/installer/blob/master/docs/user/ovirt/install_ipi.md)
- [User Provisioned Infrastructure (UPI)](https://github.com/openshift/installer/blob/master/docs/user/ovirt/install_upi.md)

## Step 1: Create a custom container image

```bash
$ podman login quay.io
$ podman build -t quay.io/${username}/origin-ovirt-machine-controllers:${tag} .
$ podman push quay.io/${username}/origin-ovirt-machine-controllers:${tag}
```

- ${username} - container registry username
- ${tag} - origin-ovirt-machine-controllers container image tag.

**Note:** 
In this example quay.io as container registry is used. However, any registry can be used as long as the cluster is able to pull images from it.

## Step 2: Stop Cluster Version Operator

In the running cluster the [cluster-version-operator](https://github.com/openshift/cluster-version-operator) is responsible for maintaining functioning and
non-altered elements.

For the purpose of using a custom image, the Cluster Version Operator needs to be disabled so that it will not revert the changes back to the original image. The easiest way
to achieve this is by scaling it down:

```bash
$ oc scale --replicas=0 deploy/cluster-version-operator -n openshift-cluster-version
```

**Note:**
This version disables the Cluster Version Operator and it is recommended for **testing purposes** only, once you are
done with the test you should scale the operator back

## Step 3: Replacing the container image

The `cluster-api-provider-ovirt` - as well as all other provider - is being managed by the [machine-api-operator](https://github.com/openshift/machine-api-operator). The operator uses a [ConfigMap](https://github.com/openshift/machine-api-operator/blob/master/install/0000_30_machine-api-operator_01_images.configmap.yaml) with a list of the provider images. In order to inject the custom oVirt provider image this ConfigMap has to be edited:

```bash
oc edit configmap -n openshift-machine-api machine-api-operator-images
```

In the ConfigMap search for the `data.image.json` entry and replace the value of `clusterAPIControllerOvirt` to point to the custom image (by digest). 

For example:

```json
// Change:
{
    // ...
    "clusterAPIControllerOvirt": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:e783933a8e7dee85e2565312c6631b7f883da3dc666d0bce989188acc23d56dc",
    // ...
}

// To: 
{
    // ...
    "clusterAPIControllerOvirt": "quay.io/rh_ee_mengel/ovirt-machine-api@sha256:c72048f1e49bc63af544939fae110ff3fe2966a8d97c0fbb3e3c344a1677a94e",
    // ...
}
```

This will cause the `machine-api-operator` to sync with the changes and restart the pod `machine-api-controllers` in the namespace `openshift-machine-api` with the custom image. 

**NOTE:**
To find the digest on quay.io, go to your image page and under tags click on fetch tag by digest  

## Step 4: Verify container image

Verify that the process was successful by checking the digest hash of the `machine-controller`:

```bash
  // Check pods contain the correct image
  oc get deployments -n openshift-machine-api machine-api-controllers -o json | jq '.spec.template.spec.containers[]|select(.name == "machine-controller")|."image"'
```
