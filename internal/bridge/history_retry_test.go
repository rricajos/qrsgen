package bridge

import (
	"errors"
	"testing"
)

func TestIsPermanentClientError_4xxNoRetry(t *testing.T) {
	cases := []struct {
		err  error
		want bool
		desc string
	}{
		{errors.New("status 400: bad request"), true, "400 bad request"},
		{errors.New("status 401: unauthorized"), true, "401 unauthorized"},
		{errors.New("status 403: forbidden"), true, "403 forbidden"},
		{errors.New("status 404: not found"), true, "404 not found"},
		{errors.New("status 409: conflict"), true, "409 conflict"},
		{errors.New("status 429: rate limit"), true, "429 rate limit"},
		{errors.New("status 500: internal server error"), false, "500 NO permanente"},
		{errors.New("status 502: bad gateway"), false, "502 NO permanente"},
		{errors.New("status 503: service unavailable"), false, "503 NO permanente"},
		{errors.New("status 504: gateway timeout"), false, "504 NO permanente"},
		{errors.New("dial timeout: connection refused"), false, "timeout NO permanente"},
		{nil, false, "nil → false"},
	}
	for _, tc := range cases {
		if got := isPermanentClientError(tc.err); got != tc.want {
			t.Errorf("%s: got %v want %v (err=%v)", tc.desc, got, tc.want, tc.err)
		}
	}
}
