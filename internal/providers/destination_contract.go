package providers

import (
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

// CompileDestinationGrant derives an approval from one selected provider contract.
// The caller supplies a pinned installation contract or an explicitly approved contract.
// Base URL overrides and resolved parameter bindings require the same approval.
// Never call this function merely because a catalog refresh changes provider facts.
func CompileDestinationGrant(provider catalogs.Provider, identity credentials.DestinationIdentity, profileID catalogs.ProviderCredentialProfileID, baseURL string, bindings map[string]string) (*credentials.DestinationGrant, error) {
	if identity.Provider != provider.ID {
		return nil, credentials.ErrDestinationUnapproved
	}
	profile, destinations, err := approvedDestinationContract(provider, profileID, baseURL, bindings)
	if err != nil {
		return nil, err
	}
	return credentials.NewDestinationGrant(identity, profile, destinations)
}

// CompileDestinationPolicy approves the selected contract for all handles in one credential role.
// The caller must select the contract and configuration through its deployment authority.
func CompileDestinationPolicy(provider catalogs.Provider, role string, profileID catalogs.ProviderCredentialProfileID, baseURL string, bindings map[string]string) (*credentials.DestinationPolicy, error) {
	profile, destinations, err := approvedDestinationContract(provider, profileID, baseURL, bindings)
	if err != nil {
		return nil, err
	}
	return credentials.NewDestinationPolicy(provider.ID, role, profile, destinations)
}

func approvedDestinationContract(provider catalogs.Provider, profileID catalogs.ProviderCredentialProfileID, baseURL string, bindings map[string]string) (catalogs.ProviderCredentialProfile, []credentials.Destination, error) {
	if provider.Inference != nil && provider.Credentials != nil && slices.Contains(provider.Credentials.Inference.Alternatives, profileID) {
		for _, profile := range provider.Credentials.Profiles {
			if profile.ID != profileID {
				continue
			}
			if !approvedDestinationBindings(profile, bindings) {
				break
			}
			destinations, err := contractDestinations(provider.Inference, baseURL, bindings)
			return profile, destinations, err
		}
	}
	return catalogs.ProviderCredentialProfile{}, nil, credentials.ErrDestinationUnapproved
}

func approvedDestinationBindings(profile catalogs.ProviderCredentialProfile, bindings map[string]string) bool {
	for name, value := range bindings {
		if value == "" || strings.ContainsAny(value, "{}") || name == "provider_model_id" || name == "publisher" {
			return false
		}
		declared := slices.ContainsFunc(profile.EndpointBindings, func(binding catalogs.ProviderCredentialEndpointBinding) bool { return binding.Variable == name })
		if !declared {
			return false
		}
	}
	return true
}

func contractDestinations(service *catalogs.ProviderInference, baseURL string, bindings map[string]string) ([]credentials.Destination, error) {
	var destinations []credentials.Destination
	seen := make(map[credentials.Destination]bool)
	for _, endpoint := range service.Endpoints {
		paths := []string{endpoint.Path, endpoint.StreamPath}
		for _, author := range slices.Sorted(maps.Keys(endpoint.PathsByAuthor)) {
			paths = append(paths, endpoint.PathsByAuthor[author])
		}
		for _, author := range slices.Sorted(maps.Keys(endpoint.StreamPathsByAuthor)) {
			paths = append(paths, endpoint.StreamPathsByAuthor[author])
		}
		for _, path := range paths {
			if path == "" {
				continue
			}
			selected := endpoint
			selected.Path = path
			target := service.EndpointURL(selected, baseURL)
			for name, value := range bindings {
				target = strings.ReplaceAll(target, "{"+name+"}", value)
			}
			target, template, err := destinationModelTemplate(target)
			if err != nil {
				return nil, err
			}
			candidates := []credentials.Destination{{Operation: endpoint.Operation, Method: http.MethodPost, URL: target, PathTemplate: template}}
			if endpoint.Operation == catalogs.ProviderOperationVideosGenerations && endpoint.Type == catalogs.EndpointTypeOpenAI {
				collection, parseErr := url.Parse(target)
				if parseErr != nil {
					return nil, credentials.ErrDestinationUnapproved
				}
				job := collection.Clone()
				job.Path = strings.TrimSuffix(job.Path, "/") + "/{job}"
				job.RawPath = ""
				content := job.Clone()
				content.Path += "/content"
				candidates = append(candidates,
					credentials.Destination{Operation: endpoint.Operation, Method: http.MethodGet, URL: job.String(), PathTemplate: true},
					credentials.Destination{Operation: endpoint.Operation, Method: http.MethodDelete, URL: job.String(), PathTemplate: true},
					credentials.Destination{Operation: endpoint.Operation, Method: http.MethodGet, URL: content.String(), PathTemplate: true},
				)
			}
			for _, destination := range candidates {
				if !seen[destination] {
					destinations = append(destinations, destination)
					seen[destination] = true
				}
			}
		}
	}
	return destinations, nil
}

func destinationModelTemplate(target string) (string, bool, error) {
	var output strings.Builder
	template := false
	for {
		literal, rest, found := strings.Cut(target, "{")
		if strings.Contains(literal, "}") {
			return "", false, credentials.ErrDestinationUnapproved
		}
		output.WriteString(literal)
		if !found {
			break
		}
		name, after, closed := strings.Cut(rest, "}")
		if !closed {
			return "", false, credentials.ErrDestinationUnapproved
		}
		switch name {
		case "provider_model_id":
			output.WriteString("{provider_model_id...}")
		case "publisher":
			output.WriteString("{publisher}")
		default:
			return "", false, credentials.ErrDestinationUnapproved
		}
		template = true
		target = after
	}
	return output.String(), template, nil
}
