# The stock runner image has no compiler or make. Static runners got these
# from their host, so workflows written for them expect them.
ARG BASE
FROM ${BASE}
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential cmake clang libclang-dev pkg-config zip xz-utils zstd bzip2 wget file rsync \
 && rm -rf /var/lib/apt/lists/*
# The base image leaves /home/runner world-writable (777). Native addons that
# keep a cache under it, like @swc/core's, refuse a cache root whose parent
# another user can write to, so builds using them fail to load.
RUN chmod 755 /home/runner
USER runner
