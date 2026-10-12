package app

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/endpoint"
	"github.com/agentstation/starport/internal/localauth"
)

// guardStateRoot takes the state root's gateway seat before storage opens, so
// one gateway serves one state root whatever storage it selects. The seat is
// released after the endpoint record goes, because ownership closes in
// reverse order. A scratch root has no record and no seat.
func (b *runtimeBuilder) guardStateRoot() error {
	path := b.config.EndpointRecordFile()
	if path == "" {
		return nil
	}
	lock, err := endpoint.LockRecord(path)
	if err != nil {
		if running := b.runningGateway(); running != "" && errors.Is(err, endpoint.ErrRecordHeld) {
			return fmt.Errorf("%s: %w", running, err)
		}
		return fmt.Errorf("guard state root: %w", err)
	}
	b.application.own("state root guard", func(context.Context) error { return lock.Close() })
	return nil
}

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

// runningGateway names another gateway that serves this state root, from its
// live endpoint record, or returns "". The seat and the local setup guard
// refuse a second gateway on one root, and their own errors do not say which
// gateway holds it. An unspecified bind host reads as loopback, which is
// where the operator reaches it.
func (b *runtimeBuilder) runningGateway() string {
	path := b.config.EndpointRecordFile()
	if path == "" {
		return ""
	}
	record, live, err := endpoint.ReadRecord(path)
	if err != nil || !live || record.PID == os.Getpid() {
		return ""
	}
	url := localauth.BrowsableBase(record.Host, record.Port, record.Scheme == "https")
	return fmt.Sprintf("another gateway (PID %d) serves this state root at %s; stop it before you start a second gateway", record.PID, url)
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
