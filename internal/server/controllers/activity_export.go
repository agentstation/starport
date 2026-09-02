package controllers

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/rs/zerolog/log"

	"github.com/agentstation/starport/internal/server/dto"
	"github.com/agentstation/starport/internal/server/requestctx"
	"github.com/agentstation/starport/internal/usage"
)

// activityExportMaxPages bounds one export walk. With full pages that is one
// million records, far past what retention keeps.
const activityExportMaxPages = 1000

// fieldStatus is the status word three controller surfaces share: the CSV
// header, the activity filter, and the admin health payload.
const fieldStatus = "status"

// fieldKeyID is the wire name for a gateway key's identifier on every
// surface this package writes it to: the CSV header, event payloads, and
// the key deletion response.
const fieldKeyID = "key_id"

// activityExportCSVHeader names the flat columns the CSV format carries. The
// NDJSON format carries the whole record; CSV keeps the columns a spreadsheet
// reads.
var activityExportCSVHeader = []string{
	"request_id", "timestamp", fieldKeyID, "account_id", "operation",
	"model_requested", "model_used", "provider", fieldStatus, "status_code",
	"streaming", "tokens_input", "tokens_output", "tokens_total",
	"latency_ms", "overhead_ms", "ttft_ms",
	"cost_nano_usd", "cost_currency", "cost_unavailable_reason",
	"cache_status", "cache_semantic", "cache_similarity",
	"guardrail_verdict", "guardrail_check",
}

// ActivityExport handles GET /api/v1/activity/export. It streams the
// authenticated key's records under the same filters the listing takes, as
// NDJSON by default or CSV with format=csv. The export reads the store the
// listing reads, so an exported line matches the stored record.
func (h *ActivityController) ActivityExport(w http.ResponseWriter, r *http.Request) {
	if h.usageRecords == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, usageNotConfiguredMessage)
		return
	}

	keyID, ok := requestctx.GetAPIKeyID(r.Context())
	if !ok || keyID == "" {
		dto.WriteError(w, http.StatusUnauthorized, dto.ErrorTypeAuthenticationError, "Authentication is required")
		return
	}

	query, err := activityQueryFromRequest(r)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}
	query.KeyID = keyID
	h.exportRecords(w, r, query)
}

// AdminExport handles GET /api/v1/admin/activity/export. It streams the
// records the admin listing reads under the same filters, across every key
// unless key_id names one. The console's usage page downloads here, so the
// file it saves matches the rows it shows.
func (h *ActivityController) AdminExport(w http.ResponseWriter, r *http.Request) {
	if h.usageRecords == nil {
		dto.WriteError(w, http.StatusServiceUnavailable, dto.ErrorTypeServerError, usageNotConfiguredMessage)
		return
	}

	query, err := activityQueryFromRequest(r)
	if err != nil {
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, err.Error())
		return
	}
	query.KeyID = r.URL.Query().Get(fieldKeyID)
	h.exportRecords(w, r, query)
}

// exportRecords walks every page the query reaches and streams it in the
// format the request names. The caller owns the KeyID scope decision.
func (h *ActivityController) exportRecords(w http.ResponseWriter, r *http.Request, query usage.Query) {
	query.Limit = usage.MaxListLimit

	format := r.URL.Query().Get("format")
	var write func([]usage.Record) error
	var finish func() error
	switch format {
	case "", "ndjson":
		w.Header().Set("Content-Type", usage.NDJSONContentType)
		encoder := json.NewEncoder(w)
		write = func(records []usage.Record) error {
			for _, record := range records {
				if err := encoder.Encode(record); err != nil {
					return err
				}
			}
			return nil
		}
		finish = func() error { return nil }
	case "csv":
		w.Header().Set("Content-Type", "text/csv")
		writer := csv.NewWriter(w)
		if err := writer.Write(activityExportCSVHeader); err != nil {
			log.Error().Err(err).Msg("Failed to write export header")
			return
		}
		write = func(records []usage.Record) error {
			for _, record := range records {
				if err := writer.Write(activityExportCSVRow(record)); err != nil {
					return err
				}
			}
			return nil
		}
		finish = func() error {
			writer.Flush()
			return writer.Error()
		}
	default:
		dto.WriteError(w, http.StatusBadRequest, dto.ErrorTypeInvalidRequest, "format must be ndjson or csv")
		return
	}

	for page := 0; page < activityExportMaxPages; page++ {
		result, err := h.usageRecords.List(r.Context(), query)
		if err != nil {
			// Headers are already written; ending the stream is the only
			// honest failure signal left.
			log.Error().Err(err).Msg("Failed to export activity")
			return
		}
		if err := write(result.Records); err != nil {
			log.Error().Err(err).Msg("Failed to write export records")
			return
		}
		query.Cursor = result.NextCursor
		if query.Cursor == "" {
			break
		}
	}
	if err := finish(); err != nil {
		log.Error().Err(err).Msg("Failed to finish export")
	}
}

func activityExportCSVRow(record usage.Record) []string {
	costNanoUSD, costCurrency := "", ""
	if record.Cost != nil {
		costNanoUSD = strconv.FormatInt(record.Cost.NanoUSD, 10)
		costCurrency = record.Cost.Currency
	}
	return []string{
		record.RequestID,
		record.Timestamp.UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		record.KeyID,
		record.AccountID,
		record.Operation,
		record.ModelRequested,
		record.ModelUsed,
		record.Provider,
		record.Status,
		strconv.Itoa(record.StatusCode),
		strconv.FormatBool(record.Streaming),
		strconv.FormatInt(record.Tokens.Input, 10),
		strconv.FormatInt(record.Tokens.Output, 10),
		strconv.FormatInt(record.Tokens.Total, 10),
		strconv.FormatInt(record.LatencyMS, 10),
		strconv.FormatInt(record.OverheadMS, 10),
		strconv.FormatInt(record.TTFTMS, 10),
		costNanoUSD,
		costCurrency,
		record.CostUnavailableReason,
		record.CacheStatus,
		strconv.FormatBool(record.CacheSemantic),
		cacheSimilarityColumn(record.CacheSimilarity),
		record.GuardrailVerdict,
		record.GuardrailCheck,
	}
}

// cacheSimilarityColumn writes a semantic similarity as a short decimal and
// leaves an exact hit or a miss blank, so a spreadsheet column reads as
// "no similarity" rather than as a zero score.
func cacheSimilarityColumn(similarity float64) string {
	if similarity <= 0 {
		return ""
	}
	return strconv.FormatFloat(similarity, 'f', 4, 64)
}
