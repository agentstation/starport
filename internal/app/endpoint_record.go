package app

import (
	"context"
	"net"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/endpoint"
)

// ownEndpointRecord removes the endpoint record when the application closes.
// The lifecycle closes in reverse order, so the record goes before the HTTP
// server drains, and no sibling command reaches a gateway that is closing.
func (b *runtimeBuilder) ownEndpointRecord() {
	path := b.config.EndpointRecordFile()
	if path == "" {
		return
	}
	b.application.own("endpoint record", func(context.Context) error { return endpoint.RemoveRecord(path) })
}

// writeEndpointRecord states the bound address for sibling commands such as
// starport ui. A failure leaves those commands on the configured port, so it
// warns and does not stop the gateway.
func (a *App) writeEndpointRecord(address net.Addr) {
	path := a.config.EndpointRecordFile()
	if path == "" {
		return
	}
	scheme := "http"
	if a.config.Security.EnableTLS {
		scheme = "https"
	}
	record := endpoint.Record{
		SchemaVersion: endpoint.RecordSchemaVersion,
		Scheme:        scheme,
		Host:          a.config.Server.Host,
		Port:          endpoint.Port(address),
		PID:           os.Getpid(),
		StartedAt:     time.Now().UTC(),
	}
	if err := endpoint.WriteRecord(path, record); err != nil {
		log.Warn().Err(err).Str("path", path).Msg("Could not write the gateway endpoint record; starport ui uses the configured port")
	}
}
