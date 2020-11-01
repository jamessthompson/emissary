// Copyright 2020 Datawire. All rights reserved.
//
// package acp contains stuff dealing with the Ambassador Control Plane as a whole.
//
// This is the EnvoyWatcher, which is a class that can keep an eye on a running
// Envoy - and just Envoy, all other Ambassador elements are ignored - and tell you
// whether it's alive and ready, or not.
//
// At the moment, "alive" and "ready" mean the same thing for an EnvoyWatcher. Both
// IsAlive() and IsReady() methods exist, though, for a future in which we monitor
// them separately.
//
// TESTING HOOKS:
// Since we try to fetch Envoy stats to see how Envoy is doing, you can use
// EnvoyWatcher.SetFetchStats to change the function that EnvoyWatcher uses to
// fetch stats. The default is EnvoyWatcher.defaultFetcher, which tries to pull
// stats from http://localhost:8001/stats.
//
// This hook is NOT meant for you to change the fetcher on the fly in a running
// EnvoyWatcher. Set it at instantiation, then leave it alone. See envoy_test.go
// for more.

package acp

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"
	"time"

	"github.com/datawire/ambassador/pkg/dlog"
)

// EnvoyWatcher encapsulates state and methods for keeping an eye on a running
// Envoy, and deciding if it's healthy.
type EnvoyWatcher struct {
	// This mutex is mostly rank paranoia, since we've really only the one
	// data element at this point...
	mutex sync.Mutex

	// How shall we fetch Envoy's stats?
	fetchStats envoyFetcher

	// Did the last call to fetch Envoy stats succeed?
	LastSucceeded bool
}

// NewEnvoyWatcher creates a new EnvoyWatcher, given a fetcher.
func NewEnvoyWatcher() *EnvoyWatcher {
	w := &EnvoyWatcher{}
	w.SetFetchStats(w.defaultFetcher)

	return w
}

// This the default Fetcher for the EnvoyWatcher -- it actually connects to Envoy
// and pulls stats.
func (w *EnvoyWatcher) defaultFetcher(ctx context.Context) (*EnvoyFetcherResponse, error) {
	// Set up a context with a deliberate 2-second timeout. Envoy shouldn't ever take more
	// than 100ms to answer the stats request, and if we don't pick a short timeout here,
	// this call can hang for way longer than we would like it to.
	tctx, tcancel := context.WithTimeout(ctx, 2*time.Second)
	defer tcancel()

	// Build a request...
	req, err := http.NewRequestWithContext(tctx, http.MethodGet, "http://localhost:8001/stats", nil)

	if err != nil {
		// ...which should never fail. WTFO?
		return nil, fmt.Errorf("error creating request: %v", err)
	}

	// We were able to create the request, so now fire it off.
	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		// Unlike the last error case, this one isn't a weird situation at
		// all -- e.g. if Envoy isn't running yet, we'll land here.
		return nil, fmt.Errorf("error fetching stats: %v", err)
	}

	// Don't forget to close the body once done.
	defer resp.Body.Close()

	// We're going to return the status code and the response body, so we
	// need to grab those.
	statusCode := resp.StatusCode
	text, err := ioutil.ReadAll(resp.Body)

	if err != nil {
		// This is a bit strange -- if we can't read the body, it implies
		// that something has gone wrong with the connection, so we'll
		// call that an error in fetching the stats.
		return nil, fmt.Errorf("error reading body: %v", err)
	}

	return &EnvoyFetcherResponse{StatusCode: statusCode, Text: text}, nil
}

// SetFetchStats will change the function we use to get the current Envoy stats. This is
// here for testing; the assumption is that you'll call it at instantiation if you need
// to, then leave it alone.
func (w *EnvoyWatcher) SetFetchStats(fetchStats envoyFetcher) {
	w.fetchStats = fetchStats
}

// FetchEnvoyStats will check whether Envoy's statistics are fetchable.
func (w *EnvoyWatcher) FetchEnvoyStats(ctx context.Context) {
	succeeded := false

	// Actually fetch the stats...
	statsResponse, err := w.fetchStats(ctx)

	// ...and see if we were able to.
	if err == nil {
		// Well, nothing blatantly failed, so check the status. (For the
		// moment, we don't care about the text.)
		if statsResponse.StatusCode == 200 {
			succeeded = true
		}
	} else {
		dlog.Debugf(ctx, "could not fetch Envoy status: %v", err)
	}

	w.mutex.Lock()
	defer w.mutex.Unlock()
	w.LastSucceeded = succeeded
}

// IsAlive returns true IFF Envoy should be considered alive.
func (w *EnvoyWatcher) IsAlive() bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	// Currently we just return LastSucceeded: we will not consider Envoy alive
	// unless we were able to talk to it.
	return w.LastSucceeded
}

// IsReady returns true IFF Envoy should be considered ready. Currently Envoy is
// considered ready whenever it's alive; this method is here for future-proofing.
func (w *EnvoyWatcher) IsReady() bool {
	return w.IsAlive()
}
