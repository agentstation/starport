package router

import (
	"context"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/inference"
	"github.com/agentstation/starport/internal/providers/connectors"
	"github.com/agentstation/starport/internal/routing"
)

// Recognition is a media route with no path of its own. Nothing outside the
// gateway asks for it: a chat turn arrives with a document whose pages carry
// no text, and the gateway orders the read. It still plans, retries, selects a
// credential, and spends the same attempt budget as every other route, so it
// is written against the same shared path rather than beside it.

// RecognitionRequest routes one document read.
type RecognitionRequest = MediaRequest[inference.RecognitionRequest]

// RecognitionResponse is one document's recognized pages with route evidence.
type RecognitionResponse = MediaResponse[inference.RecognitionResponse]

// RouteDocumentRecognition reads the text off one document's pages at a
// provider the catalog serves the operation for.
func (r *modelRouter) RouteDocumentRecognition(
	ctx context.Context,
	req *RecognitionRequest,
) (*RecognitionResponse, error) {
	// A recognition request usually names no model. The caller asked for an
	// engine, and which offering serves that engine is what the catalog states,
	// so the planner picks one from the offerings that serve the operation
	// under this key's own model and provider restrictions.
	if req == nil || !req.Request.Document.Present() {
		return nil, ErrNoModelsAvailable
	}
	call := mediaCall[*connectors.RecognitionRequest, *connectors.RecognitionResponse, inference.RecognitionResponse]{
		transport: recognitionTransport,
		build: func() *connectors.RecognitionRequest {
			return connectors.RecognitionRequestFromInference(req.Request)
		},
		convert: connectors.RecognitionResponseToInference,
	}
	return routeMedia(ctx, r, req.policy(req.Request.Model), routing.OperationDocumentsRecognition,
		inference.RecognitionResponse.Clone, call.attempt(routing.OperationDocumentsRecognition))
}

func recognitionTransport(
	connector connectors.Connector,
	endpointType catalogs.EndpointType,
) (mediaInvoke[*connectors.RecognitionRequest, *connectors.RecognitionResponse], bool) {
	recognizer, implemented := connectors.DocumentRecognizerFor(connector, endpointType)
	if !implemented {
		return nil, false
	}
	return recognizer.RecognizeDocument, true
}
