#!/usr/bin/env bash
# Deploy cert-manager to the local cluster

set -o errexit
set -o pipefail
set -o nounset
set -x

CERT_MANAGER_URL="git::https://gitlab.eng.vmware.com/core-build/cayman_photon/support/install/objects/cert-manager/local?submodules=false&ref=0255b635116f67feb7bc61151a2b7e36f5c77055"

# Exit with a non-zero exit code and an error message if
# the CERT_MANAGER_URL is not set.
CERT_MANAGER_URL="${CERT_MANAGER_URL:?}"

mkdir -p artifacts

./hack/tools/bin/kustomize build \
  --load-restrictor LoadRestrictionsNone \
  "${CERT_MANAGER_URL}" \
  >artifacts/cert-manager.yaml

kubectl apply -f artifacts/cert-manager.yaml
