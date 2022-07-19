#!/usr/bin/env bash

function fetch_tools {
    k8s_version="${ENVTEST_K8S_VERSION:-1.21.2}"
    goarch="$(go env GOARCH)"
    goos="$(go env GOOS)"

    if [[ "$goos" != "linux" && "$goos" != "darwin" ]]; then
        echo "OS '$goos' not supported. Aborting." >&2
        return 1
    fi

    envtest_tools_archive_name="kubebuilder-tools-$k8s_version-$goos-$goarch.tar.gz"
    envtest_tools_download_url="https://storage.googleapis.com/kubebuilder-tools/$envtest_tools_archive_name"

    curl -L ${envtest_tools_download_url} -o ./hack/${envtest_tools_archive_name}
    tar -C "./hack" -zxvf "./hack/$envtest_tools_archive_name"
    rm "./hack/$envtest_tools_archive_name"
}

function setup_env {
    mv ./hack/kubebuilder /usr/local/
    export PATH=$PATH:/usr/local/kubebuilder/bin
}