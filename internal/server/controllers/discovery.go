package controllers

import (
	"encoding/json/v2"
	"net/http"
	"reflect"

	"github.com/agentstation/starmap/pkg/catalogs"
	"github.com/agentstation/starport/internal/account"
	"github.com/agentstation/starport/internal/apikey"
	"github.com/agentstation/starport/internal/catalog/disclosure"
	"github.com/agentstation/starport/internal/providers/connectors"
)

// DiscoveryViewer binds the current key and account policy to their revisions.
// This record is internal authorization state and must never reach a response.
type DiscoveryViewer struct {
	Key             apikey.APIKey
	Account         account.Account
	KeyRevision     uint64
	AccountRevision uint64
}

// DiscoveryViewerReader reloads current disclosure policy for an authenticated request.
type DiscoveryViewerReader func(*http.Request) (DiscoveryViewer, error)

// DiscoveryController serves permitted catalog membership without credential probes.
type DiscoveryController struct {
	registry connectors.LeasingRegistry
	viewer   DiscoveryViewerReader
}

// NewDiscoveryController binds catalog leases and current viewer policy.
func NewDiscoveryController(registry connectors.LeasingRegistry, viewer DiscoveryViewerReader) *DiscoveryController {
	return &DiscoveryController{registry: registry, viewer: viewer}
}

type discoveryModel struct {
	ID          string              `json:"id"`
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	Offerings   []discoveryOffering `json:"offerings"`
}

type discoveryOffering struct {
	Provider           string                       `json:"provider"`
	ProviderModelID    string                       `json:"provider_model_id"`
	Availability       string                       `json:"availability"`
	Lifecycle          string                       `json:"lifecycle"`
	Operations         []catalogs.ProviderOperation `json:"operations"`
	RoutableOperations []catalogs.ProviderOperation `json:"routable_operations"`
	Readiness          string                       `json:"readiness"`
}

type discoveryResponse struct {
	GenerationID string           `json:"generation_id"`
	Models       []discoveryModel `json:"models"`
}

func validDiscoveryViewer(viewer DiscoveryViewer) bool {
	return viewer.Key.Active && !viewer.Key.IsExpired() && viewer.Account.Active &&
		viewer.Key.EffectiveAccountID() == viewer.Account.ID && (viewer.Key.HasScope("models:read") || viewer.Key.HasScope("admin"))
}

// List handles GET /api/v1/catalog/discovery with current disclosure policy.
func (h *DiscoveryController) List(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if h.registry == nil || h.viewer == nil {
		writeCatalogUnavailable(w)
		return
	}
	viewer, err := h.viewer(r)
	if err != nil || !validDiscoveryViewer(viewer) {
		writeCatalogUnavailable(w)
		return
	}
	lease, err := h.registry.AcquireRuntime()
	if err != nil || lease == nil {
		writeCatalogUnavailable(w)
		return
	}
	defer lease.Release()
	snapshot := lease.Snapshot()
	if snapshot == nil || snapshot.CheckNewAttempt() != nil {
		writeCatalogUnavailable(w)
		return
	}
	facts, err := snapshot.Discover(disclosure.New(snapshot, viewer.Key, viewer.Account))
	if err != nil {
		writeCatalogUnavailable(w)
		return
	}
	response := discoveryResponse{GenerationID: facts.GenerationID, Models: make([]discoveryModel, 0, len(facts.Models))}
	supported := make(map[catalogs.OfferingKey][]catalogs.ProviderOperation)
	for _, route := range snapshot.Routes() {
		supported[route.Key()] = route.Operations
	}
	for _, model := range facts.Models {
		projected := discoveryModel{ID: string(model.Definition.ID), Name: model.Definition.Name, Description: model.Definition.Description, Offerings: make([]discoveryOffering, 0, len(model.Offerings))}
		for _, offering := range model.Offerings {
			operations := supported[catalogs.OfferingKey{ProviderID: offering.ProviderID, ProviderModelID: offering.ProviderModelID}]
			if operations == nil {
				operations = []catalogs.ProviderOperation{}
			}
			projected.Offerings = append(projected.Offerings, discoveryOffering{
				Provider: string(offering.ProviderID), ProviderModelID: string(offering.ProviderModelID),
				Availability: string(offering.Availability), Lifecycle: string(offering.Lifecycle),
				Operations: offering.Service.Operations, RoutableOperations: operations, Readiness: "unknown",
			})
		}
		response.Models = append(response.Models, projected)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		writeCatalogUnavailable(w)
		return
	}
	current, err := h.viewer(r)
	if err != nil || !validDiscoveryViewer(current) || !reflect.DeepEqual(viewer, current) || snapshot.CheckNewAttempt() != nil {
		writeCatalogUnavailable(w)
		return
	}
	if r.Context().Err() != nil {
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(payload)
}
