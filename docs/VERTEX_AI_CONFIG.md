# Vertex AI location configuration

Starport makes one provider request for each executor attempt. The Vertex AI
connector therefore uses one explicit Google Cloud location and does not retry
through hidden backup regions.

## Configuration

```bash
export STARPORT_PROVIDERS_GOOGLE_VERTEX_PROJECT_ID=your-project-id
export STARPORT_PROVIDERS_GOOGLE_VERTEX_LOCATION=us-central1
export STARPORT_PROVIDERS_GOOGLE_VERTEX_API_KEY=<OAuth-access-token>
```

You must set `STARPORT_PROVIDERS_GOOGLE_VERTEX_PROJECT_ID` and
`STARPORT_PROVIDERS_GOOGLE_VERTEX_LOCATION`. Starport binds these runtime
values to the endpoint template from Starmap. The current adapter sends the
configured API-key value as a bearer access token. It does not load a
service-account file.

## Failure behavior

The connector returns the first location failure to the central attempt
executor. The executor applies one total attempt and elapsed-time budget across
retries and route fallbacks. A streaming request can change routes only before
Starport returns its first event.

To use regional redundancy, represent each region as an explicit route in a
future catalog extension. Do not put a region loop inside the provider adapter.
This keeps attempt counts, time budgets, and availability evidence complete.
