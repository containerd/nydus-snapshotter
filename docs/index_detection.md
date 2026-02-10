# Index Detection

## Overview

Index Detection is a feature that automatically discovers Nydus alternative manifests within OCI index manifests. This enables transparent fallback to optimized Nydus images when available while keeping the original index manifest OCI compliant, allowing non-Nydus clients to pull regular OCI images.

## Motivation

The Index Detection feature addresses several limitations of the existing Referrer API-based detection:

- **Registry Support**: Not all registries support the Referrer API
- **Supply Chain Security**: Referrer API relies on external changes to the manifest, creating potential supply chain risks
- **Universal Compatibility**: Index detection works with all OCI-compliant registries

Unlike referrer-based detection, Index Detection packages everything into a single manifest, eliminating supply chain concerns while maintaining universal registry support.

## How It Works

### Detection Process

1. **Index Manifest Parsing**: When a manifest digest is encountered, the system fetches and parses the original OCI index manifest
2. **Platform Matching**: The system finds the original manifest descriptor within the index
3. **Nydus Alternative Search**: It searches for platform-compatible manifests that contain:
   - `platform.os.features` containing `nydus.remoteimage.v1`, or
   - `artifactType` set to `application/vnd.nydus.image.manifest.v1+json`
4. **Validation**: The found manifest is validated to ensure it's a valid Nydus manifest with metadata layers
5. **Caching**: Results are cached to avoid repeated API calls for the same digest

Both `platform.os.features` and `artifactType` are looked at because index manifests built using `merge-platform` before acceleration-service: v0.2.19 or nydus: v2.3.5 will have `platform.os.features` configured while images built after will have `artifactType` configured.

### Detection Priority

When both Index Detection and Referrer Detection are available, Index Detection takes priority due to its superior supply chain security properties.

## Configuration

Index Detection is controlled by the `EnableIndexDetect` configuration option in the experimental features section:

```toml
[experimental]
enable_index_detect = true
```

## Building Compatible Images

To create images compatible with Index Detection, use the `nydusify convert` command with the `--merge-platform` flag:

```bash
nydusify convert --merge-platform <source-image> <target-image>
```

This creates an OCI index manifest containing both the original image and the Nydus alternative:

```json
{
  "schemaVersion": 2,
  "manifests": [
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:a63dfddecc661e4ad05896418b2f3774022269c3bf5b7e01baaa6e851a3a4a23",
      "size": 2320,
      "platform": {
        "architecture": "amd64",
        "os": "linux"
      }
    },
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:bb82dd8ee111bfe5fdf12145b6ed553da9c15de17f54b1f658d95ff26a65a01c",
      "size": 3229,
      "platform": {
        "architecture": "amd64",
        "os": "linux"
      },
      "artifactType": "application/vnd.nydus.image.manifest.v1+json"
    }
  ]
}
```

## Comparison with Referrer Detection

| Aspect | Index Detection | Referrer Detection |
|--------|----------------|-------------------|
| Registry Support | Universal | Limited |
| Supply Chain Security | Secure (single manifest) | Potential risks (external refs) |
| OCI Compliance | Full compliance | Requires Referrer API |
| Cache Behavior | Immutable (digest-based) | May require invalidation |
| Detection Priority | Higher | Lower |
