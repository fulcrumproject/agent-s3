# Test fixture only: the binary is built from an unchanged, recorded core revision.
# Build context must contain only that binary, never local environment files.
FROM scratch
COPY --chown=10001:10001 core /core
USER 10001:10001
ENTRYPOINT ["/core"]
