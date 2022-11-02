# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#    http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

FROM openshift/golang-builder@sha256:4820580c3368f320581eb9e32cf97aeec179a86c5749753a14ed76410a293d83 AS builder
ENV __doozer=update BUILD_RELEASE=202202160023.p0.g35ce9aa.assembly.stream BUILD_VERSION=v4.10.0 OS_GIT_MAJOR=4 OS_GIT_MINOR=10 OS_GIT_PATCH=0 OS_GIT_TREE_STATE=clean OS_GIT_VERSION=4.10.0-202202160023.p0.g35ce9aa.assembly.stream SOURCE_GIT_TREE_STATE=clean 
ENV __doozer=merge OS_GIT_COMMIT=35ce9aa OS_GIT_VERSION=4.10.0-202202160023.p0.g35ce9aa.assembly.stream-35ce9aa SOURCE_DATE_EPOCH=1639610098 SOURCE_GIT_COMMIT=35ce9aafee1ffad0734f02ff0d5f8632d3905f09 SOURCE_GIT_TAG=v0.1-186-g35ce9aa SOURCE_GIT_URL=https://github.com/uccps-samples/cluster-api-provider-ovirt 

ARG version
ARG release

LABEL   com.redhat.component="machine-api" \
        name="cluster-api-provider-ovirt" \
        version="$version" \
        release="$release" \
        architecture="x86_64" \
        summary="cluster-api-provider-ovirt" \
        maintainer="OCP RHV Team <ocprhvteam@redhat.com>"

WORKDIR /go/cluster-api-provider-ovirt
COPY . .


RUN git --version
RUN make build

FROM openshift/ose-base:v4.10.0.20220216.010142
ENV __doozer=update BUILD_RELEASE=202202160023.p0.g35ce9aa.assembly.stream BUILD_VERSION=v4.10.0 OS_GIT_MAJOR=4 OS_GIT_MINOR=10 OS_GIT_PATCH=0 OS_GIT_TREE_STATE=clean OS_GIT_VERSION=4.10.0-202202160023.p0.g35ce9aa.assembly.stream SOURCE_GIT_TREE_STATE=clean 
ENV __doozer=merge OS_GIT_COMMIT=35ce9aa OS_GIT_VERSION=4.10.0-202202160023.p0.g35ce9aa.assembly.stream-35ce9aa SOURCE_DATE_EPOCH=1639610098 SOURCE_GIT_COMMIT=35ce9aafee1ffad0734f02ff0d5f8632d3905f09 SOURCE_GIT_TAG=v0.1-186-g35ce9aa SOURCE_GIT_URL=https://github.com/uccps-samples/cluster-api-provider-ovirt 

COPY --from=builder /go/cluster-api-provider-ovirt/bin/machine-controller-manager /

LABEL \
        name="openshift/ose-ovirt-machine-controllers" \
        com.redhat.component="ose-ovirt-machine-controllers-container" \
        io.openshift.maintainer.product="OpenShift Container Platform" \
        io.openshift.maintainer.component="Cloud Compute" \
        io.openshift.maintainer.subcomponent="oVirt Provider" \
        release="202202160023.p0.g35ce9aa.assembly.stream" \
        io.openshift.build.commit.id="35ce9aafee1ffad0734f02ff0d5f8632d3905f09" \
        io.openshift.build.source-location="https://github.com/uccps-samples/cluster-api-provider-ovirt" \
        io.openshift.build.commit.url="https://github.com/uccps-samples/cluster-api-provider-ovirt/commit/35ce9aafee1ffad0734f02ff0d5f8632d3905f09" \
        version="v4.10.0"

