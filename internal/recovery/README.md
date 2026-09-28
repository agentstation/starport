# Deployment recovery

This package owns approval of the deployment KV incarnation and capture of portable backup bundles.
It does not replace external controls that stop gateways or isolate a former primary.

`BackupBundle` requires open KV, SQL, and blob adapters, a closed SQL recovery record, encryption-key access, and selected local files.
The caller must stop and fence writers before capture.
The coordinator compares the SQL boundary before capture and before publishing the manifest.
A changed boundary leaves partial output without a complete manifest.

The bundle contains the portable KV image, relational image, blob archive, and selected files.
Each artifact has a path, size, and SHA-256 digest.
The component receipts and the bundle manifest bind those artifacts.
The private directory retains partial output after failure for diagnosis.
The manifest is the final publication. Capture never opens the recovery gate.

`BundleManifest.Digest` returns the digest that the operator must retain independently.
`VerifyBundle` requires that digest and the encryption key.
It refuses missing, changed, additional, non-regular, and unsupported files.
Known empty publication locks are control files outside the payload inventory.
Unexpected lock content and pending publication files cause refusal.

The encrypted challenge proves access to the key selected during capture.
It does not prove that every historical credential uses that key.
Credential owners must validate their encrypted records before recovery approval.

The application still owns the complete selected-file inventory and external dependency list.
This includes configuration, catalog runtime and baseline files, identity state, credential-selection policy, source inputs, and transport trust.
File and job owners must verify record references against captured bytes.
These checks and the operator commands remain required before deployment recovery is complete.

A verified bundle grants no permission to resume inference.
Recovery must reconcile permission withdrawals, acknowledged spending, and uncertain work after the backup against independent evidence.
Unknown history keeps affected access restricted.
Restoring the SQL witness cannot restore permission to approve itself.
