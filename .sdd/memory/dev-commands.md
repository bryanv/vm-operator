# Development commands

Run from the repository root. `make help` lists every target.

## Lint and generate

| Task | Command |
|------|---------|
| Lint | `make lint-go` |
| Lint, auto-fixing what can be fixed | `make fix` |
| Lint with the slower linters enabled | `make lint-go-full` |
| Generate Go sources (deepcopy, conversions) after changing `api/` types | `make generate-go` |
| Generate CRD / RBAC manifests after changing `api/` types or kubebuilder markers | `make generate-manifests` |
| Generate manifests for the `external/` modules | `make generate-external-manifests` |
| Verify checked-in generated code is current | `make verify-codegen` |

Generated CRD manifests under `config/crd/` are **checked in** — regenerate and commit them in the same change set as the API edit that requires them.

## Unit and integration tests

`make test` runs the **whole** suite: it builds `etcd` and `kube-apiserver` into `hack/tools/bin/<goos>_<goarch>/` and invokes `ginkgo` with `--timeout=3h`. Expect a long run; prefer a narrower invocation while iterating.

| Task | Command |
|------|---------|
| Full suite (long) | `make test` |
| Single package | `./hack/test.sh ./pkg/errors` |
| Package tree | `./hack/test.sh ./controllers/virtualmachine/...` |
| Skip specs that need envtest | `LABEL_FILTER='!envtest' ./hack/test.sh ./controllers/infra/zone` |
| One label category | `LABEL_FILTER='controller' ./hack/test.sh ./controllers/...` |

`hack/test.sh` forwards its arguments to `ginkgo` and honors `LABEL_FILTER`, `GO_TEST_SKIP_PKGS`, `GO_TEST_SKIP_FILE`, `GO_TEST_PARALLEL`, and `GO_TEST_RECURSIVE`. Labels come from `pkg/constants/testlabels` — see [`testing-standards.md`](./testing-standards.md).

Two quirks when calling `hack/test.sh` directly rather than through `make`:

- envtest specs need `KUBEBUILDER_ASSETS`, which **only the Makefile exports** (as the absolute path to `hack/tools/bin/<goos>_<goarch>`). Either go through `make test`, filter envtest specs out with `LABEL_FILTER='!envtest'`, or export `KUBEBUILDER_ASSETS` yourself.
- The script invokes `ginkgo` from `$PATH`, not the pinned copy under `hack/tools/bin/`.

## E2E tests

Commands, environment variables, and labels: [`e2e-testing.md`](./e2e-testing.md).

`source ./hack/e2e/setup-testbed-env.sh <testbedInfo.json|URL> --e2e` exports variables into the **current shell only** — run it in the same shell invocation as the test command it configures.
