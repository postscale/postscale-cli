package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	postscale "github.com/postscale/postscale-go"
)

type result struct {
	Data       any                                `json:"data"`
	Context    *executionContext                  `json:"context,omitempty"`
	Pagination *paginationInfo                    `json:"pagination,omitempty"`
	RequestID  string                             `json:"request_id,omitempty"`
	Warnings   []postscale.WebhookDeliveryWarning `json:"warnings,omitempty"`
}

func (a *app) respond(data any, metadata postscale.ResponseMetadata) error {
	return a.printResult(result{Data: data, RequestID: metadata.RequestID})
}

type paginationInfo struct {
	Offset     int  `json:"offset"`
	Limit      int  `json:"limit"`
	Returned   int  `json:"returned"`
	Total      *int `json:"total,omitempty"`
	HasMore    bool `json:"has_more"`
	NextOffset *int `json:"next_offset,omitempty"`
}

func (a *app) encode(out io.Writer, value any) error {
	e := json.NewEncoder(out)
	e.SetEscapeHTML(false)
	if !a.compact {
		e.SetIndent("", "  ")
	}
	return e.Encode(value)
}

func (a *app) print(data any) error { return a.printResult(result{Data: data}) }

func (a *app) printResult(value result) error {
	if a.credentials != nil {
		value.Context = &a.credentials.executionContext
	}
	return a.encode(a.out, value)
}

type errorDetail struct {
	Code              string  `json:"code"`
	Message           string  `json:"message"`
	HTTPStatus        int     `json:"http_status,omitempty"`
	RequestID         string  `json:"request_id,omitempty"`
	RetryAfterSeconds float64 `json:"retry_after_seconds,omitempty"`
}

func (a *app) writeError(err error) int {
	detail := errorDetail{Code: "command_failed", Message: err.Error()}
	exit := 1
	var local *commandError
	var api *postscale.APIError
	var validation *postscale.ValidationError
	switch {
	case isCanceled(err):
		detail.Code, detail.Message, exit = "canceled", "command canceled", 130
	case errors.Is(err, context.DeadlineExceeded):
		detail.Code, detail.Message, exit = "timeout", "command timed out; a mutation may have completed, so inspect its outcome before resubmitting", 5
	case errors.As(err, &local):
		detail.Code, detail.Message, exit = local.code, local.message, local.exit
	case errors.As(err, &validation):
		detail.Code, exit = "validation_error", 2
	case errors.As(err, &api):
		detail.Code, detail.Message = api.Code, api.Message
		detail.HTTPStatus, detail.RequestID = api.StatusCode, api.RequestID
		detail.RetryAfterSeconds = api.RetryAfter.Seconds()
		switch {
		case api.StatusCode == 401 || api.StatusCode == 403:
			exit = 3
		case api.StatusCode == 429:
			exit = 4
		case api.StatusCode == 0:
			exit = 5
			detail.Message = "cannot complete API request; a mutation may have completed, so inspect its outcome before resubmitting"
		}
	}
	// Never print the credential, even if an upstream error includes it.
	if a.credentials != nil {
		detail.Message = strings.ReplaceAll(detail.Message, a.credentials.key, "[REDACTED]")
		detail.Code = strings.ReplaceAll(detail.Code, a.credentials.key, "[REDACTED]")
		detail.RequestID = strings.ReplaceAll(detail.RequestID, a.credentials.key, "[REDACTED]")
	}
	var execution *executionContext
	if a.credentials != nil {
		execution = &a.credentials.executionContext
	}
	_ = a.encode(a.errOut, struct {
		Error   errorDetail       `json:"error"`
		Context *executionContext `json:"context,omitempty"`
	}{detail, execution})
	return exit
}
