// Copyright 2026 The OpenChoreo Authors
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"log/slog"
	"unicode/utf8"

	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/api/gen"
	"github.com/openchoreo/community-modules/observability-logs-azure-loganalytics/internal/loganalytics"
)

const (
	defaultPlatformLimit = 100
	maxPlatformLimit     = 1000

	// maxValueSearchLength mirrors the contract's maxLength on valueSearch.
	maxValueSearchLength = 256
)

// QueryPlatformLogs implements POST /api/v1alpha1/platform-logs/query.
//
// Platform logs share ContainerLogV2 with workload logs; what distinguishes
// them is the filter, not the store. There is no scope to resolve here - the
// observer has already decided who may ask - so this maps the request onto a
// query and back.
func (h *LogsHandler) QueryPlatformLogs(
	ctx context.Context, request gen.QueryPlatformLogsRequestObject,
) (gen.QueryPlatformLogsResponseObject, error) {
	if request.Body == nil {
		return platformBadRequest("request body is required"), nil
	}

	params, msg := platformQueryParams(*request.Body)
	if msg != "" {
		return platformBadRequest(msg), nil
	}

	result, err := h.client.GetPlatformLogs(ctx, params)
	if err != nil {
		h.logger.Error("platform log query failed", slog.Any("error", err))
		return platformInternalError("failed to query platform logs"), nil
	}

	logs := make([]gen.PlatformLog, 0, len(result.Logs))
	for i := range result.Logs {
		logs = append(logs, mapPlatformEntry(&result.Logs[i]))
	}

	return gen.QueryPlatformLogs200JSONResponse(gen.PlatformLogsResponse{
		Logs:   logs,
		Total:  capTotal(result.TotalCount),
		TookMs: result.TookMs,
	}), nil
}

// QueryPlatformLogFilterValues implements
// POST /api/v1alpha1/platform-logs/filter-values.
func (h *LogsHandler) QueryPlatformLogFilterValues(
	ctx context.Context, request gen.QueryPlatformLogFilterValuesRequestObject,
) (gen.QueryPlatformLogFilterValuesResponseObject, error) {
	if request.Body == nil {
		return platformValuesBadRequest("request body is required"), nil
	}

	filter := loganalytics.PlatformLogFilter(request.Body.Filter)
	if !loganalytics.IsListableFilter(filter) {
		return platformValuesBadRequest(
			fmt.Sprintf("filter %q cannot have its values listed", string(request.Body.Filter))), nil
	}

	query, msg := platformQueryParams(request.Body.Query)
	if msg != "" {
		return platformValuesBadRequest(msg), nil
	}

	// limit and sortOrder are a record page size and ordering. No records are
	// returned here, so they are accepted and ignored rather than rejected.
	query.Limit = 0
	query.SortOrder = ""

	maxValues := loganalytics.DefaultMaxFilterValues
	if request.Body.MaxValues != nil {
		maxValues = *request.Body.MaxValues
	}

	valueSearch := ""
	if request.Body.ValueSearch != nil {
		valueSearch = *request.Body.ValueSearch
	}
	// The observer caps this too, but an adapter runs without in-process auth,
	// so it does not assume the observer is the only caller. Counted in runes:
	// the contract's maxLength is characters, and counting bytes would reject a
	// 100-character CJK phrase.
	if utf8.RuneCountInString(valueSearch) > maxValueSearchLength {
		return platformValuesBadRequest(
			fmt.Sprintf("valueSearch cannot exceed %d characters", maxValueSearchLength)), nil
	}

	result, err := h.client.GetPlatformLogFilterValues(ctx, loganalytics.PlatformLogFilterValuesParams{
		Query:       query,
		Filter:      filter,
		ValueSearch: valueSearch,
		MaxValues:   maxValues,
	})
	if err != nil {
		h.logger.Error("platform log filter values query failed",
			slog.String("filter", string(filter)),
			slog.Any("error", err),
		)
		return platformValuesInternalError("failed to query platform log filter values"), nil
	}

	values := make([]gen.PlatformLogFilterValue, 0, len(result.Values))
	for _, v := range result.Values {
		values = append(values, gen.PlatformLogFilterValue{Value: v.Value, Count: v.Count})
	}

	return gen.QueryPlatformLogFilterValues200JSONResponse(gen.PlatformLogFilterValuesResponse{
		Filter:      string(result.Filter),
		Values:      values,
		TotalValues: result.TotalValues,
		TookMs:      result.TookMs,
	}), nil
}

// platformQueryParams maps the wire request onto query params, returning a
// non-empty message when the request is not answerable.
func platformQueryParams(body gen.PlatformLogsQueryRequest) (loganalytics.PlatformLogsParams, string) {
	if body.EndTime.Before(body.StartTime) {
		return loganalytics.PlatformLogsParams{}, "endTime must be greater than or equal to startTime"
	}

	limit := defaultPlatformLimit
	if body.Limit != nil {
		limit = *body.Limit
	}
	if limit < 1 || limit > maxPlatformLimit {
		return loganalytics.PlatformLogsParams{}, "limit must be between 1 and 1000"
	}

	sortOrder := loganalytics.SortDesc
	if body.SortOrder != nil && *body.SortOrder == gen.PlatformLogsQueryRequestSortOrderAsc {
		sortOrder = loganalytics.SortAsc
	}

	labels := map[string]string{}
	if body.Labels != nil {
		labels = *body.Labels
	}
	// A label key becomes a KQL index expression, so it is checked against
	// the Kubernetes grammar. A malformed key is refused rather than dropped:
	// dropping a filter widens the query.
	if key, ok := loganalytics.FirstInvalidLabelKey(labels); !ok {
		return loganalytics.PlatformLogsParams{},
			fmt.Sprintf("label key %q is not a valid Kubernetes label key", key)
	}

	logLevels := []string{}
	if body.LogLevels != nil {
		for _, l := range *body.LogLevels {
			logLevels = append(logLevels, string(l))
		}
	}

	return loganalytics.PlatformLogsParams{
		ClusterInstances: derefSlice(body.ClusterInstance),
		Namespaces:       derefSlice(body.Namespace),
		PodNames:         derefSlice(body.PodName),
		ContainerNames:   derefSlice(body.ContainerName),
		Labels:           labels,
		StartTime:        body.StartTime,
		EndTime:          body.EndTime,
		Limit:            limit,
		SortOrder:        sortOrder,
		SearchPhrase:     derefString(body.SearchPhrase),
		LogLevels:        logLevels,
	}, ""
}

// mapPlatformEntry shapes one record for the wire.
//
// PodIp is left unset: ContainerLogV2 has no pod-IP column and
// KubernetesMetadata carries none, so there is nothing to report. The field
// is optional in the contract.
func mapPlatformEntry(e *loganalytics.PlatformLogEntry) gen.PlatformLog {
	entry := gen.PlatformLog{
		Timestamp:       e.Timestamp,
		Log:             e.LogMessage,
		Level:           ptrStringNonEmpty(e.Level),
		ClusterInstance: ptrStringNonEmpty(e.ClusterInstance),
		NamespaceName:   ptrStringNonEmpty(e.PodNamespace),
		PodName:         ptrStringNonEmpty(e.PodName),
		ContainerName:   ptrStringNonEmpty(e.ContainerName),
		NodeName:        ptrStringNonEmpty(e.NodeName),
		ContainerImage:  ptrStringNonEmpty(e.ContainerImage),
	}
	if len(e.Labels) > 0 {
		labels := e.Labels
		entry.Labels = &labels
	}
	return entry
}

func derefSlice(s *[]string) []string {
	if s == nil {
		return nil
	}
	return *s
}

func platformBadRequest(message string) gen.QueryPlatformLogs400JSONResponse {
	return gen.QueryPlatformLogs400JSONResponse(
		makeError(gen.BadRequest, errCodeBadRequest, message))
}

func platformInternalError(message string) gen.QueryPlatformLogs500JSONResponse {
	return gen.QueryPlatformLogs500JSONResponse(
		makeError(gen.InternalServerError, errCodeInternal, message))
}

func platformValuesBadRequest(message string) gen.QueryPlatformLogFilterValues400JSONResponse {
	return gen.QueryPlatformLogFilterValues400JSONResponse(
		makeError(gen.BadRequest, errCodeBadRequest, message))
}

func platformValuesInternalError(message string) gen.QueryPlatformLogFilterValues500JSONResponse {
	return gen.QueryPlatformLogFilterValues500JSONResponse(
		makeError(gen.InternalServerError, errCodeInternal, message))
}
