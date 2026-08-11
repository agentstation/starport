# Vertex AI location configuration

Starport makes one provider request for each executor attempt. The Vertex AI
connector therefore uses one explicit Google Cloud location and does not retry
through hidden backup regions.

## Configuration

Set the project and one location with the conventional names declared by the
Starmap provider record:

```bash
export GOOGLE_CLOUD_PROJECT=your-project-id
export GOOGLE_CLOUD_LOCATION=us-central1
```

Starport checks the ordered conventional names before the derived
`STARPORT_GOOGLE_VERTEX_PROJECT` and `STARPORT_GOOGLE_VERTEX_LOCATION` names.
It binds the selected values to the endpoint template from Starmap.

Configure Google Application Default Credentials for a renewable token. For
example, use one of the standard Google credential mechanisms:

```bash
gcloud auth application-default login
```

Default mode supports local Application Default Credentials, Google managed
identity, and workload identity federation. Starport requests the Google Cloud
platform scope. It preserves the quota project from the detected credentials
and sends it with each request. It refreshes the bearer token two minutes
before expiry.

The current Starmap Vertex AI inference profile uses the compiled
`google-default` authentication primitive. It does not declare a static token
profile or an `AUTH_MODE` field. If the catalog adds a new typed profile,
Starport can select it without a provider-specific branch when the primitive
is already compiled.

Starmap resolves its catalog-acquisition credentials independently. Starport
does not copy a Starmap credential into an inference request.

## Failure behavior

The connector returns the first location failure to the central attempt
executor. The executor applies one total attempt and elapsed-time budget across
retries and route fallbacks. A streaming request can change routes only before
Starport returns its first event.

To use regional redundancy, represent each region as an explicit route in a
future catalog extension. Do not put a region loop inside the provider adapter.
This keeps attempt counts, time budgets, and availability evidence complete.
