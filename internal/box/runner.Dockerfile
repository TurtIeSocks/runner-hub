# The stock runner image has no compiler or make. Static runners got these
# from their host, so workflows written for them expect them.
ARG BASE
FROM ${BASE}
USER root
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential pkg-config zip xz-utils bzip2 wget file rsync \
 && rm -rf /var/lib/apt/lists/*
USER runner
