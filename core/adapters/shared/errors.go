package shared

import "github.com/skys-mission/creator-agent/core"

// ClassifyStatus maps an HTTP status code to the corresponding core error type so upper layers
// (retry / reactive middleware) can act on it uniformly, regardless of which provider SDK produced
// the error. Each adapter extracts the status from its own SDK error type and delegates here,
// removing three copies of the same switch. A zero/unknown status returns err unchanged.
func ClassifyStatus(err error, status int) error {
	if err == nil {
		return nil
	}
	switch {
	case status == 429:
		return &core.RateLimitedError{Err: err, StatusCode: status}
	case status >= 500:
		return &core.ServerError{Err: err, StatusCode: status}
	case status >= 400:
		return &core.ClientError{Err: err, StatusCode: status}
	default:
		return err
	}
}
