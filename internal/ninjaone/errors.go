package ninjaone

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

const maxErrBody = 2 << 10

// APIError is a non-2xx reply from NinjaOne. Error() is written for the
// model: what failed, NinjaOne's own words, and what to try next.
type APIError struct {
	Status     int
	Method     string
	Path       string // never includes the query string
	Code       string // error / resultCode / errorCode, whichever was sent
	Detail     string // NinjaOne's message, capped at 2 KiB
	IncidentID string // NinjaOne support asks for this
	RequestID  string // x-nj-request-id
	Hint       string
	Throttled  bool
}

func (e *APIError) Error() string {
	s := fmt.Sprintf("NinjaOne %s %s: HTTP %d", e.Method, e.Path, e.Status)
	if e.Code != "" {
		s += " " + e.Code
	}
	if e.Detail != "" && e.Detail != e.Code {
		s += ": " + e.Detail
	}
	if e.IncidentID != "" {
		s += " (incidentId " + e.IncidentID + ")"
	}
	if e.Hint != "" {
		s += "\nHint: " + e.Hint
	}
	return s
}

func newAPIError(method, path string, resp *http.Response, body []byte, scrub func(string) string) *APIError {
	e := &APIError{Status: resp.StatusCode, Method: method, Path: path, RequestID: resp.Header.Get("x-nj-request-id")}
	e.decode(body)
	e.Detail = scrub(e.Detail)
	if len(e.Detail) > maxErrBody {
		e.Detail = e.Detail[:maxErrBody] + "…"
	}
	e.Hint = hint(e)
	return e
}

// decode reads NinjaOne's three error shapes leniently:
//
//	auth layer:  {error, error_description, error_code}
//	app layer:   {resultCode, errorMessage, errorDetails, incidentId}
//	billing/PSA: {errorCode, message}
func (e *APIError) decode(body []byte) {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil {
		if html(http.Header{}, body) {
			e.Detail = "(HTML page instead of JSON)"
		} else {
			e.Detail = strings.TrimSpace(string(body))
		}
		return
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if s, ok := m[k].(string); ok && s != "" {
				return s
			}
		}
		return ""
	}
	e.Code = str("resultCode", "error", "errorCode")
	e.Detail = str("errorMessage", "error_description", "message")
	if d, ok := m["errorDetails"]; ok && d != nil {
		b, _ := json.Marshal(d)
		e.Detail = strings.TrimSpace(e.Detail + " " + string(b))
	}
	e.IncidentID = str("incidentId")
	if e.Detail == "" && e.Code == "" {
		e.Detail = strings.TrimSpace(string(body))
	}
}

// permissionCodes are resultCodes that mean "forbidden" whatever the status
// (some billing endpoints document 500 for them).
var permissionCodes = []string{"permission_denied", "organization_access_violation", "user_context_required"}

func hint(e *APIError) string {
	tokenEndpoint := strings.HasPrefix(e.Path, "/ws/oauth")
	switch {
	case tokenEndpoint && e.Status == http.StatusNotFound:
		return "NinjaOne does not know this client ID on this instance: usually the wrong region (each tenant lives on one host) or a mistyped client ID."
	case tokenEndpoint && (e.Status == http.StatusBadRequest || e.Status == http.StatusUnauthorized):
		return "the client credentials were rejected: check the client secret, and that the API Services app allows the client_credentials grant and the requested scopes."
	case e.Code == "user_context_required":
		return "this endpoint needs a signed-in NinjaOne user, which a client-credentials API client cannot provide; do it in the NinjaOne console instead."
	case e.Status == http.StatusForbidden || (e.Status == http.StatusInternalServerError && containsCode(e.Code)):
		return "the API client lacks permission or scope for this (writes need the management scope). ninjaone_status shows the granted scopes."
	case e.Status == http.StatusBadRequest && (e.Code == "missing_header" || e.Code == "invalid_header"):
		return "the Authorization header was malformed; this is a server bug, retrying will not help."
	case e.Status == http.StatusBadRequest:
		return "NinjaOne rejected the request; the message above names the problem. Fix the parameters or body and retry."
	case e.Status == http.StatusUnauthorized:
		return "the access token was rejected even after re-authenticating; check the API client with ninjaone_status."
	case e.Status == http.StatusNotFound:
		return "no such object, or this route does not exist on this NinjaOne instance (instances run different versions; fed lacks many endpoints). List to find the right id."
	case e.Status == http.StatusConflict:
		return "NinjaOne refused due to a conflict, e.g. a stale ticket version. Re-read the object and retry."
	case e.Status >= 500:
		return "NinjaOne hit an internal error; quote the incidentId if it persists."
	}
	return ""
}

func containsCode(code string) bool {
	for _, c := range permissionCodes {
		if strings.EqualFold(code, c) {
			return true
		}
	}
	return false
}

func hintThrottle(retried bool) string {
	if retried {
		return "NinjaOne is rate limiting (it sends an HTML page rather than JSON); the request was retried with backoff and still throttled. Wait a minute and make fewer, narrower calls."
	}
	return "NinjaOne is rate limiting. Writes are never retried automatically: check whether it took effect before trying again."
}
