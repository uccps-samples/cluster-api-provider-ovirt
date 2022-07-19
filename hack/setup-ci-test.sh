#!/usr/bin/env bash

set -o nounset
set -o errexit
set -o pipefail

source ./hack/envtest-tools.sh && fetch_tools && setup_env
