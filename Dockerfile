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

FROM registry.ci.openshift.org/openshift/release:golang-1.18

WORKDIR /go/cluster-api-provider-ovirt
COPY . .

# unit testing
RUN make test-unit

# functional testing
RUN sh ./hack/fetch-envtest-tools.sh
RUN mv ./hack/kubebuilder /usr/local/
RUN export PATH=$PATH:/usr/local/kubebuilder/bin
RUN make test-functional


FROM registry.ci.openshift.org/openshift/release:golang-1.18 AS builder

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

RUN make build


FROM registry.ci.openshift.org/openshift/origin-v4.0:base

COPY --from=builder /go/cluster-api-provider-ovirt/bin/machine-controller-manager /
