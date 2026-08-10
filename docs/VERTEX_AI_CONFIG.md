# Vertex AI location configuration

Starport makes one provider request for each executor attempt. The Vertex AI
connector therefore uses one explicit Google Cloud location and does not retry
through hidden backup regions.

## Configuration

Set the project and one location for both authentication modes:

```bash
export STARPORT_PROVIDERS_GOOGLE_VERTEX_PROJECT_ID=your-project-id
export STARPORT_PROVIDERS_GOOGLE_VERTEX_LOCATION=us-central1
```

You must set `STARPORT_PROVIDERS_GOOGLE_VERTEX_PROJECT_ID` and
`STARPORT_PROVIDERS_GOOGLE_VERTEX_LOCATION`. Starport binds these runtime
values to the endpoint template from Starmap.

Use Google Application Default Credentials for a renewable token:

```bash
export STARPORT_PROVIDERS_GOOGLE_VERTEX_AUTH_MODE=default
```

Default mode supports local Application Default Credentials, Google managed
identity, and workload identity federation. Starport requests the Google Cloud
platform scope. It preserves the quota project from the detected credentials
and sends it with each request. It refreshes the bearer token two minutes
before expiry.

Use a static OAuth access token only when token renewal is external:

```bash
export STARPORT_PROVIDERS_GOOGLE_VERTEX_AUTH_MODE=static
export STARPORT_PROVIDERS_GOOGLE_VERTEX_API_KEY=<OAuth-access-token>
```

Do not set the API-key variable with default mode. Starport rejects this
ambiguous configuration. The adapter requires `AUTH_MODE`. Ambient Google
credentials do not activate Vertex AI when this value is empty.

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
