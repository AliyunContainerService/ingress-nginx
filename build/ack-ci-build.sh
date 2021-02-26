#!/bin/bash

set -x
set -e

log() {
    echo "[$(date +%F\ %T)] INFO: $*" >&2
}

error() {
    echo "ERROR: $*"
}

check_build_params() {
    log "check params ..."

    if [[ -z "${INGRESS_TAG}" ]]; then
	    error "missing parameter 'INGRESS_TAG'"
        return 1
    fi

    if [[ -z "${INGRESS_BRANCH}" ]]; then
	    error "missing parameter 'INGRESS_BRANCH'"
        return 1
    fi

    if [[ -z "${INGRESS_PLATFORM}" ]]; then
	    error "missing parameter 'INGRESS_PLATFORM'"
        return 1
    fi

    case $INGRESS_TARGET in
        "sync_base_image" | "build_ingress_nginx" | "build_ingress_controller" | "build_ingress_controller_e2e_image")
            :
            ;;
        *)
            error "target \"$INGRESS_TARGET\" is not supported"
            return 1
            ;;
    esac
}

setup_build_params() {
    log "setup build params ..."

    NGINX_VERSION="v20210115-gba0502603"
    E2E_TEST_VERSION="v20210104-g81a8d5cd8"
    WEBHOOK_CERTGEN_VERSION="v1.5.1"
}

setup_build_env() {
    log "setup build env ..."

    local build_dir="/home/build/go/src/k8s.io/"
    rm -rf $build_dir/ingress-nginx && mkdir -p $build_dir
    mv ingress-nginx $build_dir && cd $build_dir/ingress-nginx

    export GIT_SHA=$(git rev-parse --short HEAD)
    export PATH="$PATH:/sbin:/usr/sbin:/usr/local/sbin:/bin:/usr/bin:/usr/local/bin:/usr/local/go/bin"
    export GOARCH="amd64"
    export GOOS="linux"

    # show go version
    go version

    # show current commit id
    git log --abbrev-commit -1
}

login_docker_registry() {
    log "login docker ..."

    DOCKER_REGISTRY="registry.cn-hangzhou.aliyuncs.com"
    docker login -u ${ACS_BUILD_ACCOUNT} -p ${ACS_BUILD_PWD} $DOCKER_REGISTRY

    # show docker info
    docker info

    # show images
    docker images | grep -e ingress -e nginx -e e2e-test-runner -e kube-webhook-certgen || true
}

sync_base_image(){
    log "sync base image ..."

    docker pull k8s.gcr.io/ingress-nginx/nginx:$NGINX_VERSION
    docker pull k8s.gcr.io/ingress-nginx/e2e-test-runner:$E2E_TEST_VERSION
    docker pull docker.io/jettech/kube-webhook-certgen:$WEBHOOK_CERTGEN_VERSION

    docker image tag k8s.gcr.io/ingress-nginx/nginx:$NGINX_VERSION $DOCKER_REGISTRY/acs/nginx:$NGINX_VERSION
    docker image tag k8s.gcr.io/ingress-nginx/e2e-test-runner:$E2E_TEST_VERSION  $DOCKER_REGISTRY/acs/e2e-test-runner:$E2E_TEST_VERSION
    docker image tag docker.io/jettech/kube-webhook-certgen:$WEBHOOK_CERTGEN_VERSION $DOCKER_REGISTRY/acs/kube-webhook-certgen:$WEBHOOK_CERTGEN_VERSION

    docker push $DOCKER_REGISTRY/acs/nginx:$NGINX_VERSION
    docker push $DOCKER_REGISTRY/acs/e2e-test-runner:$E2E_TEST_VERSION
    docker push $DOCKER_REGISTRY/acs/kube-webhook-certgen:$WEBHOOK_CERTGEN_VERSION
}

build_ingress_nginx(){
    log "build ingress nginx ..."

    cd images/nginx
    IMAGE=$DOCKER_REGISTRY/acs/ack-nginx TAG=$NGINX_VERSION make build-nginx
    docker push $DOCKER_REGISTRY/acs/ack-nginx:$NGINX_VERSION
}

build_ingress_controller(){
    log "build ingress nginx controller ..."

    local target_tag="v${INGRESS_TAG}-${GIT_SHA}-aliyun"
    VERBOSE=1 DEBUG=1 DOCKER_IN_DOCKER_ENABLED=true TAG=$target_tag REGISTRY=acs/ingress-controller \
        BASE_IMAGE=$DOCKER_REGISTRY/acs/nginx:$NGINX_VERSION \
        REPO_INFO=ingress-nginx E2E_IMAGE=$DOCKER_REGISTRY/acs/e2e-test-runner:$E2E_TEST_VERSION make clean build image

    docker tag acs/ingress-controller/controller:$target_tag $DOCKER_REGISTRY/acs/aliyun-ingress-controller:$target_tag
    docker push $DOCKER_REGISTRY/acs/aliyun-ingress-controller:${userTag} || docker push $DOCKER_REGISTRY/acs/aliyun-ingress-controller:$target_tag
}

main() {
    check_build_params
    setup_build_params
    setup_build_env
    login_docker_registry

    case $INGRESS_TARGET in
    "sync_base_image")
        sync_base_image;;
    "build_ingress_nginx")
        build_ingress_nginx;;
    "build_ingress_controller")
        build_ingress_controller;;
    esac
}

main

