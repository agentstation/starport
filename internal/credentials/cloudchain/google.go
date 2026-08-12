package cloudchain

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/auth"
	googlecredentials "cloud.google.com/go/auth/credentials"
	"github.com/agentstation/starmap/pkg/catalogs"

	"github.com/agentstation/starport/internal/credentials"
)

const defaultRefreshBefore = 2 * time.Minute

type googleDefaultChain struct {
	provider       auth.TokenProvider
	projectID      func(context.Context) (string, error)
	quotaProjectID func(context.Context) (string, error)
}

func (googleDefaultChain) SuppliedFields(
	profile catalogs.ProviderCredentialProfile,
	fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
) ([]catalogs.ProviderCredentialFieldID, error) {
	supplied, err := bearerSuppliedFields(profile, fields)
	if err != nil {
		return nil, err
	}
	options := profile.ProtocolOptions.GoogleDefault
	if options == nil {
		return supplied, nil
	}
	for _, fieldID := range []catalogs.ProviderCredentialFieldID{
		options.ProjectField,
		options.QuotaProjectField,
	} {
		if fieldID != "" {
			supplied = append(supplied, fieldID)
		}
	}
	return supplied, nil
}

func (chain googleDefaultChain) Resolve(
	ctx context.Context,
	profile catalogs.ProviderCredentialProfile,
	fields map[catalogs.ProviderCredentialFieldID]catalogs.ProviderCredentialField,
) (credentials.SourceMaterial, error) {
	provider := chain.provider
	projectID := chain.projectID
	quotaProjectID := chain.quotaProjectID
	if provider == nil {
		credential, err := googlecredentials.DetectDefault(&googlecredentials.DetectOptions{
			Scopes:              append([]string(nil), profile.Scopes...),
			EarlyTokenRefresh:   0,
			DisableAsyncRefresh: true,
		})
		if err != nil {
			return credentials.SourceMaterial{}, credentials.NewSourceError(
				credentials.SourceErrorNotConfigured,
				"google-default",
			)
		}
		provider = credential.TokenProvider
		projectID = credential.ProjectID
		quotaProjectID = credential.QuotaProjectID
	}
	if provider == nil {
		return credentials.SourceMaterial{}, errors.New("google token provider is required")
	}
	token, err := provider.Token(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return credentials.SourceMaterial{}, ctx.Err()
		}
		return credentials.SourceMaterial{}, fmt.Errorf("get Google access token: %w", err)
	}
	fieldID, err := bearerField(profile, fields)
	if err != nil {
		return credentials.SourceMaterial{}, err
	}
	if token == nil || token.Value == "" {
		return credentials.SourceMaterial{}, credentials.NewSourceError(
			credentials.SourceErrorInvalid,
			"google-default",
		)
	}
	values := map[string]string{string(fieldID): token.Value}
	if options := profile.ProtocolOptions.GoogleDefault; options != nil {
		if err := resolveGoogleProperty(ctx, values, options.ProjectField, projectID); err != nil {
			return credentials.SourceMaterial{}, err
		}
		if err := resolveGoogleProperty(
			ctx,
			values,
			options.QuotaProjectField,
			quotaProjectID,
		); err != nil {
			return credentials.SourceMaterial{}, err
		}
	}
	return renewableCloudMaterial(values, token.Expiry), nil
}

func resolveGoogleProperty(
	ctx context.Context,
	values map[string]string,
	fieldID catalogs.ProviderCredentialFieldID,
	resolve func(context.Context) (string, error),
) error {
	if fieldID == "" || resolve == nil {
		return nil
	}
	value, err := resolve(ctx)
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil && value != "" {
		values[string(fieldID)] = value
	}
	return nil
}
