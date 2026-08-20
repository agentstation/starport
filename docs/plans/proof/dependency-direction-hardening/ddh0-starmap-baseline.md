# DDH0 Starmap dependency baseline

Date: 2026-08-19

Starmap work commit: `30874a34ad869919ba09054985127b88ea1864b9`

## Outcome

DDH0 added eight executable dependency-direction conditions and one isolated
mutation for each condition. The mutation suite passed. The repository baseline
failed all eight conditions and named each reversed package edge.

## Verification

Command:

```text
bash scripts/test-catalog-dependency-direction-verifier.sh
```

Result:

```text
catalog dependency direction verifier tests passed
```

Command:

```text
bash scripts/verify-catalog-dependency-direction.sh
```

Expected fail-before result:

```text
SM-D01 FAIL: catalogs does not import private authority policy
  pkg/catalogs imports forbidden private package internal/catalog/authority
SM-D02 FAIL: catalogs does not import repository-wide constants
  pkg/catalogs imports forbidden private package internal/constants
SM-D03 FAIL: catalogs does not import the private embedded filesystem
  pkg/catalogs imports forbidden private package internal/embedded
SM-D04 FAIL: catalogs does not import private source payload policy
  pkg/catalogs imports forbidden private package internal/sources/payload
SM-D05 FAIL: catalog artifacts do not import repository-wide constants
  pkg/catalogs/artifact imports forbidden private package internal/constants
SM-D06 FAIL: catalog remote transport does not import repository-wide constants
  pkg/catalogs/remote imports forbidden private package internal/constants
SM-D07 FAIL: catalog storage does not import repository-wide constants
  pkg/catalogs/storage imports forbidden private package internal/constants
SM-D08 FAIL: catalog S3 storage does not import repository-wide constants
  pkg/catalogs/storage/s3 imports forbidden private package internal/constants
Summary: 0 passed, 8 failed
```

Exit status: `1`, as required for the red baseline.
