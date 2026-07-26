# Race-detector gate for core, run as a Docker BUILD rather than a container
# the workflow starts itself.
#
# That indirection is not decoration. Two simpler mechanisms were tried on this
# self-hosted runner and both failed for reasons that have nothing to do with
# the code under test:
#
#   docker run -v "$PWD:/src"  - the runner is itself containerized, so that
#     path does not exist for the host daemon. Docker then CREATES an empty
#     directory and mounts it instead of failing, and the run died with
#     "directory prefix . does not contain main module".
#
#   a job-level `container:`   - job containers need the runner's externals
#     mounted at /__e, and this runner does not pass them through, so
#     actions/checkout could not exec node ("/__e/node24/bin/node: no such
#     file or directory").
#
# A build works because the context is UPLOADED through the Docker API instead
# of mounted, which is exactly why the image-build jobs on this same runner
# have always worked. A failing RUN fails the build, which fails the step.
#
# Deliberately unoptimized: one COPY, one RUN, no BuildKit cache mounts and no
# go.mod-first layer split. Both would help the runtime and both are more
# moving parts than a gate that has already failed twice should carry. Optimize
# once this has been green on a real run.

FROM golang:1.26

WORKDIR /src

# The whole repo, because GOWORK must stay ON: core/go.mod replaces only
# dylaris-pkg, while dylaris-proto resolves through go.work, and a workspace
# refuses to load unless every member directory it lists is present.
COPY . .

# Running ./... from core still compiles only core's own packages, so beam/app
# is never built here and needs no Wails frontend stub.
WORKDIR /src/core
RUN CGO_ENABLED=1 go test -race -count=1 ./...
