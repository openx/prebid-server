package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io/ioutil"
	httpCore "net/http"
	"net/url"
	"time"

	"golang.org/x/net/context/ctxhttp"

	"github.com/buger/jsonparser"
	"github.com/prebid/prebid-server/config"
	"github.com/prebid/prebid-server/stored_requests/events"

	"github.com/golang/glog"
)

// NewHTTPEvents makes an EventProducer which creates events by pinging an external HTTP API
// for updates periodically. If refreshRate is negative, then the data will never be refreshed.
//
// It expects the following endpoint to exist remotely:
//
// GET {endpoint}
//   -- Returns all the known Stored Requests and Stored Imps.
// GET {endpoint}?last-modified={timestamp}
//   -- Returns the Stored Requests and Stored Imps which have been updated since the last timestamp.
//      This timestamp will be sent in the rfc3339 format, using UTC and no timezone shift.
//      For more info, see: https://tools.ietf.org/html/rfc3339
//
// The responses should be JSON like this:
//
// {
//   "requests": {
//     "request1": { ... stored request data ... },
//     "request2": { ... stored request data ... },
//     "request3": { ... stored request data ... },
//   },
//   "imps": {
//     "imp1": { ... stored data for imp1 ... },
//     "imp2": { ... stored data for imp2 ... },
//   }
// }
// or
// {
//   "accounts": {
//     "acc1": { ... config data for acc1 ... },
//     "acc2": { ... config data for acc2 ... },
//   },
// }
//
// To signal deletions, the endpoint may return { "deleted": true }
// in place of the Stored Data if the "last-modified" param existed.
//
func NewHTTPEvents(client *httpCore.Client, endpoint string, ctxProducer func() (ctx context.Context, canceller func()), refreshRate time.Duration) *HTTPEvents {
	// If we're not given a function to produce Contexts, use the Background one.
	if ctxProducer == nil {
		ctxProducer = func() (ctx context.Context, canceller func()) {
			return context.Background(), func() {}
		}
	}
	e := &HTTPEvents{
		client:        client,
		ctxProducer:   ctxProducer,
		Endpoint:      endpoint,
		lastUpdate:    time.Now().UTC(),
		saves:         make(chan events.Save, 3),         // capacity for one of each data type
		invalidations: make(chan events.Invalidation, 3), // capacity for one of each data type
	}
	glog.Infof("Loading HTTP cache from GET %s", endpoint)
	e.fetchAll()

	go e.refresh(time.Tick(refreshRate))
	return e
}

type HTTPEvents struct {
	client        *httpCore.Client
	ctxProducer   func() (ctx context.Context, canceller func())
	Endpoint      string
	invalidations chan events.Invalidation
	lastUpdate    time.Time
	saves         chan events.Save
}

func (e *HTTPEvents) fetchAll() {
	ctx, cancel := e.ctxProducer()
	defer cancel()
	resp, err := ctxhttp.Get(ctx, e.client, e.Endpoint)
	if respObj, ok := e.parse(e.Endpoint, resp, err); ok {
		if len(respObj.StoredRequests) > 0 {
			e.saves <- events.Save{DataType: config.RequestDataType, Data: respObj.StoredRequests}
		}
		if len(respObj.StoredImps) > 0 {
			e.saves <- events.Save{DataType: config.ImpDataType, Data: respObj.StoredImps}
		}
		if len(respObj.Accounts) > 0 {
			e.saves <- events.Save{DataType: config.AccountDataType, Data: respObj.Accounts}
		}
	}
}

func (e *HTTPEvents) refresh(ticker <-chan time.Time) {
	for {
		select {
		case thisTime := <-ticker:
			thisTimeInUTC := thisTime.UTC()

			// Parse the endpoint url defined
			endpointUrl, urlErr := url.Parse(e.Endpoint)

			// Error with url parsing
			if urlErr != nil {
				glog.Errorf("Disabling refresh HTTP cache from GET '%s': %v", e.Endpoint, urlErr)
				return
			}

			// Parse the url query string
			urlQuery := endpointUrl.Query()

			// See the last-modified query param
			urlQuery.Set("last-modified", e.lastUpdate.Format(time.RFC3339))

			// Rebuild
			endpointUrl.RawQuery = urlQuery.Encode()

			// Convert to string
			endpoint := endpointUrl.String()

			glog.Infof("Refreshing HTTP cache from GET '%s'", endpoint)

			ctx, cancel := e.ctxProducer()
			resp, err := ctxhttp.Get(ctx, e.client, endpoint)
			if respObj, ok := e.parse(endpoint, resp, err); ok {
				sendInvalidations(e.invalidations, config.RequestDataType, respObj.StoredRequests)
				sendInvalidations(e.invalidations, config.ImpDataType, respObj.StoredImps)
				sendInvalidations(e.invalidations, config.AccountDataType, respObj.Accounts)
				sendSaves(e.saves, config.RequestDataType, respObj.StoredRequests)
				sendSaves(e.saves, config.ImpDataType, respObj.StoredImps)
				sendSaves(e.saves, config.AccountDataType, respObj.Accounts)
				e.lastUpdate = thisTimeInUTC
			}
			cancel()
		}
	}
}

// parse unpacks the HTTP response and sends the relevant events to the channels.
// It returns true if everything was successful, and false if any errors occurred.
func (e *HTTPEvents) parse(endpoint string, resp *httpCore.Response, err error) (*responseContract, bool) {
	if err != nil {
		glog.Errorf("Failed call: GET %s for Stored Requests: %v", endpoint, err)
		return nil, false
	}
	defer resp.Body.Close()

	respBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		glog.Errorf("Failed to read body of GET %s for Stored Requests: %v", endpoint, err)
		return nil, false
	}

	if resp.StatusCode != httpCore.StatusOK {
		glog.Errorf("Got %d response from GET %s for Stored Requests. Response body was: %s", resp.StatusCode, endpoint, string(respBytes))
		return nil, false
	}

	var respObj responseContract
	if err := json.Unmarshal(respBytes, &respObj); err != nil {
		glog.Errorf("Failed to unmarshal body of GET %s for Stored Requests: %v", endpoint, err)
		return nil, false
	}

	return &respObj, true
}

func sendInvalidations(invalidations chan<- events.Invalidation, dataType config.DataType, changes map[string]json.RawMessage) {
	if len(changes) == 0 {
		return
	}
	deletedIDs := make([]string, 0, len(changes))
	for id, msg := range changes {
		if value, _, _, err := jsonparser.Get(msg, "deleted"); err == nil && bytes.Equal(value, []byte("true")) {
			delete(changes, id)
			deletedIDs = append(deletedIDs, id)
		}
	}
	if len(deletedIDs) > 0 {
		invalidations <- events.Invalidation{
			DataType: dataType,
			Data:     deletedIDs,
		}
	}
}

func sendSaves(saves chan<- events.Save, dataType config.DataType, changes map[string]json.RawMessage) {
	if len(changes) > 0 {
		saves <- events.Save{DataType: dataType, Data: changes}
	}
}

func (e *HTTPEvents) Saves() <-chan events.Save {
	return e.saves
}

func (e *HTTPEvents) Invalidations() <-chan events.Invalidation {
	return e.invalidations
}

type responseContract struct {
	StoredRequests map[string]json.RawMessage `json:"requests"`
	StoredImps     map[string]json.RawMessage `json:"imps"`
	Accounts       map[string]json.RawMessage `json:"accounts"`
}
