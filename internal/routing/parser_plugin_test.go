package routing

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// PLG-V10. A document parser plugin changes what the model reads. It never
// changes which model reads it.
//
// The rule is held here rather than only at the seam that calls the planner,
// because a rule stated once at a call site survives exactly until someone
// adds a second call site. This package is where a route is decided, so this
// is where the plugin has to be absent.

// TestThePlannerCannotSeeADocumentParser states the invariant structurally.
//
// A test that plans two routes and compares them proves the planner ignores
// the plugin today. This proves it cannot read the plugin at all: there is no
// field to carry it in, so a future change that wanted to route on the parser
// engine would have to add one, and adding one fails this test.
func TestThePlannerCannotSeeADocumentParser(t *testing.T) {
	t.Parallel()
	forbidden := []string{"documentparser", "parserengine", "plugin", "recognitionengine"}

	var named []string
	var walk func(reflect.Type, string)
	seen := map[reflect.Type]bool{}
	walk = func(structType reflect.Type, path string) {
		if structType.Kind() != reflect.Struct || seen[structType] {
			return
		}
		seen[structType] = true
		for index := range structType.NumField() {
			field := structType.Field(index)
			named = append(named, path+field.Name)
			fieldType := field.Type
			for fieldType.Kind() == reflect.Pointer || fieldType.Kind() == reflect.Slice {
				fieldType = fieldType.Elem()
			}
			walk(fieldType, path+field.Name+".")
		}
	}
	walk(reflect.TypeOf(Request{}), "")

	require.NotEmpty(t, named)
	for _, name := range named {
		for _, term := range forbidden {
			require.NotContains(t, strings.ToLower(name), term,
				"Request.%s lets a parser plugin reach route planning", name)
		}
	}
}

// TestTheModalityListDecidesTheRouteSoItMustPredateParsing states why the
// caller that plans a chat route reads the modalities off the request the
// caller sent rather than the one the parser produced.
//
// The two plans below differ in one value: whether the request still says it
// carries a document. That single value moves the route to a different model,
// which is the whole hazard. Parsing before planning would erase the document
// modality, and a caller that attached a PDF and named a model that reads one
// would silently land on a model that does not.
func TestTheModalityListDecidesTheRouteSoItMustPredateParsing(t *testing.T) {
	t.Parallel()
	planner := NewPlanner()
	snapshot := parserPluginSnapshot()

	asSent, err := planner.Plan(Request{
		AllowAnyModelFallback: true,
		RequiredModalities:    []Modality{ModalityText, ModalityDocument},
	}, snapshot)
	require.NoError(t, err)
	require.NotEmpty(t, asSent.Attempts())
	require.Equal(t, "author/reads-documents", asSent.Attempts()[0].Route.ModelID)

	// The same request after a plugin turned the attachment into text. Nothing
	// else about it changed, and it plans somewhere else.
	asParsed, err := planner.Plan(Request{
		AllowAnyModelFallback: true,
		RequiredModalities:    []Modality{ModalityText},
	}, snapshot)
	require.NoError(t, err)
	require.NotEmpty(t, asParsed.Attempts())
	require.NotEqual(t, asSent.Attempts()[0].Route.ModelID, asParsed.Attempts()[0].Route.ModelID,
		"the fixture no longer separates the two models, so this proves nothing")
}

// parserPluginSnapshot holds two models the planner can tell apart by what
// they read. The cheaper one reads text alone, so a request that lost its
// document modality prefers it.
func parserPluginSnapshot() Snapshot {
	return Snapshot{
		CatalogGenerationID:  "catalog-generation-7",
		AvailabilityRevision: 3,
		Candidates: []Candidate{
			{
				Route: Route{
					CatalogGenerationID: "catalog-generation-7",
					ModelID:             "author/text-only",
					ProviderID:          "provider-a",
					ProviderModelID:     "text-only-a",
				},
				InputModalities: []Modality{ModalityText},
				ContextWindow:   32_000,
			},
			{
				Route: Route{
					CatalogGenerationID: "catalog-generation-7",
					ModelID:             "author/reads-documents",
					ProviderID:          "provider-b",
					ProviderModelID:     "reads-documents-b",
				},
				InputModalities: []Modality{ModalityText, ModalityDocument},
				ContextWindow:   32_000,
			},
		},
	}
}
