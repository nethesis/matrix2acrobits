# Container build

This repository includes a GitHub Actions workflow that builds a container image from `Containerfile` and pushes it to GitHub Container Registry (`ghcr.io`). Tagging rules used by the workflow:

- If the push is a Git tag (`refs/tags/*`) the image is pushed with that tag and a `sha-<short-sha>` tag.
- If the push is to `main`, the image is pushed with `latest` and `sha-<short-sha>`.
- For other branches the image is pushed with a sanitized branch-name tag and `sha-<short-sha>`.

The image can be pulled and run as follows:

```bash
podman pull ghcr.io/gsanchietti/matrix2acrobits:latest
```