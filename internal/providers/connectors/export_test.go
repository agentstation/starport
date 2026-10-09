package connectors

import "net/http"

// ProviderHTTPClient returns the provider HTTP client that the connector owns.
func ProviderHTTPClient(connector *OpenAIConnector) *http.Client {
	return connector.httpClient
}

// DispatchAccounting reports the tracked connections, the in-flight requests,
// and the pending dials of the dispatch transport of one provider client.
func DispatchAccounting(client *http.Client) (tracked, inFlight, dialing int) {
	transport := client.Transport.(*dispatchTransport)
	transport.mu.Lock()
	defer transport.mu.Unlock()
	for _, entry := range transport.connections {
		inFlight += entry.conn.InFlight()
	}
	for _, count := range transport.dialing {
		dialing += count
	}
	return len(transport.connections), inFlight, dialing
}
