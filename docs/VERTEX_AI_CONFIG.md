# Vertex AI Multi-Region Configuration

This document explains how to configure Google Vertex AI with multi-region support in Starport.

## Overview

The Vertex AI connector now supports:
- Primary region configuration
- Automatic failover to backup regions
- Intelligent region selection based on geography
- Retry logic for transient failures

## Configuration

### Basic Configuration

```bash
# Required: Google Cloud Project ID
export STARPORT_PROVIDERS_GOOGLE_VERTEXAI_PROJECT_ID=your-project-id

# Optional: Primary location (default: us-central1)
export STARPORT_PROVIDERS_GOOGLE_VERTEXAI_LOCATION=us-central1

# Optional: Service account credentials
export GOOGLE_APPLICATION_CREDENTIALS=/path/to/service-account.json
```

### Advanced Multi-Region Configuration

You can specify custom fallback regions via the configuration file:

```yaml
providers:
  google_vertexai:
    project_id: "your-project-id"
    location: "us-central1"
    fallback_locations:
      - "us-east4"
      - "us-west1"
      - "europe-west4"
```

## Default Fallback Regions

If no custom fallback regions are specified, the connector uses intelligent defaults based on your primary region:

### US Regions
- **us-central1**: Falls back to → us-east4, us-west1, us-west4
- **us-east4**: Falls back to → us-central1, us-west1, us-west4
- **us-west1**: Falls back to → us-west4, us-central1, us-east4
- **us-west4**: Falls back to → us-west1, us-central1, us-east4

### Europe Regions
- **europe-west1**: Falls back to → europe-west4, europe-west2, europe-north1
- **europe-west2**: Falls back to → europe-west1, europe-west4, europe-north1
- **europe-west4**: Falls back to → europe-west1, europe-west2, europe-north1
- **europe-north1**: Falls back to → europe-west4, europe-west1, europe-west2

### Asia Regions
- **asia-southeast1**: Falls back to → asia-northeast1, asia-east1, asia-south1
- **asia-northeast1**: Falls back to → asia-southeast1, asia-east1, asia-south1
- **asia-east1**: Falls back to → asia-southeast1, asia-northeast1, asia-south1
- **asia-south1**: Falls back to → asia-southeast1, asia-northeast1, asia-east1

### Global Fallback
If your primary region isn't listed above, the default fallback order is:
1. us-central1
2. europe-west4
3. asia-southeast1

## Failover Behavior

The connector automatically fails over to backup regions when:
- Network errors occur (timeouts, connection refused)
- Server errors: 500, 502, 503, 504
- Rate limiting: 429 errors

The connector will NOT fail over for:
- Authentication errors (401, 403)
- Bad requests (400)
- Not found errors (404)

## Available Models by Region

Most Gemini models are available in all regions, but some specialized models may have regional restrictions:

### Widely Available
- gemini-1.5-pro
- gemini-1.5-flash
- text-bison@001
- chat-bison@001
- textembedding-gecko@003

### Regional Availability
- Claude models (via Model Garden): Check specific region availability
- Specialized models: May be limited to certain regions

## Performance Considerations

1. **Latency**: Choose a primary region close to your users
2. **Compliance**: Use regions that meet your data residency requirements
3. **Quotas**: Each region has separate quotas
4. **Costs**: Pricing may vary slightly by region

## Example Usage

```go
// The connector automatically handles multi-region failover
client := starport.NewClient("your-api-key")

// Request will automatically failover if primary region fails
response, err := client.Chat(ctx, &ChatRequest{
    Model: "google-vertexai/gemini-1.5-flash",
    Messages: []Message{
        {Role: "user", Content: "Hello"},
    },
})
```

## Monitoring

The connector logs region failovers:
```
INFO: Vertex AI request failed in us-central1, trying fallback region us-east4
INFO: Successfully completed request in us-east4
```

## Best Practices

1. **Set primary region** closest to your users
2. **Configure fallbacks** in different geographic areas
3. **Monitor failover patterns** to optimize region selection
4. **Test failover** behavior in your environment
5. **Consider costs** when selecting regions

## Troubleshooting

### Authentication Issues
```bash
# Ensure service account has permissions in all regions
gcloud projects add-iam-policy-binding YOUR_PROJECT_ID \
  --member="serviceAccount:YOUR_SERVICE_ACCOUNT@YOUR_PROJECT_ID.iam.gserviceaccount.com" \
  --role="roles/aiplatform.user"
```

### Regional Availability
```bash
# Check if Vertex AI is available in a region
gcloud services list --available --filter="name:aiplatform.googleapis.com" \
  --project=YOUR_PROJECT_ID
```

### Quota Issues
Each region has separate quotas. Check quotas with:
```bash
gcloud compute project-info describe --project=YOUR_PROJECT_ID
```