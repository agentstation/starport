package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

const initializationRollbackTimeout = 5 * time.Second

// deliverCredential prints the one-time gateway credential of an
// initialization. init and the first run of serve both deliver through it.
//
// The credential exists nowhere else in readable form, so a failed write rolls
// the initialization back: an operator who never saw the key must not be left
// with storage that holds it. A result without a credential prints nothing.
// The next line names the step after initialization; an empty value omits it.
func deliverCredential(ctx context.Context, writer io.Writer, result InitResult, asJSON bool, next string) error {
	if result.APIKey == "" {
		return nil
	}
	if err := writeCredential(writer, result, asJSON, next); err != nil {
		return rollbackInitialization(ctx, result, err)
	}
	return nil
}

func writeCredential(writer io.Writer, result InitResult, asJSON bool, next string) error {
	if asJSON {
		encoder := json.NewEncoder(writer)
		encoder.SetEscapeHTML(false)
		// #nosec G117 -- initialization must return the new credential once.
		return encoder.Encode(result)
	}
	if next != "" {
		next += "\n"
	}
	if result.ConfigFile == "" {
		_, err := fmt.Fprintf(
			writer,
			"Initialized Starport API key storage.\nGateway API key (shown once): %s\n%s",
			result.APIKey,
			next,
		)
		return err
	}
	_, err := fmt.Fprintf(
		writer,
		"Initialized Starport.\nConfiguration: %s\nData: %s\nGateway API key (shown once): %s\n%s",
		result.ConfigFile,
		result.DataDir,
		result.APIKey,
		next,
	)
	return err
}

func rollbackInitialization(ctx context.Context, result InitResult, outputErr error) error {
	resultErr := fmt.Errorf("write initialization result: %w", outputErr)
	if result.Rollback == nil {
		return resultErr
	}
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), initializationRollbackTimeout)
	defer cancel()
	if err := result.Rollback(rollbackCtx); err != nil {
		return errors.Join(resultErr, fmt.Errorf("rollback initialization: %w", err))
	}
	return resultErr
}
